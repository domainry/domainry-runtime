package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite"
	"github.com/domainry/domainry-runtime/runtime/platform/config"

	_ "modernc.org/sqlite"
)

func TestFileMigrationsUseDedicatedManagementConnection(t *testing.T) {
	queryDB := openMigrationOwnerSQLite(t, "query")
	migrationDB := openMigrationOwnerSQLite(t, "migration")
	store := &RuntimeStore{db: queryDB, migrationDB: migrationDB, engine: sqlite.NewEngine()}

	migrationPath := filepath.Join(t.TempDir(), "001_management_owner.sql")
	if err := os.WriteFile(migrationPath, []byte(`CREATE TABLE management_owned (id TEXT PRIMARY KEY);`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.applyMigrations(t.Context(), config.Config{MigrationSQL: migrationPath}); err != nil {
		t.Fatal(err)
	}
	if sqliteTableExists(t, migrationDB, "management_owned") != 1 {
		t.Fatal("migration table was not created on management connection")
	}
	if sqliteTableExists(t, queryDB, "management_owned") != 0 {
		t.Fatal("migration leaked onto query connection")
	}
}

func TestRuntimeSchemaUsesDedicatedManagementConnection(t *testing.T) {
	queryDB := openMigrationOwnerSQLite(t, "runtime-query")
	migrationDB := openMigrationOwnerSQLite(t, "runtime-migration")
	store := &RuntimeStore{db: queryDB, migrationDB: migrationDB, engine: sqlite.NewEngine(), config: config.Config{MigrationBackupDir: t.TempDir()}}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if sqliteTableExists(t, migrationDB, "_project_model_state") != 1 {
		t.Fatal("runtime schema was not created on management connection")
	}
	if sqliteTableExists(t, queryDB, "_project_model_state") != 0 {
		t.Fatal("runtime schema leaked onto query connection")
	}
}

func openMigrationOwnerSQLite(t *testing.T, name string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+name+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func sqliteTableExists(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
