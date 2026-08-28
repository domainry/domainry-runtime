package transaction

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/mutation"
	"github.com/domainry/domainry-foundation/requestcontext"
	transactioncontract "github.com/domainry/domainry-runtime/runtime/domain/transaction/contract"
)

func TestSQLUnitOfWorkDependencyAndNilBoundaries(t *testing.T) {
	var nilUnit *SQLUnitOfWork[testTransactionPorts]
	if snapshot := nilUnit.TransactionMetricsSnapshot(); snapshot != (mutation.TransactionMetricsSnapshot{}) {
		t.Fatalf("nil metrics snapshot=%#v", snapshot)
	}
	if failures := nilUnit.AfterCommitFailures(); failures != nil {
		t.Fatalf("nil after-commit failures=%#v", failures)
	}
	if err := nilUnit.WithinTransaction(t.Context(), func(context.Context, testTransactionPorts) error { return nil }); !errors.Is(err, ErrUnitOfWorkDatabaseRequired) {
		t.Fatalf("nil unit error=%v", err)
	}
	emptyUnit := &SQLUnitOfWork[testTransactionPorts]{}
	if err := emptyUnit.WithinTransaction(t.Context(), func(context.Context, testTransactionPorts) error { return nil }); !errors.Is(err, ErrUnitOfWorkDatabaseRequired) {
		t.Fatalf("nil database error=%v", err)
	}

	database := openUnitOfWorkDatabase(t)
	unit := NewSQLUnitOfWork[testTransactionPorts](database, transactioncontract.Options{}, func(tx *sql.Tx) testTransactionPorts { return testTransactionPort{tx: tx} }, nil)
	unit.metrics = nil
	unit.afterCommitFailures = nil
	if snapshot := unit.TransactionMetricsSnapshot(); snapshot != (mutation.TransactionMetricsSnapshot{}) || unit.AfterCommitFailures() != nil {
		t.Fatalf("empty optional dependencies snapshot=%#v failures=%#v", snapshot, unit.AfterCommitFailures())
	}
	unit.metrics = mutation.NewMemoryTransactionMetricsCollector()
	if err := unit.WithinTransaction(t.Context(), nil); err != nil {
		t.Fatalf("nil operation=%v", err)
	}
	unit.bind = nil
	if err := unit.WithinTransaction(t.Context(), func(context.Context, testTransactionPorts) error { return nil }); !errors.Is(err, ErrUnitOfWorkBinderRequired) {
		t.Fatalf("nil binder error=%v", err)
	}

	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	closedUnit := NewSQLUnitOfWork[testTransactionPorts](database, transactioncontract.Options{}, func(tx *sql.Tx) testTransactionPorts { return testTransactionPort{tx: tx} })
	if err := closedUnit.WithinTransaction(t.Context(), func(context.Context, testTransactionPorts) error { return nil }); err == nil {
		t.Fatal("closed database begin succeeded")
	}
}

func TestSQLUnitOfWorkRetryDeadlineAndBackoffConditions(t *testing.T) {
	database := openUnitOfWorkDatabase(t)
	base := time.Date(2026, 7, 20, 17, 0, 0, 0, time.UTC)
	policy := testTransactionRetryPolicy(2)
	policy.Deadline = base.Add(time.Second)
	unit := NewSQLUnitOfWork[testTransactionPorts](database, transactioncontract.Options{Isolation: transactioncontract.IsolationSerializable, Retry: policy}, func(tx *sql.Tx) testTransactionPorts { return testTransactionPort{tx: tx} })
	now := base
	unit.now = func() time.Time { return now }
	unit.wait = func(context.Context, time.Duration) error {
		now = policy.Deadline
		return nil
	}
	err := unit.WithinTransaction(requestcontext.WithRequestID(t.Context(), "correlation-1"), func(context.Context, testTransactionPorts) error {
		return mutation.TransactionTransient("probe", "id", mutation.TransactionTransientDeadlock, nil)
	})
	if !errors.Is(err, ErrUnitOfWorkRetryDeadline) {
		t.Fatalf("retry deadline error=%v", err)
	}

	newRetryUnit := func() *SQLUnitOfWork[testTransactionPorts] {
		unit := NewSQLUnitOfWork[testTransactionPorts](database, transactioncontract.Options{Isolation: transactioncontract.IsolationSerializable, Retry: policy}, func(tx *sql.Tx) testTransactionPorts { return testTransactionPort{tx: tx} })
		unit.now = func() time.Time { return base }
		return unit
	}
	ctx, cancel := context.WithCancel(requestcontext.WithRequestID(context.Background(), "correlation-1"))
	cancelUnit := newRetryUnit()
	cancelUnit.wait = func(context.Context, time.Duration) error { cancel(); return nil }
	err = cancelUnit.WithinTransaction(ctx, func(context.Context, testTransactionPorts) error {
		return mutation.TransactionTransient("probe", "id", mutation.TransactionTransientDeadlock, nil)
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("retry loop cancellation=%v", err)
	}
	waitErr := errors.New("retry wait failed")
	waitUnit := newRetryUnit()
	waitUnit.wait = func(context.Context, time.Duration) error { return waitErr }
	err = waitUnit.WithinTransaction(requestcontext.WithRequestID(t.Context(), "correlation-1"), func(context.Context, testTransactionPorts) error {
		return mutation.TransactionTransient("probe", "id", mutation.TransactionTransientDeadlock, nil)
	})
	if !errors.Is(err, waitErr) {
		t.Fatalf("retry wait error=%v", err)
	}

	for _, test := range []struct {
		policy  transactioncontract.TransactionRetryPolicy
		attempt int
		want    time.Duration
	}{
		{policy: transactioncontract.TransactionRetryPolicy{InitialBackoff: 8, MaxBackoff: 10}, attempt: 2, want: 10},
		{policy: transactioncontract.TransactionRetryPolicy{InitialBackoff: 10, MaxBackoff: 10}, attempt: 2, want: 10},
		{policy: transactioncontract.TransactionRetryPolicy{InitialBackoff: 6, MaxBackoff: 10}, attempt: 1, want: 6},
		{policy: transactioncontract.TransactionRetryPolicy{InitialBackoff: 12, MaxBackoff: 10}, attempt: 1, want: 10},
	} {
		if got := transactionRetryBackoff(test.policy, test.attempt); got != test.want {
			t.Fatalf("backoff=%v want=%v policy=%+v attempt=%d", got, test.want, test.policy, test.attempt)
		}
	}
}

func TestRollbackUnitOfWorkIgnoresAlreadyFinishedTransaction(t *testing.T) {
	database := openUnitOfWorkDatabase(t)
	tx, err := database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	want := errors.New("original")
	if got := rollbackUnitOfWork(tx, want); !errors.Is(got, want) {
		t.Fatalf("rollback result=%v", got)
	}
	rollbackErr := errors.New("rollback failed")
	failureDB := sql.OpenDB(rollbackFailureConnector{err: rollbackErr})
	defer failureDB.Close()
	failureTx, err := failureDB.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := rollbackUnitOfWork(failureTx, want); !errors.Is(got, want) || !errors.Is(got, rollbackErr) {
		t.Fatalf("joined rollback result=%v", got)
	}
}

func TestTransactionWaitAndIsolationVariants(t *testing.T) {
	if err := waitForTransactionRetry(t.Context(), time.Millisecond); err != nil {
		t.Fatalf("timer retry wait=%v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForTransactionRetry(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled retry wait=%v", err)
	}
	for _, test := range []struct {
		isolation transactioncontract.Isolation
		want      sql.IsolationLevel
	}{
		{isolation: transactioncontract.IsolationReadCommitted, want: sql.LevelReadCommitted},
		{isolation: transactioncontract.IsolationRepeatableRead, want: sql.LevelRepeatableRead},
	} {
		options, err := sqlUnitOfWorkOptions(transactioncontract.Options{Isolation: test.isolation})
		if err != nil || options.Isolation != test.want {
			t.Fatalf("isolation=%q options=%+v err=%v", test.isolation, options, err)
		}
	}
}

type rollbackFailureConnector struct{ err error }

func (c rollbackFailureConnector) Connect(context.Context) (driver.Conn, error) {
	return rollbackFailureConn{err: c.err}, nil
}
func (rollbackFailureConnector) Driver() driver.Driver { return rollbackFailureDriver{} }

type rollbackFailureDriver struct{}

func (rollbackFailureDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("use connector")
}

type rollbackFailureConn struct{ err error }

func (rollbackFailureConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (rollbackFailureConn) Close() error                        { return nil }
func (c rollbackFailureConn) Begin() (driver.Tx, error)         { return rollbackFailureTx{err: c.err}, nil }

type rollbackFailureTx struct{ err error }

func (rollbackFailureTx) Commit() error      { return nil }
func (tx rollbackFailureTx) Rollback() error { return tx.err }
