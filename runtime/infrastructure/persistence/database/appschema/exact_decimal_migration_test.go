package appschema

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	appschemamysql "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema/mysql"
	appschemapostgres "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema/postgres"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestSQLiteExactDecimalMigrationPreservesDataConstraintsAndEvidenceAcrossRestart(t *testing.T) {
	store := openStoreForMetadataTest(t)
	db := store.raw.DB()
	statements := []string{
		`CREATE TABLE migration_parent (workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL DEFAULT 'created', updated_at TEXT NOT NULL DEFAULT 'updated', code TEXT NOT NULL, amount REAL NOT NULL DEFAULT 0, rate REAL, PRIMARY KEY (workspace_id, id), UNIQUE (workspace_id, code))`,
		`CREATE INDEX idx_migration_parent_amount ON migration_parent (workspace_id, amount)`,
		`CREATE TABLE migration_child (workspace_id TEXT NOT NULL, id TEXT NOT NULL, parent_id TEXT NOT NULL, FOREIGN KEY (workspace_id, parent_id) REFERENCES migration_parent(workspace_id, id))`,
		`INSERT INTO migration_parent (workspace_id, id, code, amount, rate) VALUES ('workspace-a', 'invoice-1', 'A', 12.34, 7.5)`,
		`INSERT INTO migration_child (workspace_id, id, parent_id) VALUES ('workspace-a', 'child-1', 'invoice-1')`,
	}
	for _, statement := range statements {
		if _, err := db.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{
		Key: "migration_parent",
		Fields: []definitionmodel.FieldSchema{
			{Key: "code", Type: "text", Required: true, Unique: true},
			{Key: "amount", Type: "currency", Required: true, Config: map[string]any{"precision": 12, "scale": 2}},
			{Key: "rate", Type: "percent", Config: map[string]any{"precision": 8, "scale": 2}},
		},
	}}}
	plan, err := store.MigrationPlan(t.Context(), metadataTestInstallationScope(), manifest)
	if err != nil || len(plan) != 2 || plan[0].Operation != "alter_column_exact_decimal" || plan[1].Operation != "alter_column_exact_decimal" {
		t.Fatalf("plan=%#v err=%v", plan, err)
	}
	if err := store.SyncManifest(t.Context(), metadataTestInstallationScope(), manifest); err != nil {
		types, _ := store.tableColumnTypes(t.Context(), "migration_parent")
		t.Fatalf("sync error=%v types=%#v", err, types)
	}
	types, err := store.tableColumnTypes(t.Context(), "migration_parent")
	if err != nil || types["amount"] != "TEXT" || types["rate"] != "TEXT" {
		t.Fatalf("types=%#v err=%v", types, err)
	}
	var amount, rate string
	if err := db.QueryRowContext(t.Context(), `SELECT amount, rate FROM migration_parent WHERE workspace_id='workspace-a' AND id='invoice-1'`).Scan(&amount, &rate); err != nil {
		t.Fatal(err)
	}
	if amount != "1000000001234" || rate != "100000750" {
		t.Fatalf("encoded amount=%q rate=%q", amount, rate)
	}
	var evidenceRows, rowCount int64
	var beforeHash, afterHash string
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*), MAX(row_count), MAX(before_hash), MAX(after_hash) FROM metadata_exact_decimal_migrations WHERE object_key='migration_parent'`).Scan(&evidenceRows, &rowCount, &beforeHash, &afterHash); err != nil {
		t.Fatal(err)
	}
	if evidenceRows != 1 || rowCount != 1 || beforeHash == "" || afterHash == "" {
		t.Fatalf("evidence rows=%d row_count=%d before=%q after=%q", evidenceRows, rowCount, beforeHash, afterHash)
	}
	var indexCount, fkViolations int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_migration_parent_amount'`).Scan(&indexCount); err != nil || indexCount != 1 {
		t.Fatalf("index count=%d err=%v", indexCount, err)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&fkViolations); err != nil || fkViolations != 0 {
		t.Fatalf("foreign key violations=%d err=%v", fkViolations, err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO migration_parent (workspace_id,id,code,amount) VALUES ('workspace-a','invoice-2','A','1000000000000')`); err == nil {
		t.Fatal("unique constraint was not preserved")
	}
	var databasePath string
	if err := db.QueryRowContext(t.Context(), `SELECT file FROM pragma_database_list WHERE name='main'`).Scan(&databasePath); err != nil {
		t.Fatal(err)
	}
	if err := store.raw.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: databasePath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	if err := restarted.EnsureApplicationSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := restarted.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	restartedMetadata := NewApplicationSchemaStore(restarted)
	if err := restartedMetadata.SyncManifest(t.Context(), metadataTestInstallationScope(), manifest); err != nil {
		t.Fatalf("runtime restart sync: %v", err)
	}
	if err := restarted.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM metadata_exact_decimal_migrations WHERE object_key='migration_parent'`).Scan(&evidenceRows); err != nil || evidenceRows != 1 {
		t.Fatalf("idempotent evidence rows=%d err=%v", evidenceRows, err)
	}
}

func TestSQLiteExactDecimalMigrationExternalFixture(t *testing.T) {
	databasePath := os.Getenv("DOMAINRY_EXACT_DECIMAL_FIXTURE_DB")
	manifestPath := os.Getenv("DOMAINRY_EXACT_DECIMAL_FIXTURE_MANIFEST")
	if databasePath == "" || manifestPath == "" {
		t.Skip("external exact-decimal fixture is not configured")
	}
	payload, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest manifestmodel.ManifestSchema
	if err := json.Unmarshal(payload, &manifest); err != nil {
		t.Fatal(err)
	}
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: databasePath})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureApplicationSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	metadata := NewApplicationSchemaStore(store)
	plan, err := metadata.MigrationPlan(t.Context(), metadataTestInstallationScope(), manifest)
	if err != nil {
		t.Fatal(err)
	}
	exactSteps := 0
	for _, step := range plan {
		if step.Operation == "alter_column_exact_decimal" {
			exactSteps++
		}
	}
	if exactSteps == 0 {
		t.Fatal("external fixture contains no exact decimal upgrade steps")
	}
	syncErr := metadata.SyncManifest(t.Context(), metadataTestInstallationScope(), manifest)
	if os.Getenv("DOMAINRY_EXACT_DECIMAL_FIXTURE_EXPECT_BLOCKED") == "1" {
		if syncErr == nil {
			t.Fatal("nonconforming external fixture unexpectedly migrated")
		}
		var evidenceRows int
		if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM metadata_exact_decimal_migrations`).Scan(&evidenceRows); err != nil || evidenceRows != 0 {
			t.Fatalf("blocked migration evidence rows=%d err=%v", evidenceRows, err)
		}
		var physicalType string
		if err := store.DB().QueryRowContext(t.Context(), `SELECT type FROM pragma_table_info('checkout_snapshot') WHERE name='total'`).Scan(&physicalType); err != nil || physicalType != "REAL" {
			t.Fatalf("blocked migration changed checkout_snapshot.total type=%q err=%v", physicalType, err)
		}
		t.Logf("exact_steps=%d blocked_error=%v", exactSteps, syncErr)
		return
	}
	if syncErr != nil {
		t.Fatal(syncErr)
	}
	var evidenceRows, migratedRows int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*), COALESCE(SUM(row_count),0) FROM metadata_exact_decimal_migrations`).Scan(&evidenceRows, &migratedRows); err != nil {
		t.Fatal(err)
	}
	if evidenceRows == 0 {
		t.Fatal("external fixture migration produced no evidence")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: databasePath})
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if err := restarted.EnsureApplicationSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := NewApplicationSchemaStore(restarted).SyncManifest(t.Context(), metadataTestInstallationScope(), manifest); err != nil {
		t.Fatalf("external fixture restart: %v", err)
	}
	t.Logf("exact_steps=%d evidence_rows=%d migrated_rows=%d", exactSteps, evidenceRows, migratedRows)
}

func TestSQLiteExactDecimalMigrationRollsBackOnScaleLoss(t *testing.T) {
	store := openStoreForMetadataTest(t)
	db := store.raw.DB()
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE migration_rollback (workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, amount REAL, PRIMARY KEY (workspace_id,id))`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO migration_rollback VALUES ('workspace-a','row-1','now','now',1.234)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE migration_valid_before_failure (workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL, amount REAL, PRIMARY KEY (workspace_id,id))`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO migration_valid_before_failure VALUES ('workspace-a','row-1','now','now',2.50)`); err != nil {
		t.Fatal(err)
	}
	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{
		{Key: "migration_valid_before_failure", Fields: []definitionmodel.FieldSchema{{Key: "amount", Type: "currency", Config: map[string]any{"precision": 8, "scale": 2}}}},
		{Key: "migration_rollback", Fields: []definitionmodel.FieldSchema{{Key: "amount", Type: "currency", Config: map[string]any{"precision": 8, "scale": 2}}}},
	}}
	if err := store.SyncManifest(t.Context(), metadataTestInstallationScope(), manifest); err == nil {
		t.Fatal("scale-losing migration unexpectedly succeeded")
	}
	var physicalType string
	var value float64
	if err := db.QueryRowContext(t.Context(), `SELECT type FROM pragma_table_info('migration_rollback') WHERE name='amount'`).Scan(&physicalType); err != nil || physicalType != "REAL" {
		t.Fatalf("rollback type=%q err=%v", physicalType, err)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT type FROM pragma_table_info('migration_valid_before_failure') WHERE name='amount'`).Scan(&physicalType); err != nil || physicalType != "REAL" {
		t.Fatalf("batch rollback did not restore first table type=%q err=%v", physicalType, err)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT amount FROM migration_rollback WHERE id='row-1'`).Scan(&value); err != nil || value != 1.234 {
		t.Fatalf("rollback value=%v err=%v", value, err)
	}
	var evidence int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM metadata_exact_decimal_migrations WHERE object_key='migration_rollback'`).Scan(&evidence); err != nil || evidence != 0 {
		t.Fatalf("rollback evidence=%d err=%v", evidence, err)
	}
	if err := store.SyncManifest(t.Context(), metadataTestInstallationScope(), manifest); err == nil {
		t.Fatal("idempotent retry must fail closed on the same invalid source value")
	}
}

func TestExactDecimalMigrationDialectStrategies(t *testing.T) {
	field := definitionmodel.FieldSchema{Key: "amount", Type: "currency"}
	if !metadataTestStorageProfile("sqlite").ExactDecimalUpgradeAllowed("REAL", field) ||
		!metadataTestStorageProfile("postgres").ExactDecimalUpgradeAllowed("double precision", field) ||
		!metadataTestStorageProfile("postgres").ExactDecimalUpgradeAllowed("numeric(19,2)", field) ||
		!metadataTestStorageProfile("mysql").ExactDecimalUpgradeAllowed("double", field) ||
		!metadataTestStorageProfile("mysql").ExactDecimalUpgradeAllowed("decimal(19,2)", field) {
		t.Fatal("supported legacy floating types were not recognized")
	}
	if metadataTestStorageProfile("sqlite").ExactDecimalUpgradeAllowed("TEXT", field) || metadataTestStorageProfile("postgres").ExactDecimalUpgradeAllowed("text", field) || metadataTestStorageProfile("mysql").ExactDecimalUpgradeAllowed("varchar(20)", field) {
		t.Fatal("unrelated physical types were accepted")
	}
	postgresProfile := appschemapostgres.NewApplicationSchemaStorageProfile()
	mysqlProfile := appschemamysql.NewApplicationSchemaStorageProfile()
	if got := postgresProfile.ExactDecimalPreflightSQL(`"ledger"`, `"amount"`, 2); got != `SELECT COUNT(*) FROM "ledger" WHERE "amount" IS NOT NULL AND "amount"::numeric <> ROUND("amount"::numeric, 2)` {
		t.Fatalf("postgres preflight=%s", got)
	}
	if got := postgresProfile.ExactDecimalAlterSQL(`"ledger"`, `"amount"`, "NUMERIC(19,2)"); got != `ALTER TABLE "ledger" ALTER COLUMN "amount" TYPE NUMERIC(19,2) USING "amount"::numeric` {
		t.Fatalf("postgres alter=%s", got)
	}
	if got := mysqlProfile.ExactDecimalPreflightSQL("`ledger`", "`amount`", "DECIMAL(19,2)"); got != "SELECT COUNT(*) FROM `ledger` WHERE `amount` IS NOT NULL AND `amount` <> CAST(`amount` AS DECIMAL(19,2))" {
		t.Fatalf("mysql preflight=%s", got)
	}
	if got := mysqlProfile.ExactDecimalAlterSQL("`ledger`", []string{"MODIFY COLUMN `amount` DECIMAL(19,2) NOT NULL DEFAULT '0.00'"}); got != "ALTER TABLE `ledger` MODIFY COLUMN `amount` DECIMAL(19,2) NOT NULL DEFAULT '0.00', ALGORITHM=COPY" {
		t.Fatalf("mysql alter=%s", got)
	}
}

func TestPostgresExactDecimalPostSchemaVerificationUsesDistinctPreparedStatement(t *testing.T) {
	baseDB := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = baseDB.Close() })
	state := metadataSQLState{querySteps: []metadataSQLQueryStep{
		{columns: []string{"workspace_id", "id", "amount"}},
		{columns: []string{"workspace_id", "id", "amount"}},
	}}
	repository := scriptedApplicationSchemaStore(t, &state, NewApplicationSchemaStore(baseDB))
	columns := []metadataExactDecimalColumn{{
		field: definitionmodel.FieldSchema{Key: "amount", Type: "currency", Config: map[string]any{"precision": 19, "scale": 2}},
	}}
	if _, _, err := repository.exactDecimalLogicalHash(t.Context(), repository.database(), "ledger", columns, false); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.exactDecimalLogicalHash(t.Context(), repository.database(), "ledger", columns, false, true); err != nil {
		t.Fatal(err)
	}
	if len(state.queryLog) != 2 || state.queryLog[0] == state.queryLog[1] || !strings.Contains(state.queryLog[1], "domainry_exact_decimal_post_schema_change") {
		t.Fatalf("post-schema verification must use a distinct prepared statement identity: %#v", state.queryLog)
	}
}
