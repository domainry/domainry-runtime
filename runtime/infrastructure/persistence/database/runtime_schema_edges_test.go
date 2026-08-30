package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	runtimeschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/schema"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func runtimeSchemaStore(t *testing.T, state *databaseSQLState) *RuntimeStore {
	t.Helper()
	db := openDatabaseScriptedDB(state)
	t.Cleanup(func() { _ = db.Close() })
	return &RuntimeStore{db: db, engine: sqlite.NewEngine()}
}

func runtimeSchemaLedgerQueries(count int64, checksum string, dirty bool) []databaseSQLQueryStep {
	steps := make([]databaseSQLQueryStep, 0, 11)
	for range 9 {
		steps = append(steps, databaseSQLQueryStep{})
	}
	steps = append(steps,
		databaseSQLQueryStep{columns: []string{"count"}, rows: [][]driver.Value{{count}}},
		databaseSQLQueryStep{columns: []string{"checksum", "dirty"}, rows: [][]driver.Value{{checksum, dirty}}},
	)
	return steps
}

func TestRuntimeSchemaHelpersAndDatabaseSelection(t *testing.T) {
	versions := SupportedRuntimeSchemaUpgradeVersions()
	if len(versions) != 12 || versions[0] != "001_connector_runtime_lifecycle" || versions[11] != "012_rate_limit_schema_owner" {
		t.Fatalf("versions=%#v", versions)
	}
	store := runtimeSchemaStore(t, &databaseSQLState{})
	if store.schemaDatabase() != store.db {
		t.Fatal("primary database not selected")
	}
	migrationDB := openDatabaseScriptedDB(&databaseSQLState{})
	t.Cleanup(func() { _ = migrationDB.Close() })
	store.migrationDB = migrationDB
	if store.schemaDatabase() != migrationDB {
		t.Fatal("migration database not selected")
	}
	connection, err := migrationDB.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	store.migrationConn = connection
	if store.schemaDatabase() != connection {
		t.Fatal("migration connection not selected")
	}

	store = &RuntimeStore{engine: sqlite.NewEngine(), config: config.Config{DBPath: filepath.Join("tmp", "runtime.db")}}
	if got := store.runtimeMigrationConfig().MigrationBackupDir; got != filepath.Join("tmp", "migration-backups") {
		t.Fatalf("backup dir=%q", got)
	}
	store.config = config.Config{DatabaseDSN: filepath.Join("var", "runtime.db")}
	if got := store.runtimeMigrationConfig().MigrationBackupDir; got != filepath.Join("var", "migration-backups") {
		t.Fatalf("dsn backup dir=%q", got)
	}
	store.config.MigrationBackupDir = "custom"
	if got := store.runtimeMigrationConfig().MigrationBackupDir; got != "custom" {
		t.Fatalf("custom backup dir=%q", got)
	}
	store.config = config.Config{DBPath: ":memory:"}
	if got := store.runtimeMigrationConfig().MigrationBackupDir; got != "" {
		t.Fatalf("memory backup dir=%q", got)
	}
	store = &RuntimeStore{engine: mysql.NewEngine()}
	if got := store.runtimeMigrationConfig().MigrationBackupDir; got != "" {
		t.Fatalf("mysql backup dir=%q", got)
	}
}

func TestVerifyRuntimeSchemaStates(t *testing.T) {
	checksum := currentRuntimeSchemaChecksum()
	tests := []struct {
		name  string
		step  databaseSQLQueryStep
		match string
	}{
		{"query", databaseSQLQueryStep{err: errDatabaseSQL}, "verify runtime schema"},
		{"dirty", databaseSQLQueryStep{columns: []string{"checksum", "dirty"}, rows: [][]driver.Value{{checksum, true}}}, "migration.dirty"},
		{"drift", databaseSQLQueryStep{columns: []string{"checksum", "dirty"}, rows: [][]driver.Value{{"drift", false}}}, "checksum_drift"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := runtimeSchemaStore(t, &databaseSQLState{querySteps: []databaseSQLQueryStep{test.step}})
			if err := store.verifyRuntimeSchema(t.Context()); err == nil || !strings.Contains(err.Error(), test.match) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	store := runtimeSchemaStore(t, &databaseSQLState{querySteps: []databaseSQLQueryStep{{columns: []string{"checksum", "dirty"}, rows: [][]driver.Value{{checksum, false}}}}})
	if err := store.verifyRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeSchemaMigrationLedgerFailures(t *testing.T) {
	store := runtimeSchemaStore(t, &databaseSQLState{execSteps: []databaseSQLExecStep{{err: errDatabaseSQL}}})
	if _, err := store.runtimeSchemaMigrationPending(t.Context(), "version"); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("create error=%v", err)
	}
	store = runtimeSchemaStore(t, &databaseSQLState{execSteps: []databaseSQLExecStep{{rows: 1}, {err: errDatabaseSQL}}, querySteps: []databaseSQLQueryStep{{err: errDatabaseSQL}}})
	if _, err := store.runtimeSchemaMigrationPending(t.Context(), "version"); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("alter error=%v", err)
	}
	queries := make([]databaseSQLQueryStep, 10)
	queries[9].err = errDatabaseSQL
	store = runtimeSchemaStore(t, &databaseSQLState{querySteps: queries})
	if _, err := store.runtimeSchemaMigrationPending(t.Context(), "version"); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("count error=%v", err)
	}
	store = runtimeSchemaStore(t, &databaseSQLState{querySteps: runtimeSchemaLedgerQueries(0, "", false)})
	if pending, err := store.runtimeSchemaMigrationPending(t.Context(), "version"); err != nil || !pending {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
	queries = make([]databaseSQLQueryStep, 10)
	queries[0] = databaseSQLQueryStep{err: errDatabaseSQL}
	queries[9] = databaseSQLQueryStep{columns: []string{"count"}, rows: [][]driver.Value{{int64(0)}}}
	store = runtimeSchemaStore(t, &databaseSQLState{querySteps: queries})
	if pending, err := store.runtimeSchemaMigrationPending(t.Context(), "version"); err != nil || !pending {
		t.Fatalf("alter success pending=%v err=%v", pending, err)
	}
	for _, test := range []struct {
		name     string
		checksum string
		dirty    bool
		want     string
	}{
		{"dirty", currentRuntimeSchemaChecksum(), true, "migration.dirty"},
		{"drift", "drift", false, "checksum_drift"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := runtimeSchemaStore(t, &databaseSQLState{querySteps: runtimeSchemaLedgerQueries(1, test.checksum, test.dirty)})
			if _, err := store.runtimeSchemaMigrationPending(t.Context(), "version"); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v", err)
			}
		})
	}
	store = runtimeSchemaStore(t, &databaseSQLState{querySteps: runtimeSchemaLedgerQueries(1, "", false), execSteps: []databaseSQLExecStep{{rows: 1}, {err: errDatabaseSQL}}})
	if _, err := store.runtimeSchemaMigrationPending(t.Context(), "version"); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("backfill error=%v", err)
	}
}

func TestRuntimeSchemaMutationFailuresAndDefinitions(t *testing.T) {
	store := runtimeSchemaStore(t, &databaseSQLState{execSteps: []databaseSQLExecStep{{err: errDatabaseSQL}}})
	if err := store.startRuntimeSchemaMigration(t.Context(), "version"); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("start=%v", err)
	}
	store = runtimeSchemaStore(t, &databaseSQLState{execSteps: []databaseSQLExecStep{{err: errDatabaseSQL}}})
	if err := store.recordRuntimeSchemaMigration(t.Context(), "version", time.Second); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("record=%v", err)
	}
	store = runtimeSchemaStore(t, &databaseSQLState{querySteps: []databaseSQLQueryStep{{closeErr: errDatabaseSQL}}})
	if err := store.ensureRuntimeColumn(t.Context(), "table", "column", "TEXT"); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("close=%v", err)
	}
	store = runtimeSchemaStore(t, &databaseSQLState{querySteps: []databaseSQLQueryStep{{err: errDatabaseSQL}}, execSteps: []databaseSQLExecStep{{err: errDatabaseSQL}}})
	if err := store.ensureRuntimeColumn(t.Context(), "table", "column", "TEXT"); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("alter=%v", err)
	}
	store = runtimeSchemaStore(t, &databaseSQLState{querySteps: []databaseSQLQueryStep{{err: errDatabaseSQL}}})
	if err := store.ensureRuntimeColumn(t.Context(), "table", "column", "TEXT"); err != nil {
		t.Fatal(err)
	}
	mysqlStore := &RuntimeStore{engine: mysql.NewEngine()}
	definition := "TEXT NOT NULL DEFAULT '[]', TEXT NOT NULL DEFAULT '{}', TEXT NOT NULL DEFAULT ''"
	got := mysqlStore.runtimeColumnDefinition(definition)
	for _, expected := range []string{"DEFAULT ('[]')", "DEFAULT ('{}')", "DEFAULT ('')"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("definition=%q", got)
		}
	}
	if got := (&RuntimeStore{engine: sqlite.NewEngine()}).runtimeColumnDefinition(definition); got != definition {
		t.Fatalf("sqlite definition=%q", got)
	}
}

type runtimeSchemaAssemblerStub struct{ fail string }

func (stub runtimeSchemaAssemblerStub) result(stage string) error {
	if stub.fail == stage {
		return errDatabaseSQL
	}
	return nil
}
func (stub runtimeSchemaAssemblerStub) EnsureApplicationSchema(context.Context, runtimeschema.Store) error {
	return stub.result("metadata")
}
func (stub runtimeSchemaAssemblerStub) EnsureEvidenceSchema(context.Context, runtimeschema.Store) error {
	return stub.result("evidence")
}
func (stub runtimeSchemaAssemblerStub) EnsureWorkflowProcessSchema(context.Context, runtimeschema.Store) error {
	return stub.result("workflow")
}
func (stub runtimeSchemaAssemblerStub) EnsureAgentSchema(context.Context, runtimeschema.Store) error {
	return stub.result("agent")
}
func (stub runtimeSchemaAssemblerStub) EnsureRateLimitSchema(context.Context, runtimeschema.Store) error {
	return stub.result("ratelimit")
}
func (stub runtimeSchemaAssemblerStub) EnsureLifecycleSchema(context.Context, runtimeschema.Store) error {
	return stub.result("lifecycle")
}

func TestEnsureRuntimeSchemaAssemblerFailures(t *testing.T) {
	for _, stage := range []string{"metadata", "evidence", "workflow", "agent", "ratelimit", "lifecycle"} {
		t.Run(stage, func(t *testing.T) {
			state := &databaseSQLState{querySteps: runtimeSchemaLedgerQueries(1, currentRuntimeSchemaChecksum(), false)}
			store := runtimeSchemaStore(t, state)
			store.schemaAssembler = runtimeSchemaAssemblerStub{fail: stage}
			if err := store.EnsureRuntimeSchema(t.Context()); !errors.Is(err, errDatabaseSQL) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestEnsureRuntimeSchemaOrchestrationFailures(t *testing.T) {
	verify := runtimeSchemaStore(t, &databaseSQLState{querySteps: []databaseSQLQueryStep{{err: errDatabaseSQL}}})
	verify.config.DatabaseMigrationMode = "verify"
	if err := verify.EnsureRuntimeSchema(t.Context()); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("verify error=%v", err)
	}

	primary := runtimeSchemaStore(t, &databaseSQLState{})
	closedMigration := openDatabaseScriptedDB(&databaseSQLState{})
	_ = closedMigration.Close()
	primary.migrationDB = closedMigration
	if err := primary.EnsureRuntimeSchema(t.Context()); err == nil {
		t.Fatal("closed migration database was accepted")
	}

	locked := runtimeSchemaStore(t, &databaseSQLState{})
	locked.config.DBPath = filepath.Join("/dev/null", "runtime.db")
	if err := locked.EnsureRuntimeSchema(t.Context()); err == nil {
		t.Fatal("invalid migration lock path was accepted")
	}

	pendingFailure := runtimeSchemaStore(t, &databaseSQLState{execSteps: []databaseSQLExecStep{{err: errDatabaseSQL}}})
	if err := pendingFailure.EnsureRuntimeSchema(t.Context()); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("pending error=%v", err)
	}

	pendingLedgerQueries := runtimeSchemaLedgerQueries(0, "", false)[:10]
	validationQueries := append(append([]databaseSQLQueryStep{}, pendingLedgerQueries...), databaseSQLQueryStep{err: errDatabaseSQL})
	validation := runtimeSchemaStore(t, &databaseSQLState{querySteps: validationQueries})
	if err := validation.EnsureRuntimeSchema(t.Context()); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("validation error=%v", err)
	}

	backupQueries := append(append([]databaseSQLQueryStep{}, pendingLedgerQueries...), databaseSQLQueryStep{}, databaseSQLQueryStep{err: errDatabaseSQL})
	backup := runtimeSchemaStore(t, &databaseSQLState{querySteps: backupQueries})
	if err := backup.EnsureRuntimeSchema(t.Context()); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("backup error=%v", err)
	}

	recordQueries := append(append([]databaseSQLQueryStep{}, pendingLedgerQueries...), databaseSQLQueryStep{}, databaseSQLQueryStep{})
	record := runtimeSchemaStore(t, &databaseSQLState{querySteps: recordQueries, execSteps: []databaseSQLExecStep{{rows: 1}, {rows: 1}, {err: errDatabaseSQL}}})
	record.schemaAssembler = runtimeSchemaAssemblerStub{}
	if err := record.EnsureRuntimeSchema(t.Context()); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("record error=%v", err)
	}

	startQueries := append(append([]databaseSQLQueryStep{}, pendingLedgerQueries...), databaseSQLQueryStep{}, databaseSQLQueryStep{})
	start := runtimeSchemaStore(t, &databaseSQLState{querySteps: startQueries, execSteps: []databaseSQLExecStep{{rows: 1}, {err: errDatabaseSQL}}})
	if err := start.EnsureRuntimeSchema(t.Context()); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("start error=%v", err)
	}
}

func TestRuntimeSchemaPendingRecordAndActionExecutionContextEdges(t *testing.T) {
	store := runtimeSchemaStore(t, &databaseSQLState{execSteps: []databaseSQLExecStep{{err: errDatabaseSQL}}})
	if err := store.recordRuntimeSchemaMigrationIfPending(t.Context(), false, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.recordRuntimeSchemaMigrationIfPending(t.Context(), true, time.Now()); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("record error=%v", err)
	}
	success := runtimeSchemaStore(t, &databaseSQLState{execSteps: []databaseSQLExecStep{{rows: 1}}})
	if err := success.recordRuntimeSchemaMigrationIfPending(t.Context(), true, time.Now()); err != nil {
		t.Fatalf("successful record error=%v", err)
	}
	if WithActionExecutionTransaction(nil, store.DB()) != nil {
		t.Fatal("nil context changed")
	}
	if got := WithActionExecutionTransaction(t.Context(), nil); got != t.Context() {
		t.Fatal("nil executor changed context")
	}
	if ActionExecutionTransaction(nil) != nil {
		t.Fatal("nil context returned executor")
	}
	ctx := WithActionExecutionTransaction(t.Context(), store.DB())
	if ActionExecutionTransaction(ctx) != store.DB() {
		t.Fatal("transaction executor was not preserved")
	}
	if ActionExecutionTransaction(t.Context()) != nil {
		t.Fatal("plain context returned executor")
	}
}

func TestRuntimeSchemaMigrationChecksumQueryFailure(t *testing.T) {
	queries := runtimeSchemaLedgerQueries(1, currentRuntimeSchemaChecksum(), false)
	queries[10] = databaseSQLQueryStep{err: errDatabaseSQL}
	store := runtimeSchemaStore(t, &databaseSQLState{querySteps: queries})
	if _, err := store.runtimeSchemaMigrationPending(t.Context(), "version"); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("checksum query error=%v", err)
	}
}

func TestSchemaAssemblerSeamMethods(t *testing.T) {
	store := &RuntimeStore{schemaAssembler: runtimeSchemaAssemblerStub{}}
	if err := store.EnsureApplicationSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureEvidenceSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureWorkflowProcessSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureAgentSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRateLimitSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureLifecycleSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyAuditPrimaryKeyReplacementSQLIsDialectSpecific(t *testing.T) {
	tests := map[string]string{
		"postgres": `ALTER TABLE "runtime"."_audit_events" DROP CONSTRAINT "audit_events_pkey", ADD PRIMARY KEY ("workspace_id", "id")`,
		"mysql":    "ALTER TABLE `runtime`.`_audit_events` DROP PRIMARY KEY, ADD PRIMARY KEY (`workspace_id`, `id`)",
	}
	for driver, expected := range tests {
		t.Run(driver, func(t *testing.T) {
			table, workspace, id, constraint := `"runtime"."_audit_events"`, `"workspace_id"`, `"id"`, `"audit_events_pkey"`
			if driver == "mysql" {
				table, workspace, id, constraint = "`runtime`.`_audit_events`", "`workspace_id`", "`id`", ""
			}
			actual, err := legacyAuditPrimaryKeyReplacementSQL(driver, table, workspace, id, constraint)
			if err != nil || actual != expected {
				t.Fatalf("statement=%q err=%v", actual, err)
			}
		})
	}
	if _, err := legacyAuditPrimaryKeyReplacementSQL("postgres", "audit", "workspace", "id", ""); err == nil {
		t.Fatal("empty Postgres constraint was accepted")
	}
	if _, err := legacyAuditPrimaryKeyReplacementSQL("sqlite", "audit", "workspace", "id", ""); err == nil {
		t.Fatal("SQLite rebuild was incorrectly represented as one ALTER statement")
	}
}

var _ schemaDatabase = (*sql.DB)(nil)
