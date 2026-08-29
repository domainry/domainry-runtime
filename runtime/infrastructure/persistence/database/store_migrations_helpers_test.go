package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ormdialect "github.com/domainry/domainry-orm/dialect"

	migrationcontract "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/migration"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestMigrationHelpersCoverDialectAndFilesystemEdges(t *testing.T) {
	if got := durationMilliseconds(1500 * time.Millisecond); got != "1500ms" {
		t.Fatalf("durationMilliseconds=%q", got)
	}
	if version, name := migrationIdentity("plain.sql"); version != "plain" || name != "plain" {
		t.Fatalf("migrationIdentity=%q,%q", version, name)
	}

	mysqlStore := &RuntimeStore{engine: mysql.NewEngine()}
	if sql := mysqlStore.schemaMigrationSQL(); !strings.Contains(sql, "VARCHAR(255)") || !strings.Contains(sql, "VARCHAR(64)") {
		t.Fatalf("mysql ledger SQL=%q", sql)
	}
	postgresStore := &RuntimeStore{engine: postgres.NewEngine(), databaseSchema: "runtime"}
	if sql := postgresStore.schemaMigrationSQL(); strings.Contains(sql, "VARCHAR(255)") || !strings.Contains(sql, `"_schema_migrations"`) {
		t.Fatalf("postgres ledger SQL=%q", sql)
	}

	dir := t.TempDir()
	if paths, err := (&RuntimeStore{engine: sqlite.NewEngine()}).migrationPaths(config.Config{MigrationDir: filepath.Join(dir, "missing")}); err != nil || paths != nil {
		t.Fatalf("missing migration directory paths=%v err=%v", paths, err)
	}
	blocked := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (&RuntimeStore{engine: sqlite.NewEngine()}).migrationPaths(config.Config{MigrationDir: blocked}); err == nil {
		t.Fatal("file used as migration directory was accepted")
	}
	entriesDir := filepath.Join(dir, "entries")
	if err := os.MkdirAll(filepath.Join(entriesDir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(entriesDir, "ignored.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(entriesDir)
	if err != nil {
		t.Fatal(err)
	}
	if paths, err := sqlPaths(entriesDir, entries); err != nil || paths != nil {
		t.Fatalf("non-SQL entries paths=%v err=%v", paths, err)
	}

	missing := filepath.Join(dir, "missing.sql")
	store := &RuntimeStore{}
	if err := store.setExpectedMigrations([]string{missing}); err == nil || !strings.Contains(err.Error(), "read migration checksum") {
		t.Fatalf("setExpectedMigrations error=%v", err)
	}
}

func TestMigrationBackupAndTableDiscoveryRejectInvalidInputs(t *testing.T) {
	for _, test := range []struct{ driver, evidence, want string }{
		{driver: "sqlite", want: "MIGRATION_BACKUP_EVIDENCE_PATH"},
		{driver: "postgres", want: "MIGRATION_BACKUP_EVIDENCE_PATH"},
		{driver: "mysql", evidence: filepath.Join(t.TempDir(), "missing.json"), want: "read backup evidence"},
	} {
		if _, err := validateExternalMigrationBackup(test.driver, test.evidence); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("driver=%q error=%v want=%q", test.driver, err, test.want)
		}
	}

	store := &RuntimeStore{engine: sqlite.NewEngine()}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.createSQLiteMigrationBackup(cancelled, config.Config{}); err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("cancelled backup error=%v", err)
	}
	if _, err := store.createSQLiteMigrationBackup(t.Context(), config.Config{DBPath: ":memory:"}); err == nil || !strings.Contains(err.Error(), "not a copyable file path") {
		t.Fatalf("memory backup error=%v", err)
	}

	unsupported := &RuntimeStore{engine: unsupportedMigrationDialect{Engine: sqlite.NewEngine()}}
	if _, err := unsupported.applicationTables(t.Context()); err == nil || !strings.Contains(err.Error(), "unsupported database driver") {
		t.Fatalf("applicationTables error=%v", err)
	}
}

func TestValidateExternalMigrationBackupAcceptsMatchingEvidence(t *testing.T) {
	now := time.Now().UTC()
	evidence := migrationcontract.BackupEvidence{
		EvidenceVersion: "v1", BackupID: "backup-1", Engine: "postgres", BackupType: "full", Provider: "test",
		Region: "test-region", FaultDomain: "test-zone", Owner: "runtime", CreatedAt: now.Add(-time.Hour), VerifiedAt: now.Add(-time.Minute),
		SchemaVersion: "001", Checksum: "checksum", Size: 1, Encrypted: true, ImmutableUntil: now.Add(time.Hour), RTOSeconds: 60, IntegrityOK: true,
		Artifacts: []migrationcontract.Artifact{
			{Kind: "database", Checksum: "database", Size: 1},
			{Kind: "manifest", Checksum: "manifest", Size: 1},
			{Kind: "uploads", Checksum: "uploads", Size: 1},
			{Kind: "key_versions", Checksum: "keys", Size: 1},
			{Kind: "runtime_artifact", Checksum: "runtime", Size: 1},
		},
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "backup-evidence.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := validateExternalMigrationBackup("postgres", path)
	if err != nil || loaded.BackupID != evidence.BackupID {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	if _, err := validateExternalMigrationBackup("mysql", path); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("engine mismatch error=%v", err)
	}
	state := &databaseSQLState{querySteps: []databaseSQLQueryStep{
		{columns: []string{"name"}, rows: [][]driver.Value{{"records"}}},
		{columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}},
	}}
	store := runtimeSchemaStore(t, state)
	store.engine = postgres.NewEngine()
	store.operationalMetrics = NewRuntimeOperationalMetrics("", "")
	if err := store.ensureMigrationBackupForExistingData(t.Context(), config.Config{MigrationBackupEvidencePath: path}); err != nil {
		t.Fatal(err)
	}
	if !store.migrationBackupReady || store.migrationBackupID != evidence.BackupID || store.operationalMetrics.AgeSnapshot().BackupLastSuccess.IsZero() {
		t.Fatalf("ready=%v id=%q metrics=%+v", store.migrationBackupReady, store.migrationBackupID, store.operationalMetrics.AgeSnapshot())
	}
	store = runtimeSchemaStore(t, &databaseSQLState{querySteps: []databaseSQLQueryStep{
		{columns: []string{"name"}, rows: [][]driver.Value{{"records"}}},
		{columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}},
	}})
	store.engine = postgres.NewEngine()
	if err := store.ensureMigrationBackupForExistingData(t.Context(), config.Config{MigrationBackupEvidencePath: path}); err != nil {
		t.Fatalf("external backup without metrics=%v", err)
	}
}

func TestEnsureMigrationBackupCoversEmptyAndExistingSQLiteDatabases(t *testing.T) {
	emptyDB, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "empty.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = emptyDB.Close() })
	emptyStore := &RuntimeStore{db: emptyDB, engine: sqlite.NewEngine()}
	if err := emptyStore.ensureMigrationBackupForExistingData(t.Context(), config.Config{}); err != nil {
		t.Fatal(err)
	}
	if !emptyStore.migrationBackupReady || emptyStore.migrationBackupID != "bootstrap-empty" {
		t.Fatalf("empty backup state ready=%v id=%q", emptyStore.migrationBackupReady, emptyStore.migrationBackupID)
	}
	if err := emptyStore.ensureMigrationBackupForExistingData(t.Context(), config.Config{}); err != nil {
		t.Fatalf("already-ready backup error=%v", err)
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "existing.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE customer (id TEXT PRIMARY KEY); INSERT INTO customer (id) VALUES ('one')`); err != nil {
		t.Fatal(err)
	}
	store := &RuntimeStore{db: db, engine: sqlite.NewEngine(), operationalMetrics: NewRuntimeOperationalMetrics("", "")}
	if err := store.ensureMigrationBackupForExistingData(t.Context(), config.Config{DBPath: dbPath, MigrationBackupDir: filepath.Join(dir, "backups")}); err != nil {
		t.Fatal(err)
	}
	if !store.migrationBackupReady || !strings.HasPrefix(store.migrationBackupID, "sqlite-") {
		t.Fatalf("existing backup state ready=%v id=%q", store.migrationBackupReady, store.migrationBackupID)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "backups"))
	if err != nil || len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".bak.enc") {
		t.Fatalf("backup entries=%v err=%v", entries, err)
	}
	store = &RuntimeStore{db: db, engine: sqlite.NewEngine()}
	if err := store.ensureMigrationBackupForExistingData(t.Context(), config.Config{DBPath: dbPath, MigrationBackupDir: filepath.Join(dir, "backups-without-metrics")}); err != nil {
		t.Fatalf("sqlite backup without metrics=%v", err)
	}
}

type unsupportedMigrationDialect struct{ sqlite.Engine }

func (unsupportedMigrationDialect) Name() ormdialect.Name             { return ormdialect.Name("unsupported") }
func (unsupportedMigrationDialect) SQLDriver() string                 { return "" }
func (unsupportedMigrationDialect) DSN(config.Config) (string, error) { return "", nil }
func (unsupportedMigrationDialect) Configure(context.Context, *sql.DB, config.Config) error {
	return nil
}
func (unsupportedMigrationDialect) SQLDialect() ormdialect.Dialect {
	value, _ := ormdialect.New(ormdialect.SQLite)
	return value
}
func (unsupportedMigrationDialect) SchemaMigrationSQL() string { return "" }
func (unsupportedMigrationDialect) ApplicationTablesQuery(ormdialect.Renderer, string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{}
}
