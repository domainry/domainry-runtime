package database_test

import (
	"context"
	"path/filepath"
	"testing"

	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/testsupport/lifecyclesdkfixture"
)

var lifecycleOwnedTables = []string{
	"_lifecycle_cleanup_jobs", "_lifecycle_legal_holds", "_subject_requests",
	"_subject_steps",
}

func openLifecycleMigrationBinding(t *testing.T, store *database.RuntimeStore, runtimeID string) lifecyclesdk.Binding {
	t.Helper()
	binding, err := lifecyclesdkfixture.Open(t.Context(), store, runtimeID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = binding.Close(context.Background()) })
	return binding
}

func TestLifecycleModuleAloneAppliesOwnedMigrationToHostLedger(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "lifecycle-module.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	var before int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name LIKE '_lifecycle_%'`).Scan(&before); err != nil || before != 0 {
		t.Fatalf("Runtime pre-created Lifecycle tables=%d err=%v", before, err)
	}
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='_subject_steps'`).Scan(&before); err != nil || before != 0 {
		t.Fatalf("Runtime pre-created shared subject step table=%d err=%v", before, err)
	}
	openLifecycleMigrationBinding(t, store, "lifecycle-migration-test")
	for _, table := range lifecycleOwnedTables {
		var found int
		if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&found); err != nil || found != 1 {
			t.Fatalf("table %s count=%d err=%v", table, found, err)
		}
	}
	for _, retired := range []string{"_lifecycle_account_erasure_approvals", "_lifecycle_external_erasure_requests", "_lifecycle_deletion_registry", "_lifecycle_policy_versions", "_lifecycle_subject_erasure_fences", "_lifecycle_subject_execution_steps", "_lifecycle_audit_evidence", "_lifecycle_archive_entries", "_lifecycle_file_artifacts"} {
		var found int
		if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, retired).Scan(&found); err != nil || found != 0 {
			t.Fatalf("retired table %s count=%d err=%v", retired, found, err)
		}
	}
	var migrations int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _schema_migrations WHERE kind='module:lifecycle' AND dirty=FALSE`).Scan(&migrations); err != nil || migrations != 1 {
		t.Fatalf("Lifecycle ledger rows=%d err=%v", migrations, err)
	}
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _schema_migrations WHERE kind='module:shared/subject-lifecycle' AND dirty=FALSE`).Scan(&migrations); err != nil || migrations != 1 {
		t.Fatalf("shared Subject Lifecycle ledger rows=%d err=%v", migrations, err)
	}
}

func TestLifecycleModuleReopenKeepsDirectOwnedSchemaOnce(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "lifecycle-adoption.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	openLifecycleMigrationBinding(t, store, "lifecycle-first-open")
	openLifecycleMigrationBinding(t, store, "lifecycle-reopen")
	var clean int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _schema_migrations WHERE kind='module:lifecycle' AND dirty=FALSE`).Scan(&clean); err != nil || clean != 1 {
		t.Fatalf("reopened lifecycle ledger rows=%d err=%v", clean, err)
	}
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _schema_migrations WHERE kind='module:shared/subject-lifecycle' AND dirty=FALSE`).Scan(&clean); err != nil || clean != 1 {
		t.Fatalf("reopened shared Subject Lifecycle ledger rows=%d err=%v", clean, err)
	}
}
