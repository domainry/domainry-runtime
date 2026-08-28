package transaction

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/domainry/domainry-foundation/mutation"
	"github.com/domainry/domainry-foundation/requestcontext"
	transactioncontract "github.com/domainry/domainry-runtime/runtime/domain/transaction/contract"
)

var (
	ErrUnitOfWorkDatabaseRequired = errors.New("unit of work database is required")
	ErrUnitOfWorkBinderRequired   = errors.New("unit of work port binder is required")
	ErrUnitOfWorkIsolationInvalid = errors.New("unit of work isolation is invalid")
	ErrNestedUnitOfWork           = errors.New("nested unit of work requires an explicit savepoint contract")
	ErrUnitOfWorkRetryUnsafe      = errors.New("unit of work retry requires transactional-only side effects")
	ErrUnitOfWorkRetryIdentity    = errors.New("unit of work retry requires one idempotency scope and correlation id")
	ErrUnitOfWorkRetryPolicy      = errors.New("unit of work retry policy is invalid")
	ErrUnitOfWorkRetryBudget      = errors.New("unit of work retry budget exhausted")
	ErrUnitOfWorkRetryDeadline    = errors.New("unit of work retry deadline exceeded")
)

// SQLUnitOfWork binds one owner-scoped port set to a database transaction.
// The sql.Tx remains infrastructure-private; Application receives only Ports.
type SQLUnitOfWork[Ports transactioncontract.PortSet] struct {
	database            *sql.DB
	options             transactioncontract.Options
	bind                func(*sql.Tx) Ports
	now                 func() time.Time
	wait                func(context.Context, time.Duration) error
	metrics             mutation.TransactionMetricsCollector
	afterCommitFailures *afterCommitFailureStore
}

func NewSQLUnitOfWork[Ports transactioncontract.PortSet](database *sql.DB, options transactioncontract.Options, bind func(*sql.Tx) Ports, metrics ...mutation.TransactionMetricsCollector) *SQLUnitOfWork[Ports] {
	collector := mutation.TransactionMetricsCollector(mutation.NewMemoryTransactionMetricsCollector())
	if len(metrics) > 0 && metrics[0] != nil {
		collector = metrics[0]
	}
	return &SQLUnitOfWork[Ports]{database: database, options: options, bind: bind, now: time.Now, wait: waitForTransactionRetry, metrics: collector, afterCommitFailures: &afterCommitFailureStore{}}
}

func (u *SQLUnitOfWork[Ports]) TransactionMetricsSnapshot() mutation.TransactionMetricsSnapshot {
	if u == nil || u.metrics == nil {
		return mutation.TransactionMetricsSnapshot{}
	}
	return u.metrics.TransactionMetricsSnapshot()
}

func (u *SQLUnitOfWork[Ports]) WithinTransaction(ctx context.Context, operation transactioncontract.Operation[Ports]) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if transactioncontract.ActiveTransaction(ctx) {
		return ErrNestedUnitOfWork
	}
	if u == nil || u.database == nil {
		return ErrUnitOfWorkDatabaseRequired
	}
	if u.bind == nil {
		return ErrUnitOfWorkBinderRequired
	}
	if operation == nil {
		return nil
	}
	maxAttempts := u.options.Retry.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	if maxAttempts > 1 && u.options.Retry.SideEffects != transactioncontract.TransactionSideEffectsTransactionalOnly {
		return ErrUnitOfWorkRetryUnsafe
	}
	retryIdentity := transactioncontract.TransactionRetryIdentity{
		IdempotencyScope: u.options.Retry.IdempotencyScope,
		CorrelationID:    requestcontext.RequestID(ctx),
	}.Normalized()
	if maxAttempts > 1 && !retryIdentity.IsComplete() {
		return ErrUnitOfWorkRetryIdentity
	}
	if maxAttempts > 1 && !validTransactionRetryPolicy(u.options.Retry, u.now()) {
		return ErrUnitOfWorkRetryPolicy
	}
	if maxAttempts > 1 {
		ctx = transactioncontract.WithTransactionRetryIdentity(ctx, retryIdentity)
	}
	options, err := sqlUnitOfWorkOptions(u.options)
	if err != nil {
		return err
	}
	observation := mutation.TransactionObservation{
		WorkspaceID:   retryIdentity.IdempotencyScope.WorkspaceID,
		Scope:         retryIdentity.IdempotencyScope.UseCase,
		CorrelationID: retryIdentity.CorrelationID,
	}
	startedAt := u.now()
	defer func() {
		observation.Duration = u.now().Sub(startedAt)
		u.metrics.ObserveTransaction(observation)
	}()
	retryDelaySpent := time.Duration(0)
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if attempt > 1 && !u.now().Before(u.options.Retry.Deadline) {
			return ErrUnitOfWorkRetryDeadline
		}
		observation.Attempts++
		if attempt > 1 {
			observation.Retries++
		}
		err, rolledBack := u.withinTransactionAttempt(ctx, options, operation)
		if rolledBack {
			observation.Rollbacks++
		}
		if mutation.IsMutationConflict(err, "") || mutation.IsTransactionTransient(err, "") {
			observation.Conflicts++
		}
		if err == nil || attempt == maxAttempts || !mutation.IsTransactionTransient(err, "") {
			observation.Succeeded = err == nil
			return err
		}
		delay := transactionRetryBackoff(u.options.Retry, attempt)
		if retryDelaySpent+delay > u.options.Retry.RetryBudget {
			return errors.Join(err, ErrUnitOfWorkRetryBudget)
		}
		if !u.now().Add(delay).Before(u.options.Retry.Deadline) {
			return errors.Join(err, ErrUnitOfWorkRetryDeadline)
		}
		if waitErr := u.wait(ctx, delay); waitErr != nil {
			return errors.Join(err, waitErr)
		}
		retryDelaySpent += delay
	}
}

func validTransactionRetryPolicy(policy transactioncontract.TransactionRetryPolicy, now time.Time) bool {
	return policy.MaxAttempts >= 2 &&
		policy.MaxAttempts <= transactioncontract.MaxTransactionRetryAttempts &&
		policy.InitialBackoff > 0 &&
		policy.MaxBackoff >= policy.InitialBackoff &&
		policy.RetryBudget > 0 &&
		!policy.Deadline.IsZero() && policy.Deadline.After(now)
}

func transactionRetryBackoff(policy transactioncontract.TransactionRetryPolicy, failedAttempt int) time.Duration {
	delay := policy.InitialBackoff
	for step := 1; step < failedAttempt && delay < policy.MaxBackoff; step++ {
		if delay > policy.MaxBackoff/2 {
			return policy.MaxBackoff
		}
		delay *= 2
	}
	if delay > policy.MaxBackoff {
		return policy.MaxBackoff
	}
	return delay
}

func waitForTransactionRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (u *SQLUnitOfWork[Ports]) withinTransactionAttempt(ctx context.Context, options *sql.TxOptions, operation transactioncontract.Operation[Ports]) (error, bool) {
	tx, err := u.database.BeginTx(ctx, options)
	if err != nil {
		return err, false
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			_ = tx.Rollback()
			panic(recovered)
		}
	}()
	operationContext, afterCommit := transactioncontract.WithAfterCommitRegistry(transactioncontract.WithActiveTransaction(ctx))
	if err := operation(operationContext, u.bind(tx)); err != nil {
		return rollbackUnitOfWork(tx, err), true
	}
	if err := ctx.Err(); err != nil {
		return rollbackUnitOfWork(tx, err), true
	}
	if err := tx.Commit(); err != nil {
		_ = tx.Rollback()
		return err, true
	}
	u.runAfterCommitHooks(ctx, afterCommit.Hooks())
	return nil, false
}

func sqlUnitOfWorkOptions(options transactioncontract.Options) (*sql.TxOptions, error) {
	level := sql.LevelDefault
	switch options.Isolation {
	case "", transactioncontract.IsolationDefault:
	case transactioncontract.IsolationReadCommitted:
		level = sql.LevelReadCommitted
	case transactioncontract.IsolationRepeatableRead:
		level = sql.LevelRepeatableRead
	case transactioncontract.IsolationSerializable:
		level = sql.LevelSerializable
	default:
		return nil, ErrUnitOfWorkIsolationInvalid
	}
	return &sql.TxOptions{Isolation: level, ReadOnly: options.ReadOnly}, nil
}

func rollbackUnitOfWork(tx *sql.Tx, original error) error {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return errors.Join(original, err)
	}
	return original
}
