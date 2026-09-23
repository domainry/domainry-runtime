package operations

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/requestcontext"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/datamigration"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/testsupport/auditmodulefixture"
)

func TestSQLiteDatabaseRetirementExecutorDropsOnlyTypedApprovedObject(t *testing.T) {
	store := openDatabaseRetirementExecutorStore(t)
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE old_table (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 16, 0, 0, 0, time.UTC)
	retirement := executableDatabaseRetirement(now, operationsmodel.DatabaseObjectIdentity{Engine: "sqlite", Database: "runtime", Kind: "table", Name: "old_table"})
	executor := NewDatabaseRetirementSQLExecutor(store, func() time.Time { return now }, func() string { return "execution-1" })
	plan, err := executor.PreviewDatabaseRetirement(t.Context(), retirement)
	if err != nil {
		t.Fatal(err)
	}
	ctx := requestcontext.WithOwnerExecutionID(t.Context(), "operation-1")
	result, err := executor.ExecuteDatabaseRetirement(ctx, retirement, plan)
	if err != nil || result.Dirty || result.ExecutedStatements != 1 || result.AuditEventID == "" {
		t.Fatalf("execution result=%+v err=%v", result, err)
	}
	var count int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'old_table'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("retired table still exists: count=%d err=%v", count, err)
	}
	var event, recordID, operationID, ownerRunID string
	if err := store.DB().QueryRowContext(t.Context(), "SELECT "+store.Identifier("event")+", "+store.Identifier("record_id")+", "+store.Identifier("operation_id")+", "+store.Identifier("owner_run_id")+" FROM "+store.TableIdentifier("_audit_events")+" WHERE "+store.Identifier("id")+" = "+store.Placeholder(1), result.AuditEventID).Scan(&event, &recordID, &operationID, &ownerRunID); err != nil {
		t.Fatalf("load immutable retirement audit event: %v", err)
	}
	if event != "database_retirement_completed" || recordID != retirement.ID || operationID != "operation-1" || ownerRunID != retirement.ID {
		t.Fatalf("retirement audit event=%q record=%q operation=%q owner_run=%q", event, recordID, operationID, ownerRunID)
	}
	if err := executor.VerifyDatabaseRetirementCompletionAudit(t.Context(), retirement, result.AuditEventID); err != nil {
		t.Fatalf("verify immutable retirement Audit event: %v", err)
	}
	if err := executor.VerifyDatabaseRetirementCompletionAudit(t.Context(), retirement, "missing-audit"); err == nil {
		t.Fatal("missing retirement Audit event was accepted")
	}
	tampered := retirement
	tampered.Evidence.Owner = "other-owner"
	if err := executor.VerifyDatabaseRetirementCompletionAudit(t.Context(), tampered, result.AuditEventID); err == nil {
		t.Fatal("mismatched retirement Audit event was accepted")
	}
}

func TestSQLiteColumnRetirementPreservesUnaffectedIndexAndTrigger(t *testing.T) {
	store := openDatabaseRetirementExecutorStore(t)
	statements := []string{
		`CREATE TABLE old_shape (id TEXT PRIMARY KEY, obsolete TEXT, retained TEXT)`,
		`CREATE INDEX idx_old_shape_retained ON old_shape(retained)`,
		`CREATE TRIGGER trg_old_shape_retained AFTER UPDATE OF retained ON old_shape BEGIN UPDATE old_shape SET retained = NEW.retained WHERE id = NEW.id; END`,
	}
	for _, statement := range statements {
		if _, err := store.DB().ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 7, 19, 16, 0, 0, 0, time.UTC)
	retirement := executableDatabaseRetirement(now, operationsmodel.DatabaseObjectIdentity{Engine: "sqlite", Database: "runtime", Kind: "column", Name: "obsolete", ParentName: "old_shape"})
	executor := NewDatabaseRetirementSQLExecutor(store, func() time.Time { return now }, func() string { return "execution-2" })
	plan, err := executor.PreviewDatabaseRetirement(t.Context(), retirement)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.ExecuteDatabaseRetirement(t.Context(), retirement, plan); err != nil {
		t.Fatal(err)
	}
	for kind, name := range map[string]string{"index": "idx_old_shape_retained", "trigger": "trg_old_shape_retained"} {
		var count int
		if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type = ? AND name = ?`, kind, name).Scan(&count); err != nil || count != 1 {
			t.Fatalf("unaffected %s %s not preserved: count=%d err=%v", kind, name, count, err)
		}
	}
}

func TestDatabaseRetirementPreviewBlocksUnretiredDependencies(t *testing.T) {
	store := openDatabaseRetirementExecutorStore(t)
	if _, err := store.DB().ExecContext(t.Context(), `PRAGMA foreign_keys = ON; CREATE TABLE old_parent (id TEXT PRIMARY KEY); CREATE TABLE active_child (id TEXT PRIMARY KEY, parent_id TEXT REFERENCES old_parent(id))`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 16, 0, 0, 0, time.UTC)
	retirement := executableDatabaseRetirement(now, operationsmodel.DatabaseObjectIdentity{Engine: "sqlite", Database: "runtime", Kind: "table", Name: "old_parent"})
	executor := NewDatabaseRetirementSQLExecutor(store, func() time.Time { return now }, nil)
	if _, err := executor.PreviewDatabaseRetirement(t.Context(), retirement); err == nil {
		t.Fatal("table with live foreign-key dependency received DROP plan")
	}
}

func TestSQLiteDatabaseRetirementEnforcesIndexColumnAndForeignKeyOrder(t *testing.T) {
	store := openDatabaseRetirementExecutorStore(t)
	if _, err := store.DB().ExecContext(t.Context(), `PRAGMA foreign_keys = ON;
		CREATE TABLE ordered_parent (id TEXT PRIMARY KEY, obsolete TEXT, retained TEXT);
		CREATE INDEX idx_ordered_parent_obsolete ON ordered_parent(obsolete);
		CREATE TABLE ordered_child (id TEXT PRIMARY KEY, parent_id TEXT REFERENCES ordered_parent(id))`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 16, 30, 0, 0, time.UTC)
	executor := NewDatabaseRetirementSQLExecutor(store, func() time.Time { return now }, nil)
	column := executableDatabaseRetirement(now, operationsmodel.DatabaseObjectIdentity{Engine: "sqlite", Database: "runtime", Kind: "column", Name: "obsolete", ParentName: "ordered_parent"})
	if _, err := executor.PreviewDatabaseRetirement(t.Context(), column); err == nil || !strings.Contains(err.Error(), "idx_ordered_parent_obsolete") {
		t.Fatalf("column preview did not require dependent index first: %v", err)
	}
	index := executableDatabaseRetirement(now, operationsmodel.DatabaseObjectIdentity{Engine: "sqlite", Database: "runtime", Kind: "index", Name: "idx_ordered_parent_obsolete", ParentName: "ordered_parent"})
	dropRetirementObject(t, executor, index)
	dropRetirementObject(t, executor, column)
	parent := executableDatabaseRetirement(now, operationsmodel.DatabaseObjectIdentity{Engine: "sqlite", Database: "runtime", Kind: "table", Name: "ordered_parent"})
	if _, err := executor.PreviewDatabaseRetirement(t.Context(), parent); err == nil || !strings.Contains(err.Error(), "ordered_child") {
		t.Fatalf("parent preview did not require foreign-key child first: %v", err)
	}
	child := executableDatabaseRetirement(now, operationsmodel.DatabaseObjectIdentity{Engine: "sqlite", Database: "runtime", Kind: "table", Name: "ordered_child"})
	dropRetirementObject(t, executor, child)
	dropRetirementObject(t, executor, parent)
}

func dropRetirementObject(t *testing.T, executor DatabaseRetirementSQLExecutor, retirement operationsmodel.DatabaseRetirement) {
	t.Helper()
	plan, err := executor.PreviewDatabaseRetirement(t.Context(), retirement)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.ExecuteDatabaseRetirement(t.Context(), retirement, plan)
	if err != nil || result.Dirty {
		t.Fatalf("drop %s %s: result=%+v err=%v", retirement.Object.Kind, retirement.Object.Name, result, err)
	}
}

func TestSQLiteDatabaseRetirementAppliesWriteProtectionAndQuarantine(t *testing.T) {
	store := openDatabaseRetirementExecutorStore(t)
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE old_guarded (id TEXT PRIMARY KEY, value TEXT); INSERT INTO old_guarded VALUES ('1', 'before')`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 17, 0, 0, 0, time.UTC)
	executor := NewDatabaseRetirementSQLExecutor(store, func() time.Time { return now }, nil)
	current := executableDatabaseRetirement(now, operationsmodel.DatabaseObjectIdentity{Engine: "sqlite", Database: "runtime", Kind: "table", Name: "old_guarded"})
	current.State = operationsmodel.DatabaseRetirementReadsSwitched
	next := current
	next.State = operationsmodel.DatabaseRetirementWritesDisabled
	protected, err := executor.ApplyDatabaseRetirementTransition(t.Context(), current, next)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `UPDATE old_guarded SET value = 'forbidden' WHERE id = '1'`); err == nil {
		t.Fatal("write protection accepted an update")
	}
	quarantined := protected
	quarantined.State = operationsmodel.DatabaseRetirementQuarantined
	quarantined.Evidence.QuarantineObjectName = "retired_old_guarded_test"
	quarantined, err = executor.ApplyDatabaseRetirementTransition(t.Context(), protected, quarantined)
	if err != nil {
		t.Fatal(err)
	}
	var oldCount, quarantineCount int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='old_guarded'`).Scan(&oldCount); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, quarantined.Evidence.QuarantineObjectName).Scan(&quarantineCount); err != nil {
		t.Fatal(err)
	}
	if oldCount != 0 || quarantineCount != 1 {
		t.Fatalf("quarantine rename old=%d quarantine=%d", oldCount, quarantineCount)
	}
	plan, err := executor.PreviewDatabaseRetirement(t.Context(), quarantined)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.Statements[0], quarantined.Evidence.QuarantineObjectName) || strings.Contains(plan.Statements[0], `"old_guarded"`) {
		t.Fatalf("drop does not target quarantine object: %v", plan.Statements)
	}
	restored := quarantined
	restored.State = operationsmodel.DatabaseRetirementObservationComplete
	if _, err := executor.ApplyDatabaseRetirementTransition(t.Context(), quarantined, restored); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='old_guarded'`).Scan(&oldCount); err != nil || oldCount != 1 {
		t.Fatalf("restore did not recover original table name: count=%d err=%v", oldCount, err)
	}
}

func TestDatabaseRetirementWriteProtectionAndQuarantineAcrossExternalDialects(t *testing.T) {
	for _, test := range []struct {
		name, driver, dsnEnv string
	}{
		{name: "postgres", driver: "pgx", dsnEnv: "RUNTIME_POSTGRES_TEST_DSN"},
		{name: "mysql", driver: "mysql", dsnEnv: "RUNTIME_MYSQL_TEST_DSN"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dsn := strings.TrimSpace(os.Getenv(test.dsnEnv))
			if dsn == "" {
				t.Skipf("%s is not configured", test.dsnEnv)
			}
			store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: test.driver, DatabaseDSN: dsn, IntegrationSecretKey: "retirement-test-key"})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			name := fmt.Sprintf("retirement_guard_%d", time.Now().UnixNano())
			schema := store.DatabaseSchema()
			object := operationsmodel.DatabaseObjectIdentity{Engine: test.name, Database: "runtime", Schema: schema, Kind: "table", Name: name}
			qualified := qualifyRetirementIdentifier(datamigrationEngineForTest(test.name), schema, name)
			if _, err := store.DB().ExecContext(t.Context(), "CREATE TABLE "+qualified+" (id VARCHAR(64) PRIMARY KEY, value VARCHAR(255))"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_, _ = store.DB().ExecContext(t.Context(), "DROP TABLE IF EXISTS "+qualified)
			})
			now := time.Now().UTC()
			executor := NewDatabaseRetirementSQLExecutor(store, func() time.Time { return now }, nil)
			current := executableDatabaseRetirement(now, object)
			current.State = operationsmodel.DatabaseRetirementReadsSwitched
			next := current
			next.State = operationsmodel.DatabaseRetirementWritesDisabled
			protected, err := executor.ApplyDatabaseRetirementTransition(t.Context(), current, next)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO "+qualified+" (id, value) VALUES ("+store.Placeholder(1)+", "+store.Placeholder(2)+")", "blocked", "write"); err == nil {
				t.Fatal("write protection accepted insert")
			}
			quarantined := protected
			quarantined.State = operationsmodel.DatabaseRetirementQuarantined
			quarantined.Evidence.QuarantineObjectName = "retired_" + name
			quarantined, err = executor.ApplyDatabaseRetirementTransition(t.Context(), protected, quarantined)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := executor.PreviewDatabaseRetirement(t.Context(), quarantined)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(plan.Statements[0], quarantined.Evidence.QuarantineObjectName) {
				t.Fatalf("drop plan does not target quarantine object: %v", plan.Statements)
			}
			quarantineQualified := qualifyRetirementIdentifier(datamigrationEngineForTest(test.name), schema, quarantined.Evidence.QuarantineObjectName)
			t.Cleanup(func() { _, _ = store.DB().ExecContext(t.Context(), "DROP TABLE IF EXISTS "+quarantineQualified) })
			result, err := executor.ExecuteDatabaseRetirement(t.Context(), quarantined, plan)
			if err != nil || result.Dirty || result.ExecutedStatements != 1 {
				t.Fatalf("typed destructive execution result=%+v err=%v", result, err)
			}
			inventory, err := datamigration.Inspect(t.Context(), store.DB(), datamigrationEngineForTest(test.name), schema)
			if err != nil {
				t.Fatal(err)
			}
			if _, found := retirementTable(inventory.Tables, operationsmodel.DatabaseObjectIdentity{Kind: "table", Name: quarantined.Evidence.QuarantineObjectName}); found {
				t.Fatal("quarantine table still exists after typed drop")
			}
		})
	}
}

func datamigrationEngineForTest(name string) datamigration.Engine {
	engine, _ := datamigration.ParseEngine(name)
	return engine
}

func openDatabaseRetirementExecutorStore(t *testing.T) *database.RuntimeStore {
	t.Helper()
	if err := principalmodel.ConfigureInstallationWorkspaceID("workspace-primary"); err != nil {
		t.Fatal(err)
	}
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "retirement-executor.db"), IntegrationSecretKey: "retirement-test-key"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	auditmodulefixture.Bind(t, t.Context(), store)
	return store
}

func executableDatabaseRetirement(now time.Time, object operationsmodel.DatabaseObjectIdentity) operationsmodel.DatabaseRetirement {
	verified, drill, quarantine := now.Add(-time.Hour), now.Add(-2*time.Hour), now.Add(-time.Minute)
	return operationsmodel.DatabaseRetirement{
		ID: "retire-" + object.Name, Object: object, State: operationsmodel.DatabaseRetirementQuarantined, UpdatedAt: now.Add(-time.Hour),
		Evidence: operationsmodel.DatabaseRetirementEvidence{
			Owner: "record", Replacement: "replacement", ExpectedSchemaVersion: "2", ExpectedDataVersion: "2", BackfillCheckpoint: "checkpoint", BackfillComplete: true,
			ReadsSwitchVersion: "2.1", WritesDisableVersion: "2.2", WriteProtection: "read_only",
			Observation: operationsmodel.DatabaseAccessObservation{WindowStarted: now.Add(-48 * time.Hour), WindowEnds: now.Add(-24 * time.Hour), SourceCounts: map[string]uint64{"runtime": 0}},
			Comparison:  operationsmodel.DatabaseDataComparison{SourceRows: 1, ReplacementRows: 1, SourceKeys: 1, ReplacementKeys: 1, SourceHash: "same", ReplacementHash: "same", BusinessChecks: []string{"count"}, ComparedAt: now.Add(-25 * time.Hour)},
			Disposition: "migrate", BackupID: "backup", BackupChecksum: "checksum", BackupVerifiedAt: &verified, RestoreDrillAt: &drill, MaintenanceEvidence: "maintenance", DrainEvidence: "drain", ChangePlanID: "change", ApprovalID: "approval", QuarantineUntil: &quarantine, Rollback: "restore backup", AuditEventID: "audit",
		},
	}
}
