package transaction

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	transactioncontract "github.com/domainry/domainry-runtime/runtime/domain/transaction/contract"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/mutation"
	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
	_ "modernc.org/sqlite"
)

type testTransactionPorts interface {
	transactioncontract.PortSet
	Insert(context.Context, string) error
	InsertDurableWork(context.Context, string) error
	InsertDeferredViolation(context.Context) error
}

type testTransactionPort struct {
	tx *sql.Tx
}

type deadlineAfterWriteContext struct {
	context.Context
	expired atomic.Bool
}

func (c *deadlineAfterWriteContext) Err() error {
	if c.expired.Load() {
		return context.DeadlineExceeded
	}
	return c.Context.Err()
}

func (p testTransactionPort) Insert(ctx context.Context, value string) error {
	_, err := p.tx.ExecContext(ctx, `INSERT INTO unit_of_work_probe (value) VALUES (?)`, value)
	return err
}

func (p testTransactionPort) InsertDeferredViolation(ctx context.Context) error {
	_, err := p.tx.ExecContext(ctx, `INSERT INTO unit_of_work_child (id, parent_id) VALUES (1, 999)`)
	return err
}

func (p testTransactionPort) InsertDurableWork(ctx context.Context, id string) error {
	_, err := p.tx.ExecContext(ctx, `INSERT INTO unit_of_work_durable_work (id, status) VALUES (?, 'queued')`, id)
	return err
}

func TestSQLUnitOfWorkCommitsOwnerPorts(t *testing.T) {
	database := openUnitOfWorkDatabase(t)
	unit := newTestSQLUnitOfWork(database)
	var _ transactioncontract.UnitOfWork[testTransactionPorts] = unit
	if transactioncontract.ActiveTransaction(t.Context()) {
		t.Fatal("caller context was marked as transactional")
	}

	if err := unit.WithinTransaction(t.Context(), func(ctx context.Context, ports testTransactionPorts) error {
		if !transactioncontract.ActiveTransaction(ctx) {
			t.Fatal("unit of work operation context is missing the active transaction boundary")
		}
		return ports.Insert(ctx, "committed")
	}); err != nil {
		t.Fatal(err)
	}
	if count := unitOfWorkRowCount(t, database); count != 1 {
		t.Fatalf("row count = %d", count)
	}
}

func TestSQLUnitOfWorkRollsBackCallbackError(t *testing.T) {
	database := openUnitOfWorkDatabase(t)
	unit := newTestSQLUnitOfWork(database)
	want := errors.New("operation failed")
	err := unit.WithinTransaction(t.Context(), func(ctx context.Context, ports testTransactionPorts) error {
		if err := ports.Insert(ctx, "rolled-back"); err != nil {
			return err
		}
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v", err)
	}
	if count := unitOfWorkRowCount(t, database); count != 0 {
		t.Fatalf("row count = %d", count)
	}
}

func TestSQLUnitOfWorkPreservesCallbackErrorClassification(t *testing.T) {
	database := openUnitOfWorkDatabase(t)
	unit := newTestSQLUnitOfWork(database)
	want := &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.transaction.probe_conflict"}
	err := unit.WithinTransaction(t.Context(), func(ctx context.Context, ports testTransactionPorts) error {
		if err := ports.Insert(ctx, "classified-error"); err != nil {
			return err
		}
		return want
	})
	if !errors.Is(err, want) || apperror.CodeOf(err) != want.Code {
		t.Fatalf("error classification lost: %v", err)
	}
	if count := unitOfWorkRowCount(t, database); count != 0 {
		t.Fatalf("row count = %d", count)
	}
}

func TestSQLUnitOfWorkRollsBackAndRepanics(t *testing.T) {
	database := openUnitOfWorkDatabase(t)
	unit := newTestSQLUnitOfWork(database)
	const panicValue = "transaction panic"
	func() {
		defer func() {
			if recovered := recover(); recovered != panicValue {
				t.Fatalf("recovered = %#v", recovered)
			}
		}()
		_ = unit.WithinTransaction(t.Context(), func(ctx context.Context, ports testTransactionPorts) error {
			if err := ports.Insert(ctx, "panic"); err != nil {
				return err
			}
			panic(panicValue)
		})
	}()
	if count := unitOfWorkRowCount(t, database); count != 0 {
		t.Fatalf("row count = %d", count)
	}
}

func TestSQLUnitOfWorkRollsBackCommitError(t *testing.T) {
	database := openUnitOfWorkDatabase(t)
	unit := newTestSQLUnitOfWork(database)
	err := unit.WithinTransaction(t.Context(), func(ctx context.Context, ports testTransactionPorts) error {
		if err := ports.InsertDurableWork(ctx, "outbox-before-commit-failure"); err != nil {
			return err
		}
		return ports.InsertDeferredViolation(ctx)
	})
	if err == nil {
		t.Fatal("expected deferred foreign-key commit failure")
	}
	var count int
	if scanErr := database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM unit_of_work_child`).Scan(&count); scanErr != nil {
		t.Fatal(scanErr)
	}
	if count != 0 {
		t.Fatalf("child row count = %d", count)
	}
	if err := database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM unit_of_work_durable_work WHERE status = 'queued'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("commit failure left executable durable work: count=%d", count)
	}
}

func TestSQLUnitOfWorkPropagatesCancellationBeforeBegin(t *testing.T) {
	database := openUnitOfWorkDatabase(t)
	unit := newTestSQLUnitOfWork(database)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := unit.WithinTransaction(ctx, func(context.Context, testTransactionPorts) error {
		called = true
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if called {
		t.Fatal("callback ran after cancellation")
	}
}

func TestSQLUnitOfWorkRollsBackCancellationBeforeCommit(t *testing.T) {
	database := openUnitOfWorkDatabase(t)
	unit := newTestSQLUnitOfWork(database)
	ctx, cancel := context.WithCancel(context.Background())
	err := unit.WithinTransaction(ctx, func(operationContext context.Context, ports testTransactionPorts) error {
		if err := ports.Insert(operationContext, "cancelled"); err != nil {
			return err
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if count := unitOfWorkRowCount(t, database); count != 0 {
		t.Fatalf("row count = %d", count)
	}
}

func TestSQLUnitOfWorkRollsBackDeadlineExceededAfterWriteBeforeCommit(t *testing.T) {
	database := openUnitOfWorkDatabase(t)
	unit := newTestSQLUnitOfWork(database)
	ctx := &deadlineAfterWriteContext{Context: t.Context()}
	err := unit.WithinTransaction(ctx, func(operationContext context.Context, ports testTransactionPorts) error {
		if err := ports.Insert(operationContext, "deadline-after-write"); err != nil {
			return err
		}
		ctx.expired.Store(true)
		return nil
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
	if count := unitOfWorkRowCount(t, database); count != 0 {
		t.Fatalf("deadline-exceeded transaction committed %d rows", count)
	}
}

func TestSQLUnitOfWorkRejectsUnknownIsolation(t *testing.T) {
	database := openUnitOfWorkDatabase(t)
	unit := NewSQLUnitOfWork[testTransactionPorts](database, transactioncontract.Options{Isolation: "unknown"}, func(tx *sql.Tx) testTransactionPorts {
		return testTransactionPort{tx: tx}
	})
	err := unit.WithinTransaction(t.Context(), func(context.Context, testTransactionPorts) error { return nil })
	if !errors.Is(err, ErrUnitOfWorkIsolationInvalid) {
		t.Fatalf("error = %v", err)
	}
}

func TestSQLUnitOfWorkRejectsNestedTransaction(t *testing.T) {
	database := openUnitOfWorkDatabase(t)
	outer := newTestSQLUnitOfWork(database)
	inner := newTestSQLUnitOfWork(database)
	err := outer.WithinTransaction(t.Context(), func(ctx context.Context, ports testTransactionPorts) error {
		if err := ports.Insert(ctx, "outer"); err != nil {
			return err
		}
		return inner.WithinTransaction(ctx, func(innerContext context.Context, innerPorts testTransactionPorts) error {
			return innerPorts.Insert(innerContext, "inner")
		})
	})
	if !errors.Is(err, ErrNestedUnitOfWork) {
		t.Fatalf("error = %v", err)
	}
	if count := unitOfWorkRowCount(t, database); count != 0 {
		t.Fatalf("row count = %d", count)
	}
}

func TestSQLUnitOfWorkRetriesTheCompleteTransactionOnlyWhenDeclaredSafe(t *testing.T) {
	database := openUnitOfWorkDatabase(t)
	unit := NewSQLUnitOfWork[testTransactionPorts](database, transactioncontract.Options{
		Isolation: transactioncontract.IsolationSerializable,
		Retry:     testTransactionRetryPolicy(2),
	}, func(tx *sql.Tx) testTransactionPorts {
		return testTransactionPort{tx: tx}
	})
	attempts := 0
	identities := make([]transactioncontract.TransactionRetryIdentity, 0, 2)
	ctx := requestcontext.WithRequestID(t.Context(), "correlation-1")
	err := unit.WithinTransaction(ctx, func(ctx context.Context, ports testTransactionPorts) error {
		attempts++
		identity, ok := transactioncontract.TransactionRetryIdentityFromContext(ctx)
		if !ok {
			return errors.New("retry identity missing")
		}
		identities = append(identities, identity)
		if err := ports.Insert(ctx, "attempt"); err != nil {
			return err
		}
		if attempts == 1 {
			return mutation.TransactionTransient("unit_of_work_probe", "attempt", mutation.TransactionTransientSerializationFailure, errors.New("SQLSTATE 40001"))
		}
		return nil
	})
	if err != nil || attempts != 2 {
		t.Fatalf("retry result: attempts=%d err=%v", attempts, err)
	}
	if len(identities) != 2 || identities[0] != identities[1] || identities[0].IdempotencyScope != testTransactionRetryScope() || identities[0].CorrelationID != "correlation-1" {
		t.Fatalf("retry identity changed across attempts: %#v", identities)
	}
	if count := unitOfWorkRowCount(t, database); count != 1 {
		t.Fatalf("complete first transaction was not rolled back before retry: row count=%d", count)
	}
}

func TestSQLUnitOfWorkRejectsRetryWhenSideEffectsMayEscape(t *testing.T) {
	database := openUnitOfWorkDatabase(t)
	unit := NewSQLUnitOfWork[testTransactionPorts](database, transactioncontract.Options{
		Isolation: transactioncontract.IsolationSerializable,
		Retry: transactioncontract.TransactionRetryPolicy{
			MaxAttempts: 2,
			SideEffects: transactioncontract.TransactionSideEffectsMayEscape,
		},
	}, func(tx *sql.Tx) testTransactionPorts {
		return testTransactionPort{tx: tx}
	})
	called := false
	err := unit.WithinTransaction(t.Context(), func(context.Context, testTransactionPorts) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrUnitOfWorkRetryUnsafe) || called {
		t.Fatalf("unsafe retry must fail before operation: called=%v err=%v", called, err)
	}
}

func TestSQLUnitOfWorkDoesNotRetryNonTransientFailure(t *testing.T) {
	database := openUnitOfWorkDatabase(t)
	unit := NewSQLUnitOfWork[testTransactionPorts](database, transactioncontract.Options{
		Isolation: transactioncontract.IsolationSerializable,
		Retry:     testTransactionRetryPolicy(2),
	}, func(tx *sql.Tx) testTransactionPorts {
		return testTransactionPort{tx: tx}
	})
	attempts := 0
	want := errors.New("business validation failed")
	err := unit.WithinTransaction(requestcontext.WithRequestID(t.Context(), "correlation-1"), func(context.Context, testTransactionPorts) error {
		attempts++
		return want
	})
	if !errors.Is(err, want) || attempts != 1 {
		t.Fatalf("non-transient failure retried: attempts=%d err=%v", attempts, err)
	}
}

func TestSQLUnitOfWorkEnforcesAttemptsBackoffDeadlineAndBudget(t *testing.T) {
	database := openUnitOfWorkDatabase(t)
	base := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	policy := testTransactionRetryPolicy(3)
	policy.InitialBackoff = 10 * time.Millisecond
	policy.MaxBackoff = 20 * time.Millisecond
	policy.Deadline = base.Add(time.Second)
	policy.RetryBudget = 30 * time.Millisecond
	unit := NewSQLUnitOfWork[testTransactionPorts](database, transactioncontract.Options{Isolation: transactioncontract.IsolationSerializable, Retry: policy}, func(tx *sql.Tx) testTransactionPorts {
		return testTransactionPort{tx: tx}
	})
	now := base
	var waits []time.Duration
	unit.now = func() time.Time { return now }
	unit.wait = func(_ context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		now = now.Add(delay)
		return nil
	}
	attempts := 0
	err := unit.WithinTransaction(requestcontext.WithRequestID(t.Context(), "correlation-1"), func(context.Context, testTransactionPorts) error {
		attempts++
		return mutation.TransactionTransient("probe", "attempt", mutation.TransactionTransientDeadlock, nil)
	})
	if !mutation.IsTransactionTransient(err, mutation.TransactionTransientDeadlock) || attempts != 3 || !reflect.DeepEqual(waits, []time.Duration{10 * time.Millisecond, 20 * time.Millisecond}) {
		t.Fatalf("bounded retry: attempts=%d waits=%v err=%v", attempts, waits, err)
	}

	budgetPolicy := policy
	budgetPolicy.RetryBudget = 15 * time.Millisecond
	budgetUnit := NewSQLUnitOfWork[testTransactionPorts](database, transactioncontract.Options{Isolation: transactioncontract.IsolationSerializable, Retry: budgetPolicy}, func(tx *sql.Tx) testTransactionPorts {
		return testTransactionPort{tx: tx}
	})
	now = base
	budgetUnit.now = func() time.Time { return now }
	budgetUnit.wait = func(_ context.Context, delay time.Duration) error { now = now.Add(delay); return nil }
	attempts = 0
	err = budgetUnit.WithinTransaction(requestcontext.WithRequestID(t.Context(), "correlation-1"), func(context.Context, testTransactionPorts) error {
		attempts++
		return mutation.TransactionTransient("probe", "attempt", mutation.TransactionTransientLockTimeout, nil)
	})
	if !errors.Is(err, ErrUnitOfWorkRetryBudget) || attempts != 2 {
		t.Fatalf("retry budget: attempts=%d err=%v", attempts, err)
	}

	deadlinePolicy := policy
	deadlinePolicy.Deadline = base.Add(15 * time.Millisecond)
	deadlineUnit := NewSQLUnitOfWork[testTransactionPorts](database, transactioncontract.Options{Isolation: transactioncontract.IsolationSerializable, Retry: deadlinePolicy}, func(tx *sql.Tx) testTransactionPorts {
		return testTransactionPort{tx: tx}
	})
	now = base
	deadlineUnit.now = func() time.Time { return now }
	deadlineUnit.wait = func(_ context.Context, delay time.Duration) error { now = now.Add(delay); return nil }
	attempts = 0
	err = deadlineUnit.WithinTransaction(requestcontext.WithRequestID(t.Context(), "correlation-1"), func(context.Context, testTransactionPorts) error {
		attempts++
		return mutation.TransactionTransient("probe", "attempt", mutation.TransactionTransientSerializationFailure, nil)
	})
	if !errors.Is(err, ErrUnitOfWorkRetryDeadline) || attempts != 2 {
		t.Fatalf("retry deadline: attempts=%d err=%v", attempts, err)
	}
}

func TestSQLUnitOfWorkRecordsDurationConflictsRollbacksAndRetries(t *testing.T) {
	database := openUnitOfWorkDatabase(t)
	base := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	policy := testTransactionRetryPolicy(2)
	policy.Deadline = base.Add(time.Second)
	collector := mutation.NewMemoryTransactionMetricsCollector()
	unit := NewSQLUnitOfWork[testTransactionPorts](database, transactioncontract.Options{Isolation: transactioncontract.IsolationSerializable, Retry: policy}, func(tx *sql.Tx) testTransactionPorts {
		return testTransactionPort{tx: tx}
	}, collector)
	now := base
	unit.now = func() time.Time { return now }
	unit.wait = func(_ context.Context, delay time.Duration) error { now = now.Add(delay); return nil }
	attempts := 0
	err := unit.WithinTransaction(requestcontext.WithRequestID(t.Context(), "correlation-1"), func(ctx context.Context, ports testTransactionPorts) error {
		attempts++
		now = now.Add(5 * time.Millisecond)
		if err := ports.Insert(ctx, "metric"); err != nil {
			return err
		}
		if attempts == 1 {
			return mutation.TransactionTransient("probe", "metric", mutation.TransactionTransientDeadlock, nil)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := collector.TransactionMetricsSnapshot()
	if snapshot.Transactions != 1 || snapshot.Succeeded != 1 || snapshot.TotalDuration != 11*time.Millisecond || snapshot.Attempts != 2 || snapshot.Conflicts != 1 || snapshot.Rollbacks != 1 || snapshot.Retries != 1 {
		t.Fatalf("transaction metrics=%#v", snapshot)
	}
	if unit.TransactionMetricsSnapshot() != snapshot {
		t.Fatalf("unit metrics snapshot mismatch: %#v", unit.TransactionMetricsSnapshot())
	}
	err = unit.WithinTransaction(requestcontext.WithRequestID(t.Context(), "correlation-2"), func(ctx context.Context, ports testTransactionPorts) error {
		now = now.Add(2 * time.Millisecond)
		if err := ports.Insert(ctx, "conflict"); err != nil {
			return err
		}
		return mutation.MutationConflict("unit_of_work_probe", "conflict", mutation.MutationConflictUnique, nil)
	})
	if !mutation.IsMutationConflict(err, mutation.MutationConflictUnique) {
		t.Fatalf("expected unique conflict, got %v", err)
	}
	snapshot = collector.TransactionMetricsSnapshot()
	if snapshot.Transactions != 2 || snapshot.Succeeded != 1 || snapshot.Failed != 1 || snapshot.TotalDuration != 13*time.Millisecond || snapshot.Attempts != 3 || snapshot.Conflicts != 2 || snapshot.Rollbacks != 2 || snapshot.Retries != 1 {
		t.Fatalf("success/failure transaction metrics=%#v", snapshot)
	}
}

func testTransactionRetryScope() transactioncontract.TransactionIdempotencyScope {
	return transactioncontract.TransactionIdempotencyScope{WorkspaceID: "workspace-a", UseCase: "transaction.probe", ResourceType: "probe", TargetID: "attempt", Key: "caller-key"}
}

func testTransactionRetryPolicy(maxAttempts int) transactioncontract.TransactionRetryPolicy {
	return transactioncontract.TransactionRetryPolicy{
		MaxAttempts:      maxAttempts,
		SideEffects:      transactioncontract.TransactionSideEffectsTransactionalOnly,
		IdempotencyScope: testTransactionRetryScope(),
		InitialBackoff:   time.Millisecond,
		MaxBackoff:       time.Millisecond,
		Deadline:         time.Now().Add(time.Minute),
		RetryBudget:      time.Second,
	}
}

func newTestSQLUnitOfWork(database *sql.DB) *SQLUnitOfWork[testTransactionPorts] {
	return NewSQLUnitOfWork[testTransactionPorts](database, transactioncontract.Options{Isolation: transactioncontract.IsolationSerializable}, func(tx *sql.Tx) testTransactionPorts {
		return testTransactionPort{tx: tx}
	})
}

func openUnitOfWorkDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.ExecContext(t.Context(), `PRAGMA foreign_keys = ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(t.Context(), `CREATE TABLE unit_of_work_probe (value TEXT NOT NULL UNIQUE)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(t.Context(), `CREATE TABLE unit_of_work_parent (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(t.Context(), `CREATE TABLE unit_of_work_child (id INTEGER PRIMARY KEY, parent_id INTEGER NOT NULL, FOREIGN KEY (parent_id) REFERENCES unit_of_work_parent(id) DEFERRABLE INITIALLY DEFERRED)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(t.Context(), `CREATE TABLE unit_of_work_durable_work (id TEXT PRIMARY KEY, status TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	return database
}

func unitOfWorkRowCount(t *testing.T, database *sql.DB) int {
	t.Helper()
	var count int
	if err := database.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM unit_of_work_probe`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
