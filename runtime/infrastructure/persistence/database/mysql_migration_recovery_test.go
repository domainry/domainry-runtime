package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	agentmodule "github.com/domainry/domainry-agent/module"
	auditmodule "github.com/domainry/domainry-audit/module"
	dataexchangemodule "github.com/domainry/domainry-data-exchange/module"
	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	shareddefinition "github.com/domainry/domainry-foundation/definition"
	sharedoperation "github.com/domainry/domainry-foundation/operation"
	sharedsubject "github.com/domainry/domainry-foundation/subjectlifecycle"
	sharedworkerscope "github.com/domainry/domainry-foundation/workerscope"
	integrationmodule "github.com/domainry/domainry-integration/module"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
	lifecyclemodule "github.com/domainry/domainry-lifecycle/module"
	metadatamodule "github.com/domainry/domainry-metadata/module"
	notificationmodule "github.com/domainry/domainry-notification/module"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormmigration "github.com/domainry/domainry-orm/migration"
	reportmodule "github.com/domainry/domainry-report/module"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	schedulermodule "github.com/domainry/domainry-scheduler/module"
	todomodule "github.com/domainry/domainry-todo/module"
	mysqldriver "github.com/go-sql-driver/mysql"
)

func TestCurrentMySQLMigrationsHaveRecoverablePhysicalContracts(t *testing.T) {
	groups := currentMySQLMigrationGroups(t)
	store := &RuntimeStore{engine: mysql.NewEngine()}
	runtimeStatements, err := runtimeSchemaDDL(t.Context(), store, FullRuntimeSchemaCapabilities())
	if err != nil {
		t.Fatal(err)
	}
	groups["runtime"] = []ormmigration.Migration{{Version: 1, Name: "runtime", Statements: runtimeStatements}}
	for name, migrations := range groups {
		for _, migration := range migrations {
			contract, err := buildMySQLMigrationContract(migration.Statements)
			if err != nil {
				t.Fatalf("%s/%d/%s: %v", name, migration.Version, migration.Name, err)
			}
			if len(contract.createdTables)+len(contract.indexes) == 0 {
				t.Fatalf("%s/%d/%s has no physical recovery effects", name, migration.Version, migration.Name)
			}
		}
	}
}

func currentMySQLMigrationGroups(t *testing.T) map[string][]ormmigration.Migration {
	t.Helper()
	renderer, err := ormdialect.ParseRenderer("mysql", "", "")
	if err != nil {
		t.Fatal(err)
	}
	groups := map[string][]ormmigration.Migration{}
	groups["foundation_definition"], err = shareddefinition.SchemaMigrations("mysql", "")
	checkMySQLMigrationGroup(t, groups, "foundation_definition", err)
	groups["foundation_operation"], err = sharedoperation.SchemaMigrations("mysql", "")
	checkMySQLMigrationGroup(t, groups, "foundation_operation", err)
	groups["foundation_artifact"], err = sharedartifact.SchemaMigrations("mysql", "")
	checkMySQLMigrationGroup(t, groups, "foundation_artifact", err)
	groups["foundation_subject"], err = sharedsubject.SchemaMigrations("mysql", "")
	checkMySQLMigrationGroup(t, groups, "foundation_subject", err)
	groups["foundation_worker"], err = sharedworkerscope.SchemaMigrations("mysql", "")
	checkMySQLMigrationGroup(t, groups, "foundation_worker", err)
	groups["audit"], err = auditmodule.SchemaMigrations(renderer, "mysql")
	checkMySQLMigrationGroup(t, groups, "audit", err)
	groups["metadata"], err = metadatamodule.SchemaMigrations("mysql", "")
	checkMySQLMigrationGroup(t, groups, "metadata", err)
	groups["integration"], err = integrationmodule.SchemaMigrations("mysql", "")
	checkMySQLMigrationGroup(t, groups, "integration", err)
	groups["notification"], err = notificationmodule.SchemaMigrations("mysql", "", "")
	checkMySQLMigrationGroup(t, groups, "notification", err)
	groups["lifecycle"], err = lifecyclemodule.SchemaMigrations(renderer)
	checkMySQLMigrationGroup(t, groups, "lifecycle", err)
	groups["scheduler"], err = schedulermodule.SchemaMigrations("mysql", "")
	checkMySQLMigrationGroup(t, groups, "scheduler", err)
	groups["report"], err = reportmodule.SchemaMigrations("mysql", "")
	checkMySQLMigrationGroup(t, groups, "report", err)
	groups["agent"], err = agentmodule.SchemaMigrations("mysql", "")
	checkMySQLMigrationGroup(t, groups, "agent", err)
	groups["knowledge"], err = knowledgemodule.SchemaMigrations(renderer)
	checkMySQLMigrationGroup(t, groups, "knowledge", err)
	groups["todo"], err = todomodule.SchemaMigrations(renderer)
	checkMySQLMigrationGroup(t, groups, "todo", err)
	dataExchange, err := dataexchangemodule.SchemaMigrations("mysql", "")
	if err != nil {
		t.Fatal(err)
	}
	for index, migration := range dataExchange {
		groups["data_exchange"] = append(groups["data_exchange"], ormmigration.Migration{Version: uint(index + 1), Name: migration.ID, Statements: []string{migration.SQL}})
	}
	return groups
}

func checkMySQLMigrationGroup(t *testing.T, groups map[string][]ormmigration.Migration, name string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("render %s MySQL migrations: %v", name, err)
	}
	if len(groups[name]) == 0 {
		t.Fatalf("%s returned no MySQL migrations", name)
	}
}

func TestMySQLMigrationRecoversCommittedDDLAndRejectsDrift(t *testing.T) {
	store := openIsolatedMySQLMigrationStore(t)
	if err := store.ensureMigrationLedger(t.Context()); err != nil {
		t.Fatal(err)
	}

	committed := ormmigration.Migration{Version: 1, Name: "committed_before_receipt", Statements: []string{
		"CREATE TABLE recovery_committed (id VARCHAR(191) NOT NULL PRIMARY KEY)",
		"CREATE UNIQUE INDEX uniq_recovery_committed ON recovery_committed (id)",
	}}
	insertDirtyMySQLMigration(t, store, "recovery", committed)
	executeMySQLStatements(t, store, committed.Statements)
	if err := store.ApplyORMOwnedMigrations(t.Context(), "recovery", []ormmigration.Migration{committed}); err != nil {
		t.Fatalf("recover fully committed DDL: %v", err)
	}
	assertMySQLMigrationClean(t, store, "recovery", committed)

	partial := ormmigration.Migration{Version: 2, Name: "partial_ddl", Statements: []string{
		"CREATE TABLE recovery_partial (id VARCHAR(191) NOT NULL PRIMARY KEY)",
		"CREATE INDEX idx_recovery_partial ON recovery_partial (id)",
	}}
	insertDirtyMySQLMigration(t, store, "recovery", partial)
	executeMySQLStatements(t, store, partial.Statements[:1])
	if err := store.ApplyORMOwnedMigrations(t.Context(), "recovery", []ormmigration.Migration{committed, partial}); err != nil {
		t.Fatalf("resume partial DDL: %v", err)
	}
	assertMySQLMigrationClean(t, store, "recovery", partial)

	adopted := ormmigration.Migration{Version: 1, Name: "missing_receipt", Statements: []string{
		"CREATE TABLE recovery_adopted (id VARCHAR(191) NOT NULL PRIMARY KEY)",
		"CREATE INDEX idx_recovery_adopted ON recovery_adopted (id)",
	}}
	executeMySQLStatements(t, store, adopted.Statements)
	if err := store.ApplyORMOwnedMigrations(t.Context(), "adoption", []ormmigration.Migration{adopted}); err != nil {
		t.Fatalf("adopt exact schema with missing receipt: %v", err)
	}
	assertMySQLMigrationClean(t, store, "adoption", adopted)

	callbackChecksum := "identity-schema-checksum"
	callbackMigration := ormmigration.Migration{Version: 1, Name: "callback_schema", Statements: []string{"source-owned-schema:" + callbackChecksum}}
	insertDirtyMySQLMigration(t, store, "callback", callbackMigration)
	if _, err := store.DB().ExecContext(t.Context(), "CREATE TABLE recovery_callback (id VARCHAR(191) NOT NULL PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyOwnedMigration(t.Context(), "callback", 1, "callback_schema", callbackChecksum, func(ctx context.Context) error {
		var count int
		if err := store.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'recovery_callback' AND column_name = 'id' AND column_type = 'varchar(191)' AND is_nullable = 'NO' AND column_key = 'PRI'").Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("callback schema mismatch")
		}
		return nil
	}); err != nil {
		t.Fatalf("recover idempotent callback migration: %v", err)
	}
	assertMySQLMigrationClean(t, store, "callback", callbackMigration)

	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatalf("create Runtime schema: %v", err)
	}
	definition, err := runtimeSchemaDDL(t.Context(), store, FullRuntimeSchemaCapabilities())
	if err != nil {
		t.Fatal(err)
	}
	runtimeChecksum := runtimeSchemaChecksum(definition)
	if _, err := store.DB().ExecContext(t.Context(), "UPDATE _schema_migrations SET dirty = TRUE WHERE path = ?", runtimeSchemaMigrationPath(runtimeChecksum)); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatalf("recover Runtime schema receipt: %v", err)
	}
	var runtimeDirty bool
	if err := store.DB().QueryRowContext(t.Context(), "SELECT dirty FROM _schema_migrations WHERE path = ?", runtimeSchemaMigrationPath(runtimeChecksum)).Scan(&runtimeDirty); err != nil || runtimeDirty {
		t.Fatalf("Runtime schema dirty=%t err=%v", runtimeDirty, err)
	}
	for name, migrations := range currentMySQLMigrationGroups(t) {
		values := append([]ormmigration.Migration(nil), migrations...)
		for index := range values {
			values[index].Name = compositionMigrationNamePattern.ReplaceAllString(strings.ToLower(strings.TrimSpace(values[index].Name)), "_")
		}
		if err := store.ApplyORMOwnedMigrations(t.Context(), "mysql_contract_"+name, values); err != nil {
			t.Fatalf("apply current %s MySQL migrations: %v", name, err)
		}
	}

	drift := ormmigration.Migration{Version: 1, Name: "reject_drift", Statements: []string{"CREATE TABLE recovery_drift (id VARCHAR(191) NOT NULL PRIMARY KEY)"}}
	insertDirtyMySQLMigration(t, store, "drift", drift)
	if _, err := store.DB().ExecContext(t.Context(), "CREATE TABLE recovery_drift (id BIGINT NOT NULL PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	err = store.ApplyORMOwnedMigrations(t.Context(), "drift", []ormmigration.Migration{drift})
	if err == nil || !strings.Contains(err.Error(), "migration.recovery_mismatch") {
		t.Fatalf("schema drift error=%v", err)
	}
	assertMySQLMigrationDirty(t, store, "drift", drift)
}

func openIsolatedMySQLMigrationStore(t *testing.T) *RuntimeStore {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("RUNTIME_MYSQL_TEST_DSN"))
	if dsn == "" {
		t.Skip("RUNTIME_MYSQL_TEST_DSN is not configured")
	}
	parsed, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	adminConfig := parsed.Clone()
	adminConfig.DBName = ""
	admin, err := sql.Open("mysql", adminConfig.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.PingContext(t.Context()); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	databaseName := fmt.Sprintf("runtime_migration_recovery_%d", time.Now().UnixNano())
	if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE `"+databaseName+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = admin.ExecContext(ctx, "DROP DATABASE IF EXISTS `"+databaseName+"`")
		_ = admin.Close()
	})
	parsed.DBName = databaseName
	store, err := OpenContext(t.Context(), config.Config{DatabaseDriver: "mysql", DatabaseDSN: parsed.FormatDSN(), DatabaseMigrationMode: "apply", RuntimeVersion: "migration-recovery-test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func insertDirtyMySQLMigration(t *testing.T, store *RuntimeStore, owner string, migration ormmigration.Migration) {
	t.Helper()
	path := moduleMigrationPath(owner, migration)
	if err := store.insertOwnedMigration(t.Context(), path, owner, migration, moduleMigrationChecksum(migration), false); err != nil {
		t.Fatal(err)
	}
}

func executeMySQLStatements(t *testing.T, store *RuntimeStore, statements []string) {
	t.Helper()
	for _, statement := range statements {
		if _, err := store.DB().ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
}

func assertMySQLMigrationClean(t *testing.T, store *RuntimeStore, owner string, migration ormmigration.Migration) {
	t.Helper()
	var dirty bool
	if err := store.DB().QueryRowContext(t.Context(), "SELECT dirty FROM _schema_migrations WHERE path = ?", moduleMigrationPath(owner, migration)).Scan(&dirty); err != nil || dirty {
		t.Fatalf("migration %s dirty=%t err=%v", migration.Name, dirty, err)
	}
}

func assertMySQLMigrationDirty(t *testing.T, store *RuntimeStore, owner string, migration ormmigration.Migration) {
	t.Helper()
	var dirty bool
	if err := store.DB().QueryRowContext(t.Context(), "SELECT dirty FROM _schema_migrations WHERE path = ?", moduleMigrationPath(owner, migration)).Scan(&dirty); err != nil || !dirty {
		t.Fatalf("migration %s dirty=%t err=%v", migration.Name, dirty, err)
	}
}
