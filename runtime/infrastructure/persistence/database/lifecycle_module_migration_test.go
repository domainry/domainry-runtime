package database_test

import (
	"context"
	"path/filepath"
	"testing"

	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecyclemoduleimpl "github.com/domainry/domainry-lifecycle/module"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	lifecyclemodule "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/lifecyclemodule"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

var lifecycleOwnedTables = []string{
	"_lifecycle_archive_entries", "_lifecycle_audit_evidence", "_lifecycle_cleanup_jobs",
	"_lifecycle_deletion_registry", "_lifecycle_external_erasure_requests", "_lifecycle_file_artifacts",
	"_lifecycle_legal_holds", "_lifecycle_policy_versions", "_lifecycle_subject_requests",
	"_lifecycle_subject_execution_steps",
}

func openLifecycleMigrationBinding(t *testing.T, store *database.RuntimeStore, runtimeID string) lifecyclesdk.Binding {
	t.Helper()
	binding, err := lifecyclemoduleimpl.NewFactory().OpenModule(t.Context(), lifecyclesdk.ApplicationRef{RuntimeID: runtimeID}, lifecyclemodule.NewHost(store))
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
	openLifecycleMigrationBinding(t, store, "lifecycle-migration-test")
	for _, table := range lifecycleOwnedTables {
		var found int
		if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&found); err != nil || found != 1 {
			t.Fatalf("table %s count=%d err=%v", table, found, err)
		}
	}
	var migrations int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _schema_migrations WHERE kind='module:lifecycle' AND dirty=FALSE`).Scan(&migrations); err != nil || migrations != 2 {
		t.Fatalf("Lifecycle ledger rows=%d err=%v", migrations, err)
	}
}

func TestLifecycleModuleReopenAdoptsExistingOwnedSchemaOnce(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "lifecycle-adoption.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	openLifecycleMigrationBinding(t, store, "lifecycle-first-open")
	if _, err := store.DB().ExecContext(t.Context(), `DELETE FROM _schema_migrations WHERE kind='module:lifecycle'`); err != nil {
		t.Fatal(err)
	}
	openLifecycleMigrationBinding(t, store, "lifecycle-adoption")
	var clean int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _schema_migrations WHERE kind='module:lifecycle' AND dirty=FALSE`).Scan(&clean); err != nil || clean != 2 {
		t.Fatalf("adopted lifecycle ledger rows=%d err=%v", clean, err)
	}
}
