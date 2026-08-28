package operations

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/datamigration"
)

func executableRetirementPlan(t *testing.T) (*DatabaseRetirementSQLExecutor, operationsmodel.DatabaseRetirement, operationsmodel.DatabaseDropPlan) {
	t.Helper()
	store := openDatabaseRetirementExecutorStore(t)
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE legacy_sql (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	retirement := executableDatabaseRetirement(now, operationsmodel.DatabaseObjectIdentity{Engine: "sqlite", Database: "runtime", Kind: "table", Name: "legacy_sql"})
	executor := NewDatabaseRetirementSQLExecutor(store, func() time.Time { return now }, func() string { return "execution" })
	plan, err := executor.PreviewDatabaseRetirement(t.Context(), retirement)
	if err != nil {
		t.Fatal(err)
	}
	return &executor, retirement, plan
}

func withExecutorSQL(t *testing.T, base DatabaseRetirementSQLExecutor, state *operationsSQLState) DatabaseRetirementSQLExecutor {
	t.Helper()
	base.db = openOperationsScriptedDB(state)
	base.inspect = func(context.Context, *sql.DB, datamigration.Engine, string) (datamigration.Inventory, error) {
		return datamigration.Inventory{Engine: datamigration.EngineSQLite}, nil
	}
	t.Cleanup(func() { _ = base.db.Close() })
	return base
}

func TestDatabaseRetirementTransitionSQLStages(t *testing.T) {
	base, retirement, _ := executableRetirementPlan(t)
	current := retirement
	current.State = operationsmodel.DatabaseRetirementReadsSwitched
	next := current
	next.State = operationsmodel.DatabaseRetirementWritesDisabled

	invalid := next
	invalid.Object.Kind = "view"
	if _, err := base.ApplyDatabaseRetirementTransition(t.Context(), current, invalid); err == nil {
		t.Fatal("invalid write-protection object accepted")
	}
	ordinary := next
	ordinary.State = operationsmodel.DatabaseRetirementState("unknown")
	if applied, err := base.ApplyDatabaseRetirementTransition(t.Context(), current, ordinary); err != nil || applied.State != ordinary.State {
		t.Fatalf("ordinary transition=%+v err=%v", applied, err)
	}
	badRestoreCurrent := current
	badRestoreCurrent.State = operationsmodel.DatabaseRetirementQuarantined
	badRestoreNext := badRestoreCurrent
	badRestoreNext.State = operationsmodel.DatabaseRetirementObservationComplete
	if _, err := base.ApplyDatabaseRetirementTransition(t.Context(), badRestoreCurrent, badRestoreNext); err == nil {
		t.Fatal("restore without quarantine name accepted")
	}
	for _, test := range []struct {
		name  string
		state operationsSQLState
	}{
		{name: "begin", state: operationsSQLState{beginErr: errOperationsSQL}},
		{name: "lock", state: operationsSQLState{execSteps: []operationsSQLExecStep{{err: errOperationsSQL}}}},
		{name: "statement", state: operationsSQLState{execSteps: []operationsSQLExecStep{{rows: 1}, {err: errOperationsSQL}}}},
		{name: "commit", state: operationsSQLState{execSteps: []operationsSQLExecStep{{rows: 1}, {rows: 1}, {rows: 1}, {rows: 1}}, commitErr: errOperationsSQL}},
	} {
		t.Run(test.name, func(t *testing.T) {
			executor := withExecutorSQL(t, *base, &test.state)
			if _, err := executor.ApplyDatabaseRetirementTransition(t.Context(), current, next); !errors.Is(err, errOperationsSQL) {
				t.Fatalf("error=%v", err)
			}
		})
	}

	quarantine := current
	quarantine.State = operationsmodel.DatabaseRetirementQuarantined
	executor := withExecutorSQL(t, *base, &operationsSQLState{})
	if applied, err := executor.ApplyDatabaseRetirementTransition(t.Context(), current, quarantine); err != nil || applied.Evidence.QuarantineObjectName == "" {
		t.Fatalf("applied=%+v err=%v", applied, err)
	}
}

func TestDatabaseRetirementExecutionSQLStages(t *testing.T) {
	base, retirement, plan := executableRetirementPlan(t)
	if _, err := base.ExecuteDatabaseRetirement(t.Context(), retirement, operationsmodel.DatabaseDropPlan{}); err == nil {
		t.Fatal("invalid plan accepted")
	}
	invalidEvidence := retirement
	invalidEvidence.Evidence.ApprovalID = ""
	if _, err := base.ExecuteDatabaseRetirement(t.Context(), invalidEvidence, plan); err == nil {
		t.Fatal("invalid evidence accepted")
	}
	mismatch := plan
	mismatch.RetirementID = "other"
	if _, err := base.ExecuteDatabaseRetirement(t.Context(), retirement, mismatch); err == nil {
		t.Fatal("identity mismatch accepted")
	}
	mismatch = plan
	mismatch.Object.Name = "other"
	if _, err := base.ExecuteDatabaseRetirement(t.Context(), retirement, mismatch); err == nil {
		t.Fatal("plan object mismatch accepted")
	}
	invalidEngine := retirement
	invalidEngine.Object.Engine = "oracle"
	invalidPlan := plan
	invalidPlan.Object = invalidEngine.Object
	if _, err := base.ExecuteDatabaseRetirement(t.Context(), invalidEngine, invalidPlan); err == nil {
		t.Fatal("invalid engine accepted")
	}

	inspectFailure := *base
	inspectFailure.inspect = func(context.Context, *sql.DB, datamigration.Engine, string) (datamigration.Inventory, error) {
		return datamigration.Inventory{}, errOperationsSQL
	}
	if _, err := inspectFailure.ExecuteDatabaseRetirement(t.Context(), retirement, plan); !errors.Is(err, errOperationsSQL) {
		t.Fatalf("inspect error=%v", err)
	}
	if _, err := inspectFailure.PreviewDatabaseRetirement(t.Context(), retirement); !errors.Is(err, errOperationsSQL) {
		t.Fatalf("preview inspect error=%v", err)
	}
	defaultDependencies := *base
	defaultDependencies.inspect = func(context.Context, *sql.DB, datamigration.Engine, string) (datamigration.Inventory, error) {
		return datamigration.Inventory{Engine: datamigration.EngineSQLite, Tables: []datamigration.TableInventory{{Name: retirement.Object.Name}}}, nil
	}
	if _, err := defaultDependencies.PreviewDatabaseRetirement(t.Context(), retirement); err != nil {
		t.Fatalf("injected preview rejected: %v", err)
	}
	unsafe := retirement
	unsafe.Object.Name = "unsafe;drop"
	unsafe.ID = "retire-unsafe"
	unsafePreview := *base
	unsafePreview.inspect = func(context.Context, *sql.DB, datamigration.Engine, string) (datamigration.Inventory, error) {
		return datamigration.Inventory{Engine: datamigration.EngineSQLite, Tables: []datamigration.TableInventory{{Name: unsafe.Object.Name}}}, nil
	}
	if _, err := unsafePreview.PreviewDatabaseRetirement(t.Context(), unsafe); err == nil {
		t.Fatal("unsafe drop identifier accepted")
	}

	for _, test := range []struct {
		name  string
		state operationsSQLState
	}{
		{name: "begin", state: operationsSQLState{beginErr: errOperationsSQL}},
		{name: "lock", state: operationsSQLState{execSteps: []operationsSQLExecStep{{err: errOperationsSQL}}}},
		{name: "statement", state: operationsSQLState{execSteps: []operationsSQLExecStep{{rows: 1}, {err: errOperationsSQL}}}},
		{name: "commit", state: operationsSQLState{execSteps: []operationsSQLExecStep{{rows: 1}, {rows: 1}}, commitErr: errOperationsSQL}},
	} {
		t.Run(test.name, func(t *testing.T) {
			executor := withExecutorSQL(t, *base, &test.state)
			result, err := executor.ExecuteDatabaseRetirement(t.Context(), retirement, plan)
			if !errors.Is(err, errOperationsSQL) || (test.name != "begin" && result.BlockedReason == "") {
				t.Fatalf("result=%+v err=%v", result, err)
			}
		})
	}

	mysqlRetirement, mysqlPlan := retirement, plan
	mysqlRetirement.Object.Engine = "mysql"
	mysqlPlan.Object = mysqlRetirement.Object
	mysqlPlan.Statements = []string{"DROP TABLE one", "DROP TABLE two"}
	state := operationsSQLState{execSteps: []operationsSQLExecStep{{rows: 1}, {rows: 1}, {err: errOperationsSQL}}}
	executor := withExecutorSQL(t, *base, &state)
	result, err := executor.ExecuteDatabaseRetirement(t.Context(), mysqlRetirement, mysqlPlan)
	if !errors.Is(err, errOperationsSQL) || !result.Dirty || result.ExecutedStatements != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	mysqlPlan.Statements = []string{"DROP TABLE one"}
	state = operationsSQLState{}
	executor = withExecutorSQL(t, *base, &state)
	if result, err := executor.ExecuteDatabaseRetirement(t.Context(), mysqlRetirement, mysqlPlan); err != nil || result.Dirty {
		t.Fatalf("successful MySQL result=%+v err=%v", result, err)
	}
	columnRetirement, columnPlan := retirement, plan
	columnRetirement.Object = operationsmodel.DatabaseObjectIdentity{Engine: "sqlite", Database: "runtime", Kind: "column", ParentName: "records", Name: "obsolete"}
	columnPlan.Object = columnRetirement.Object
	columnPlan.Statements = []string{`ALTER TABLE "records" DROP COLUMN "obsolete"`}
	state = operationsSQLState{execSteps: []operationsSQLExecStep{{rows: 1}, {rows: 1}}}
	executor = withExecutorSQL(t, *base, &state)
	originalInspect := inspectSQLiteRetirementTransaction
	inspectSQLiteRetirementTransaction = func(context.Context, *sql.Tx) (datamigration.Inventory, error) {
		return datamigration.Inventory{}, errOperationsSQL
	}
	_, err = executor.ExecuteDatabaseRetirement(t.Context(), columnRetirement, columnPlan)
	inspectSQLiteRetirementTransaction = originalInspect
	if !errors.Is(err, errOperationsSQL) {
		t.Fatalf("SQLite preservation error=%v", err)
	}
	defaultTime := *base
	defaultTime.now = nil
	defaultTime.inspect = func(context.Context, *sql.DB, datamigration.Engine, string) (datamigration.Inventory, error) {
		return datamigration.Inventory{}, nil
	}
	if _, err := defaultTime.ExecuteDatabaseRetirement(t.Context(), retirement, plan); err != nil {
		t.Fatalf("default time execution rejected: %v", err)
	}
	zero := DatabaseRetirementSQLExecutor{}
	if zero.database() != nil {
		t.Fatal("zero executor returned database")
	}
	constructed := NewDatabaseRetirementSQLExecutor(base.store, nil, nil)
	if constructed.now().IsZero() {
		t.Fatal("default clock returned zero")
	}
}

func TestRetirementLockTimeoutEveryEngine(t *testing.T) {
	for _, test := range []struct {
		engine  datamigration.Engine
		timeout time.Duration
	}{
		{engine: datamigration.EngineSQLite, timeout: time.Second},
		{engine: datamigration.EnginePostgres, timeout: time.Second},
		{engine: datamigration.EngineMySQL, timeout: time.Millisecond},
	} {
		state := operationsSQLState{}
		db := openOperationsScriptedDB(&state)
		tx, err := db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := applyRetirementLockTimeout(t.Context(), tx, test.engine, test.timeout); err != nil {
			t.Fatal(err)
		}
		_ = tx.Rollback()
		_ = db.Close()
	}
	state := operationsSQLState{}
	db := openOperationsScriptedDB(&state)
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyRetirementLockTimeout(t.Context(), tx, datamigration.Engine("oracle"), time.Second); err == nil {
		t.Fatal("unsupported engine accepted")
	}
	_ = tx.Rollback()
	_ = db.Close()
}
