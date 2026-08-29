package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	ormdialect "github.com/domainry/domainry-orm/dialect"

	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestApplicationTablesDialectAndFailureEdges(t *testing.T) {
	tests := []struct {
		name    string
		dialect dialect
	}{
		{"sqlite", sqlite.NewEngine()},
		{"mysql", mysql.NewEngine()},
		{"postgres", postgres.NewEngine()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := &databaseSQLState{querySteps: []databaseSQLQueryStep{{columns: []string{"name"}, rows: [][]driver.Value{{"zeta"}, {"_schema_migrations"}, {"alpha"}, {"_schema_materializations"}, {""}}}}}
			store := runtimeSchemaStore(t, state)
			store.dialect = test.dialect
			store.databaseSchema = "runtime"
			tables, err := store.applicationTables(t.Context())
			if err != nil || !reflect.DeepEqual(tables, []string{"alpha", "zeta"}) {
				t.Fatalf("tables=%#v err=%v", tables, err)
			}
		})
	}
	store := runtimeSchemaStore(t, &databaseSQLState{})
	store.dialect = namedTestDialect{Engine: sqlite.NewEngine(), name: "oracle"}
	if _, err := store.applicationTables(t.Context()); err == nil {
		t.Fatal("unsupported dialect accepted")
	}
	for _, step := range []databaseSQLQueryStep{
		{err: errDatabaseSQL},
		{columns: []string{"name"}, rows: [][]driver.Value{{nil}}},
		{columns: []string{"name"}, nextErr: errDatabaseSQL},
	} {
		store := runtimeSchemaStore(t, &databaseSQLState{querySteps: []databaseSQLQueryStep{step}})
		if _, err := store.applicationTables(t.Context()); err == nil {
			t.Fatal("expected inventory error")
		}
	}
}

func TestExistingApplicationDataEdges(t *testing.T) {
	store := runtimeSchemaStore(t, &databaseSQLState{querySteps: []databaseSQLQueryStep{{err: errDatabaseSQL}}})
	if _, err := store.hasExistingApplicationData(t.Context()); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("inventory error=%v", err)
	}
	store = runtimeSchemaStore(t, &databaseSQLState{querySteps: []databaseSQLQueryStep{
		{columns: []string{"name"}, rows: [][]driver.Value{{"records"}}},
		{err: errDatabaseSQL},
	}})
	if _, err := store.hasExistingApplicationData(t.Context()); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("count error=%v", err)
	}
	for _, count := range []int64{0, 1} {
		store = runtimeSchemaStore(t, &databaseSQLState{querySteps: []databaseSQLQueryStep{
			{columns: []string{"name"}, rows: [][]driver.Value{{"records"}}},
			{columns: []string{"count"}, rows: [][]driver.Value{{count}}},
		}})
		hasData, err := store.hasExistingApplicationData(t.Context())
		if err != nil || hasData != (count > 0) {
			t.Fatalf("count=%d hasData=%v err=%v", count, hasData, err)
		}
	}
}

func TestEnsureMigrationBackupShortCircuitsAndFailures(t *testing.T) {
	store := runtimeSchemaStore(t, &databaseSQLState{})
	store.migrationBackupReady = true
	if err := store.ensureMigrationBackupForExistingData(t.Context(), config.Config{}); err != nil {
		t.Fatal(err)
	}
	store = runtimeSchemaStore(t, &databaseSQLState{})
	if err := store.ensureMigrationBackupForExistingData(t.Context(), config.Config{}); err != nil || !store.migrationBackupReady || store.migrationBackupID != "bootstrap-empty" {
		t.Fatalf("ready=%v id=%q err=%v", store.migrationBackupReady, store.migrationBackupID, err)
	}
	store = runtimeSchemaStore(t, &databaseSQLState{querySteps: []databaseSQLQueryStep{{err: errDatabaseSQL}}})
	if err := store.ensureMigrationBackupForExistingData(t.Context(), config.Config{}); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("inventory error=%v", err)
	}

	backupDir := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(backupDir, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	store = runtimeSchemaStore(t, &databaseSQLState{querySteps: []databaseSQLQueryStep{
		{columns: []string{"name"}, rows: [][]driver.Value{{"records"}}},
		{columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}},
	}})
	if err := store.ensureMigrationBackupForExistingData(t.Context(), config.Config{DBPath: "runtime.db", MigrationBackupDir: backupDir}); err == nil || !strings.Contains(err.Error(), "backup directory") {
		t.Fatalf("backup error=%v", err)
	}

	store = runtimeSchemaStore(t, &databaseSQLState{querySteps: []databaseSQLQueryStep{
		{columns: []string{"name"}, rows: [][]driver.Value{{"records"}}},
		{columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}},
	}})
	store.dialect = postgres.NewEngine()
	if err := store.ensureMigrationBackupForExistingData(t.Context(), config.Config{}); err == nil || !strings.Contains(err.Error(), "MIGRATION_BACKUP_EVIDENCE_PATH") {
		t.Fatalf("external evidence error=%v", err)
	}
}

func TestCreateSQLiteMigrationBackupSQLAndEncryptionFailures(t *testing.T) {
	backupDir := t.TempDir()
	cfg := config.Config{DBPath: filepath.Join(t.TempDir(), "runtime.db"), MigrationBackupDir: backupDir}
	store := runtimeSchemaStore(t, &databaseSQLState{execSteps: []databaseSQLExecStep{{err: errDatabaseSQL}}})
	if _, err := store.createSQLiteMigrationBackup(t.Context(), cfg); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("vacuum error=%v", err)
	}
	store = runtimeSchemaStore(t, &databaseSQLState{})
	if _, err := store.createSQLiteMigrationBackup(t.Context(), config.Config{}); err == nil {
		t.Fatal("empty database path accepted")
	}
	if _, err := store.createSQLiteMigrationBackup(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "encrypt sqlite migration backup") {
		t.Fatalf("encryption error=%v", err)
	}
	cfg.DatabaseDSN = "file:memory"
	if _, err := store.createSQLiteMigrationBackup(t.Context(), cfg); err == nil {
		t.Fatal("file DSN accepted")
	}
}

func TestSQLiteMigrationBackupChecksumAndStatFailures(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "runtime.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.ExecContext(t.Context(), `CREATE TABLE records(id TEXT); INSERT INTO records VALUES ('one')`); err != nil {
		t.Fatal(err)
	}
	store := &RuntimeStore{db: db, dialect: sqlite.NewEngine(), backupChecksum: func(string) (string, error) { return "", errDatabaseSQL }}
	if err := store.ensureMigrationBackupForExistingData(t.Context(), config.Config{DBPath: dbPath, MigrationBackupDir: filepath.Join(dir, "checksum")}); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("checksum error=%v", err)
	}

	assertStatFailure := func(t *testing.T, symlink bool, match string) {
		t.Helper()
		caseDir := t.TempDir()
		caseDB := filepath.Join(caseDir, "runtime.db")
		caseBackup := filepath.Join(caseDir, "backups")
		if err := os.MkdirAll(caseBackup, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(caseBackup, filepath.Base(caseDB)+"."+time.Now().UTC().Format("20060102T150405Z")+".bak.enc")
		if symlink {
			if err := os.Symlink(path, path); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
			t.Fatal(err)
		}
		candidate := runtimeSchemaStore(t, &databaseSQLState{})
		_, err := candidate.createSQLiteMigrationBackup(t.Context(), config.Config{DBPath: caseDB, MigrationBackupDir: caseBackup})
		if err == nil || !strings.Contains(err.Error(), match) {
			t.Fatalf("stat failure=%v", err)
		}
	}
	assertStatFailure(t, false, "already exists")
	assertStatFailure(t, true, "inspect sqlite migration backup")
}

type namedTestDialect struct {
	sqlite.Engine
	name string
}

func (dialect namedTestDialect) Name() ormdialect.Name { return ormdialect.Name(dialect.name) }
func (namedTestDialect) SQLDriver() string             { return "sqlite" }
func (namedTestDialect) DSN(config.Config) (string, error) {
	return "", nil
}
func (namedTestDialect) Configure(context.Context, *sql.DB, string) error { return nil }
func (namedTestDialect) SQLDialect() ormdialect.Dialect {
	value, _ := ormdialect.New(ormdialect.SQLite)
	return value
}
func (namedTestDialect) SchemaMigrationSQL() string { return "" }
func (namedTestDialect) ApplicationTablesQuery(ormdialect.Renderer, string) persistencedriver.SchemaQuery {
	return persistencedriver.SchemaQuery{}
}
