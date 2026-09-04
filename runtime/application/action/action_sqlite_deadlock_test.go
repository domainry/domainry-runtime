package action

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	actionruntime "github.com/domainry/domainry-runtime/runtime/domain/action/runtime"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

type sqliteSingleConnectionExecutionStore struct {
	*actionUnitOfWorkStoreProbe
	db *sql.DB
}

func (s *sqliteSingleConnectionExecutionStore) BeginExecutionTransaction(ctx context.Context) (actioncontract.ActionExecutionTransaction, error) {
	s.beginTransactionCalls++
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return &sqliteSingleConnectionExecutionTransaction{store: s, conn: conn}, nil
}

type sqliteSingleConnectionExecutionTransaction struct {
	store *sqliteSingleConnectionExecutionStore
	conn  *sql.Conn
}

func (t *sqliteSingleConnectionExecutionTransaction) Context(ctx context.Context) context.Context {
	return ctx
}

func (t *sqliteSingleConnectionExecutionTransaction) Commit(ctx context.Context, commits []transactionmodel.RecordMutationCommit, completion actionmodel.ActionExecutionCompletion) (actionmodel.ActionBusinessExecution, error) {
	if _, err := t.conn.ExecContext(ctx, "COMMIT"); err != nil {
		_ = t.conn.Close()
		return actionmodel.ActionBusinessExecution{}, err
	}
	_ = t.conn.Close()
	return t.store.commitTransaction(ctx, commits, completion)
}

func (t *sqliteSingleConnectionExecutionTransaction) RollBack(ctx context.Context) error {
	t.store.rollbackCalls++
	_, err := t.conn.ExecContext(ctx, "ROLLBACK")
	_ = t.conn.Close()
	return err
}

func TestCreatePlanningDoesNotSelfDeadlockSharedSingleConnectionSQLitePool(t *testing.T) {
	db, err := sql.Open("sqlite", "file:action-create-planning-deadlock?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("CREATE TABLE _identity_users (id TEXT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO _identity_users(id) VALUES ('identity-user-1')"); err != nil {
		t.Fatal(err)
	}

	store := &sqliteSingleConnectionExecutionStore{actionUnitOfWorkStoreProbe: &actionUnitOfWorkStoreProbe{}, db: db}
	unitOfWork := &actionUnitOfWork{
		manager: NewActionUnitOfWorkManager(actionruntime.NewActionExecutionRuntime(store)),
		claim: actionmodel.ActionExecutionClaimResult{Execution: actionmodel.ActionBusinessExecution{
			ID: "execution-1", LeaseOwner: "test-owner", FencingToken: 1,
		}},
		phases: newActionExecutionPhaseMachine(),
	}
	execution := &businessActionExecution{
		unitOfWork: unitOfWork,
		action: definitionmodel.ActionSchema{Key: "member.self_enroll", EffectSet: &definitionmodel.ActionEffectSet{
			Write: []definitionmodel.ActionObjectEffect{{ObjectKey: "member"}},
		}},
		dependencies: BusinessHandlerExecutionDependencies{
			PlanCreateMutation: func(ctx context.Context, _ string, _ map[string]any, _ string, _ principalmodel.Principal) (transactionmodel.MutationPlan, recordmodel.Record, error) {
				// Models Identity Projection relation validation using the same DB pool
				// but outside Runtime's Action transaction composition.
				var id string
				if err := db.QueryRowContext(ctx, "SELECT id FROM _identity_users WHERE id = ?", "identity-user-1").Scan(&id); err != nil {
					return transactionmodel.MutationPlan{}, recordmodel.Record{}, err
				}
				return transactionmodel.MutationPlan{}, recordmodel.Record{ID: "member-1", Data: map[string]any{"identity_user_id": id}}, nil
			},
		},
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if _, err := execution.ApplyRecordMutation(ctx, runtimeext.RecordMutation{
		Operation: runtimeext.MutationCreate, ObjectKey: "member", Fields: map[string]any{"identity_user_id": "identity-user-1"},
	}); err != nil {
		t.Fatalf("create planning through shared SQLite pool: %v", err)
	}
	if store.beginTransactionCalls != 0 {
		t.Fatalf("create planning opened physical transaction before external relation validation: %d", store.beginTransactionCalls)
	}
	if err := unitOfWork.commit(ctx, actionmodel.ActionInvocationResult{}, nil, nil); err != nil {
		t.Fatalf("atomic action commit: %v", err)
	}
	if store.beginTransactionCalls != 1 || len(store.commits) != 1 {
		t.Fatalf("transaction begins=%d commits=%d", store.beginTransactionCalls, len(store.commits))
	}
}
