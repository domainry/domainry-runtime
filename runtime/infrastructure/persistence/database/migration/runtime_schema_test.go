package migration_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	. "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestFreshRuntimeSchemaRecordsContentAddressedReceipt(t *testing.T) {
	tempDir := t.TempDir()
	cfg := config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(tempDir, "runtime.db"), MigrationDir: filepath.Join(tempDir, "none")}
	store, err := OpenContext(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var applied, checksumLength int
	var version, path string
	if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*), MIN(version), MIN(path), MIN(LENGTH(checksum)) FROM _schema_migrations WHERE kind = 'runtime_schema'").Scan(&applied, &version, &path, &checksumLength); err != nil {
		t.Fatal(err)
	}
	if applied != 1 || version != "" || checksumLength != 64 || len(path) != len("runtime_schema_sha256_")+64 {
		t.Fatalf("runtime schema receipt rows=%d version=%q path=%q checksum_length=%d", applied, version, path, checksumLength)
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
	if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*), MAX(dirty) FROM _schema_migrations WHERE kind = 'runtime_schema'").Scan(&applied, &dirty); err != nil {
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
	for _, table := range []string{"_definitions", "_definition_versions", "_metadata_definitions", "_metadata_definition_versions"} {
		var count int
		if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("Runtime schema created Definition store table %s: count=%d", table, count)
		}
	}
}
