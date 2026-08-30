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
	for _, table := range []string{"preference_definitions", "rule_set_definitions"} {
		if _, err := store.DB().ExecContext(t.Context(), "CREATE TABLE "+table+" (id TEXT PRIMARY KEY)"); err != nil {
			t.Fatal(err)
		}
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
	for _, table := range []string{"preference_definitions", "rule_set_definitions"} {
		var count int
		if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("retired definition table %s still exists", table)
		}
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
	if _, err := store.DB().ExecContext(t.Context(), "CREATE VIEW runtime_publication_outbox AS SELECT id FROM customer"); err != nil {
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

func TestRuntimeSchemaDoesNotCreateOnlineMetadataAuthoringTables(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"business_change_plan_operations", "business_change_plan_drafts", "application_definition_versions", "preference_definitions", "rule_set_definitions"} {
		var count int
		if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("retired online metadata authoring table %s still exists", table)
		}
	}
	var metadataVersionTableCount int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='metadata_definition_versions'`).Scan(&metadataVersionTableCount); err != nil {
		t.Fatal(err)
	}
	if metadataVersionTableCount != 1 {
		t.Fatalf("Metadata Module definition version table count=%d want=1", metadataVersionTableCount)
	}
}

func TestRuntimeSchemaMigratesLegacyCatalogIntoExplicitProjectionHead(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE application_schema_catalog (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"template_id": "shop", "template_version": "7", "schema_version": "manifest-v7", "schema_hash": "schema-7",
		"default_locale": "zh-CN", "name": "Shop", "identity_seed_synced_version": "identity-3",
	} {
		if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO application_schema_catalog (key, value, updated_at) VALUES (?, ?, ?)`, key, value, "2026-08-30T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	var templateID, artifactVersion, contractVersion, schemaHash, status string
	if err := store.DB().QueryRowContext(t.Context(), `SELECT template_id, artifact_version, contract_version, schema_hash, status FROM _runtime_metadata_projection WHERE id = 'current'`).Scan(&templateID, &artifactVersion, &contractVersion, &schemaHash, &status); err != nil {
		t.Fatal(err)
	}
	if templateID != "shop" || artifactVersion != "7" || contractVersion != "manifest-v7" || schemaHash != "schema-7" || status != "materialized" {
		t.Fatalf("projection=%q,%q,%q,%q,%q", templateID, artifactVersion, contractVersion, schemaHash, status)
	}
	var checkpoint string
	if err := store.DB().QueryRowContext(t.Context(), `SELECT value FROM _runtime_seed_checkpoints WHERE key = 'identity_seed_synced_version'`).Scan(&checkpoint); err != nil || checkpoint != "identity-3" {
		t.Fatalf("checkpoint=%q err=%v", checkpoint, err)
	}
	var legacyCount int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'application_schema_catalog'`).Scan(&legacyCount); err != nil || legacyCount != 0 {
		t.Fatalf("legacy catalog count=%d err=%v", legacyCount, err)
	}
}
