package database

import (
	"path/filepath"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

var lifecycleOwnedTables = []string{
	"_lifecycle_archive_entries", "_lifecycle_audit_evidence", "_lifecycle_cleanup_jobs",
	"_lifecycle_deletion_registry", "_lifecycle_external_erasure_requests", "_lifecycle_file_artifacts",
	"_lifecycle_legal_holds", "_lifecycle_policy_versions", "_lifecycle_subject_requests",
}

func TestRuntimeAppliesLifecycleOwnedMigrationToSoleLedger(t *testing.T) {
	store, err := OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "lifecycle-module.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, table := range lifecycleOwnedTables {
		var found int
		if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&found); err != nil || found != 1 {
			t.Fatalf("table %s count=%d err=%v", table, found, err)
		}
	}
	var migrations int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _schema_migrations WHERE kind='module:lifecycle' AND dirty=FALSE`).Scan(&migrations); err != nil || migrations != 1 {
		t.Fatalf("Lifecycle ledger rows=%d err=%v", migrations, err)
	}
	var privateLedgers int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name LIKE 'lifecycle%schema%migration%'`).Scan(&privateLedgers); err != nil || privateLedgers != 0 {
		t.Fatalf("Lifecycle private ledgers=%d err=%v", privateLedgers, err)
	}
}

func TestLifecycleMigrationAdoptsCompletePreExtractionSchema(t *testing.T) {
	store, err := OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "lifecycle-adoption.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureLifecycleSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `DELETE FROM _schema_migrations WHERE kind='module:lifecycle'`); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatalf("adopt complete pre-extraction Lifecycle schema: %v", err)
	}
	var clean int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _schema_migrations WHERE kind='module:lifecycle' AND dirty=FALSE`).Scan(&clean); err != nil || clean != 1 {
		t.Fatalf("adopted lifecycle ledger rows=%d err=%v", clean, err)
	}
}
