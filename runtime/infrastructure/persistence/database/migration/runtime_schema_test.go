package migration_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	. "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestRuntimeSchemaMigrationBacksUpExistingDataAndRecordsVersion(t *testing.T) {
	tempDir := t.TempDir()
	cfg := config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(tempDir, "runtime.db"), MigrationDir: filepath.Join(tempDir, "none"), MigrationBackupDir: filepath.Join(tempDir, "backups")}
	store, err := OpenContext(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), "CREATE TABLE customer (id TEXT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO customer (id) VALUES (?)", "existing"); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	entries, err := os.ReadDir(cfg.MigrationBackupDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("runtime schema backups=%v error=%v", entries, err)
	}
	var applied int
	if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _schema_migrations WHERE path = ?", "runtime_schema_"+CurrentRuntimeSchemaVersion).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("runtime schema version rows=%d", applied)
	}
}

func TestRuntimeSchemaMigrationFailureLeavesDirtyLedger(t *testing.T) {
	tempDir := t.TempDir()
	cfg := config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(tempDir, "failure.db"), MigrationDir: filepath.Join(tempDir, "none"), MigrationBackupDir: filepath.Join(tempDir, "backups")}
	store, err := OpenContext(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB().ExecContext(t.Context(), "CREATE TABLE customer (id TEXT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO customer (id) VALUES (?)", "existing"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), "CREATE VIEW _publication_outbox AS SELECT id FROM customer"); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err == nil {
		t.Fatal("broken runtime schema unexpectedly migrated")
	}
	var applied int
	var dirty bool
	if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*), MAX(dirty) FROM _schema_migrations WHERE path = ?", "runtime_schema_"+CurrentRuntimeSchemaVersion).Scan(&applied, &dirty); err != nil {
		t.Fatal(err)
	}
	if applied != 1 || !dirty {
		t.Fatalf("failed runtime schema ledger rows=%d dirty=%v", applied, dirty)
	}
}

func openStoreForGeneratedListTest(t *testing.T) *RuntimeStore {
	t.Helper()
	store, err := OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "runtime-schema.db")})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestRuntimeSchemaMigrationHonorsCallerContext(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.EnsureRuntimeSchema(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled runtime schema error=%v", err)
	}
}

func TestRuntimeSchemaDoesNotCreateMetadataOwnedTables(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	var metadataVersionTableCount int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='_metadata_definition_versions'`).Scan(&metadataVersionTableCount); err != nil {
		t.Fatal(err)
	}
	if metadataVersionTableCount != 0 {
		t.Fatalf("Runtime schema created Metadata-owned definition version table: count=%d", metadataVersionTableCount)
	}
}
