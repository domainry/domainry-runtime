package transaction

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	transactioncontract "github.com/domainry/domainry-runtime/runtime/domain/transaction/contract"
	"github.com/domainry/domainry-runtime/runtime/platform/mutation"
	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
)

func TestSQLUnitOfWorkRunsOnlyCommittedRecoverableAfterCommitHooks(t *testing.T) {
	database := openUnitOfWorkDatabase(t)
	unit := newTestSQLUnitOfWork(database)
	visibleAfterCommit := false
	wantHookError := errors.New("local wakeup unavailable")
	err := unit.WithinTransaction(requestcontext.WithRequestID(t.Context(), "correlation-hook"), func(ctx context.Context, ports testTransactionPorts) error {
		if err := ports.Insert(ctx, "committed-hook"); err != nil {
			return err
		}
		if err := transactioncontract.RegisterAfterCommit(ctx, transactioncontract.AfterCommitHook{
			Name: "wake durable worker", Purpose: transactioncontract.AfterCommitDurableWorkWakeup, DurableRecovery: true,
			Run: func(context.Context) error {
				visibleAfterCommit = unitOfWorkRowCount(t, database) == 1
				return nil
			},
		}); err != nil {
			return err
		}
		return transactioncontract.RegisterAfterCommit(ctx, transactioncontract.AfterCommitHook{
			Name: "invalidate local cache", Purpose: transactioncontract.AfterCommitCacheInvalidation, DurableRecovery: true,
			Run: func(context.Context) error { return wantHookError },
		})
	})
	if err != nil || !visibleAfterCommit || unitOfWorkRowCount(t, database) != 1 {
		t.Fatalf("committed hook semantics: visible=%v err=%v", visibleAfterCommit, err)
	}
	failures := unit.AfterCommitFailures()
	if len(failures) != 1 || failures[0].Name != "invalidate local cache" || failures[0].CorrelationID != "correlation-hook" || !errors.Is(failures[0].Cause, wantHookError) {
		t.Fatalf("after-commit failures=%#v", failures)
	}
}

func TestSQLUnitOfWorkDiscardsRolledBackAttemptHooksAndRejectsUnrecoverableHooks(t *testing.T) {
	database := openUnitOfWorkDatabase(t)
	policy := testTransactionRetryPolicy(2)
	unit := NewSQLUnitOfWork[testTransactionPorts](database, transactioncontract.Options{Isolation: transactioncontract.IsolationSerializable, Retry: policy}, func(tx *sql.Tx) testTransactionPorts {
		return testTransactionPort{tx: tx}
	})
	runs, attempts := 0, 0
	err := unit.WithinTransaction(requestcontext.WithRequestID(t.Context(), "correlation-hook"), func(ctx context.Context, _ testTransactionPorts) error {
		attempts++
		attempt := attempts
		if err := transactioncontract.RegisterAfterCommit(ctx, transactioncontract.AfterCommitHook{
			Name: "refresh projection", Purpose: transactioncontract.AfterCommitLocalProjectionRefresh, DurableRecovery: true,
			Run: func(context.Context) error { runs += attempt; return nil },
		}); err != nil {
			return err
		}
		if attempts == 1 {
			return mutation.TransactionTransient("probe", "hook", mutation.TransactionTransientDeadlock, nil)
		}
		return nil
	})
	if err != nil || attempts != 2 || runs != 2 {
		t.Fatalf("rolled-back hook was not discarded: attempts=%d runs=%d err=%v", attempts, runs, err)
	}

	unsafe := newTestSQLUnitOfWork(database)
	err = unsafe.WithinTransaction(t.Context(), func(ctx context.Context, ports testTransactionPorts) error {
		if err := ports.Insert(ctx, "unsafe-hook"); err != nil {
			return err
		}
		return transactioncontract.RegisterAfterCommit(ctx, transactioncontract.AfterCommitHook{
			Name: "external delivery", Purpose: transactioncontract.AfterCommitDurableWorkWakeup, DurableRecovery: false,
			Run: func(context.Context) error { return nil },
		})
	})
	if !errors.Is(err, transactioncontract.ErrAfterCommitRecoveryRequired) || unitOfWorkRowCount(t, database) != 0 {
		t.Fatalf("unrecoverable hook must roll back registration attempt: rows=%d err=%v", unitOfWorkRowCount(t, database), err)
	}
}

func TestCriticalTransactionChainsCoverEveryFailureWindow(t *testing.T) {
	criticalChains := []string{
		"record_mutation",
		"workflow_task_decision",
		"action_execution",
		"scheduler_run_state",
		"integration_event_acceptance",
		"metadata_publication",
		"cross_boundary_durable_intent",
	}
	for _, chain := range criticalChains {
		t.Run(chain, func(t *testing.T) {
			t.Run("first_write", func(t *testing.T) {
				database := openUnitOfWorkDatabase(t)
				unit := newTestSQLUnitOfWork(database)
				if _, err := database.ExecContext(t.Context(), `INSERT INTO unit_of_work_probe (value) VALUES ('collision')`); err != nil {
					t.Fatal(err)
				}
				err := unit.WithinTransaction(t.Context(), func(ctx context.Context, ports testTransactionPorts) error {
					return ports.Insert(ctx, "collision")
				})
				if err == nil || unitOfWorkRowCount(t, database) != 1 {
					t.Fatalf("first-write failure leaked %s facts: rows=%d err=%v", chain, unitOfWorkRowCount(t, database), err)
				}
			})

			t.Run("middle_write", func(t *testing.T) {
				database := openUnitOfWorkDatabase(t)
				unit := newTestSQLUnitOfWork(database)
				if _, err := database.ExecContext(t.Context(), `INSERT INTO unit_of_work_probe (value) VALUES ('collision')`); err != nil {
					t.Fatal(err)
				}
				err := unit.WithinTransaction(t.Context(), func(ctx context.Context, ports testTransactionPorts) error {
					if err := ports.Insert(ctx, chain+":first"); err != nil {
						return err
					}
					return ports.Insert(ctx, "collision")
				})
				if err == nil || unitOfWorkRowCount(t, database) != 1 {
					t.Fatalf("middle-write failure partially committed %s: rows=%d err=%v", chain, unitOfWorkRowCount(t, database), err)
				}
			})

			t.Run("before_commit", func(t *testing.T) {
				database := openUnitOfWorkDatabase(t)
				unit := newTestSQLUnitOfWork(database)
				injected := errors.New("injected before commit")
				err := unit.WithinTransaction(t.Context(), func(ctx context.Context, ports testTransactionPorts) error {
					if err := ports.Insert(ctx, chain+":first"); err != nil {
						return err
					}
					if err := ports.Insert(ctx, chain+":middle"); err != nil {
						return err
					}
					return injected
				})
				if !errors.Is(err, injected) || unitOfWorkRowCount(t, database) != 0 {
					t.Fatalf("pre-commit failure partially committed %s: rows=%d err=%v", chain, unitOfWorkRowCount(t, database), err)
				}
			})

			t.Run("after_commit", func(t *testing.T) {
				database := openUnitOfWorkDatabase(t)
				unit := newTestSQLUnitOfWork(database)
				hookFailure := errors.New("injected after commit")
				err := unit.WithinTransaction(requestcontext.WithRequestID(t.Context(), "failure-window:"+chain), func(ctx context.Context, ports testTransactionPorts) error {
					if err := ports.Insert(ctx, chain+":first"); err != nil {
						return err
					}
					if err := ports.Insert(ctx, chain+":middle"); err != nil {
						return err
					}
					return transactioncontract.RegisterAfterCommit(ctx, transactioncontract.AfterCommitHook{
						Name: "failure-window", Purpose: transactioncontract.AfterCommitDurableWorkWakeup, DurableRecovery: true,
						Run: func(context.Context) error { return hookFailure },
					})
				})
				failures := unit.AfterCommitFailures()
				if err != nil || unitOfWorkRowCount(t, database) != 2 || len(failures) != 1 || !errors.Is(failures[0].Cause, hookFailure) {
					t.Fatalf("post-commit semantics invalid for %s: rows=%d failures=%#v err=%v", chain, unitOfWorkRowCount(t, database), failures, err)
				}
			})
		})
	}
}

func TestSQLUnitOfWorkRejectsRetryWithoutStableIdentity(t *testing.T) {
	database := openUnitOfWorkDatabase(t)
	unit := NewSQLUnitOfWork[testTransactionPorts](database, transactioncontract.Options{
		Isolation: transactioncontract.IsolationSerializable,
		Retry: transactioncontract.TransactionRetryPolicy{
			MaxAttempts: 2,
			SideEffects: transactioncontract.TransactionSideEffectsTransactionalOnly,
		},
	}, func(tx *sql.Tx) testTransactionPorts {
		return testTransactionPort{tx: tx}
	})
	called := false
	err := unit.WithinTransaction(t.Context(), func(context.Context, testTransactionPorts) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrUnitOfWorkRetryIdentity) || called {
		t.Fatalf("retry without stable identity must fail before operation: called=%v err=%v", called, err)
	}
}

func TestSQLUnitOfWorkRejectsRetryPolicyAboveHardAttemptLimit(t *testing.T) {
	database := openUnitOfWorkDatabase(t)
	policy := testTransactionRetryPolicy(transactioncontract.MaxTransactionRetryAttempts + 1)
	unit := NewSQLUnitOfWork[testTransactionPorts](database, transactioncontract.Options{Isolation: transactioncontract.IsolationSerializable, Retry: policy}, func(tx *sql.Tx) testTransactionPorts {
		return testTransactionPort{tx: tx}
	})
	called := false
	err := unit.WithinTransaction(requestcontext.WithRequestID(t.Context(), "correlation-1"), func(context.Context, testTransactionPorts) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrUnitOfWorkRetryPolicy) || called {
		t.Fatalf("retry above hard attempt limit must fail before operation: called=%v err=%v", called, err)
	}
}
