package migration_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	. "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestMigrationRollbackPolicyCoversEveryRuntimeDatabase(t *testing.T) {
	tests := []struct {
		name    string
		profile driver.EngineProfile
		mode    string
	}{
		{name: "sqlite", profile: sqlite.NewEngine(), mode: "restore_sqlite_backup"},
		{name: "postgres", profile: postgres.NewEngine(), mode: "restore_external_backup_or_pitr"},
		{name: "mysql", profile: mysql.NewEngine(), mode: "restore_external_backup"},
	}
	wantProcedure := []string{"stop_runtime", "restart_runtime", "verify_migration_status"}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := test.profile.MigrationRollbackPolicy()
			if policy.Mode != test.mode || !policy.RequiresVerifiedBackup {
				t.Fatalf("policy=%+v", policy)
			}
			for _, required := range wantProcedure {
				found := false
				for _, step := range policy.Procedure {
					found = found || step == required
				}
				if !found {
					t.Fatalf("policy %s missing %s: %+v", test.name, required, policy)
				}
			}
		})
	}
}

func TestExternalDatabaseMigrationRequiresVerifiedBackupConfirmation(t *testing.T) {
	for _, driver := range []string{"postgres", "mysql"} {
		if err := ValidateExternalMigrationBackup(driver, ""); err == nil {
			t.Fatalf("%s accepted migration without verified backup", driver)
		}
	}
}

func TestMigrationDiscoveryUsesDatabaseSpecificDirectory(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name    string
		dialect driver.Engine
	}{
		{name: "sqlite", dialect: sqlite.NewEngine()},
		{name: "postgres", dialect: postgres.NewEngine()},
		{name: "mysql", dialect: mysql.NewEngine()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := filepath.Join(root, test.name)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			migration := filepath.Join(dir, "001_connector_runtime.sql")
			if err := os.WriteFile(migration, []byte("-- "+test.name+" connector runtime migration\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			paths, err := MigrationPathsForEngine(config.Config{MigrationDir: root}, test.dialect)
			if err != nil || len(paths) != 1 || paths[0] != migration {
				t.Fatalf("paths=%v error=%v", paths, err)
			}
		})
	}
}

func TestFailedSQLiteMigrationRollsBackSchemaAndLedger(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "rollback.db")
	migration := filepath.Join(tempDir, "001_failing.sql")
	if err := os.WriteFile(migration, []byte("CREATE TABLE should_rollback (id TEXT PRIMARY KEY);\nINVALID SQL;"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: dbPath, MigrationSQL: migration}); err == nil {
		t.Fatal("invalid migration unexpectedly succeeded")
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var tableCount, ledgerCount int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'should_rollback'").Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _schema_migrations WHERE path = ?", filepath.Base(migration)).Scan(&ledgerCount); err != nil {
		t.Fatal(err)
	}
	var dirty bool
	if err := db.QueryRowContext(t.Context(), "SELECT dirty FROM _schema_migrations WHERE path = ?", filepath.Base(migration)).Scan(&dirty); err != nil {
		t.Fatal(err)
	}
	if tableCount != 0 || ledgerCount != 1 || !dirty {
		t.Fatalf("failed migration leaked schema=%d ledger=%d", tableCount, ledgerCount)
	}
}

func TestSQLiteMigrationBackupHonorsCallerContext(t *testing.T) {
	tempDir := t.TempDir()
	cfg := config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(tempDir, "context.db"), MigrationDir: filepath.Join(tempDir, "none"), MigrationBackupDir: filepath.Join(tempDir, "backups")}
	store, err := OpenContext(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := store.CreateSQLiteMigrationBackup(ctx, cfg); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled backup error=%v", err)
	}
	entries, err := os.ReadDir(cfg.MigrationBackupDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("cancelled backup left files: %#v", entries)
	}
}
