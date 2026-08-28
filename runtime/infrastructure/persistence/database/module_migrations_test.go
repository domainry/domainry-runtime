package database

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/domainry/domainry-notification-sdk/modulehost"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func openModuleMigrationStore(t *testing.T) *RuntimeStore {
	t.Helper()
	store, err := OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "module-migrations.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestOwnedModuleMigrationAppliesAndRejectsChecksumDrift(t *testing.T) {
	store := openModuleMigrationStore(t)
	migration := modulehost.SchemaMigration{Version: 1, Name: "create_owned_schema", Statements: []string{"CREATE TABLE notification_owned_test (id TEXT PRIMARY KEY)"}, BaselineTables: []string{"notification_owned_test"}}
	if err := store.ApplyOwnedMigrations(t.Context(), "notification", []modulehost.SchemaMigration{migration}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO notification_owned_test(id) VALUES ('one')"); err != nil {
		t.Fatalf("owned table was not created: %v", err)
	}
	var kind string
	if err := store.DB().QueryRowContext(t.Context(), "SELECT kind FROM _schema_migrations WHERE path = ?", moduleMigrationPath("notification", migration)).Scan(&kind); err != nil || kind != "module:notification" {
		t.Fatalf("ledger kind=%q err=%v", kind, err)
	}
	migration.Statements[0] = "CREATE TABLE notification_owned_test (id TEXT, changed TEXT)"
	if err := store.ApplyOwnedMigrations(t.Context(), "notification", []modulehost.SchemaMigration{migration}); err == nil || !strings.Contains(err.Error(), "migration.checksum_drift") {
		t.Fatalf("checksum drift error=%v", err)
	}
}

func TestOwnedModuleMigrationBaselinesOnlyCompleteLegacySchema(t *testing.T) {
	store := openModuleMigrationStore(t)
	migration := modulehost.SchemaMigration{Version: 1, Name: "legacy_schema", Statements: []string{"CREATE TABLE notification_legacy_a (id TEXT)", "CREATE TABLE notification_legacy_b (id TEXT)"}, BaselineTables: []string{"notification_legacy_a", "notification_legacy_b"}}
	for _, statement := range migration.Statements {
		if _, err := store.DB().ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.ApplyOwnedMigrations(t.Context(), "notification", []modulehost.SchemaMigration{migration}); err != nil {
		t.Fatalf("complete legacy baseline: %v", err)
	}
	var dirty bool
	if err := store.DB().QueryRowContext(t.Context(), "SELECT dirty FROM _schema_migrations WHERE path = ?", moduleMigrationPath("notification", migration)).Scan(&dirty); err != nil || dirty {
		t.Fatalf("baseline dirty=%v err=%v", dirty, err)
	}

	partial := openModuleMigrationStore(t)
	if _, err := partial.DB().ExecContext(t.Context(), migration.Statements[0]); err != nil {
		t.Fatal(err)
	}
	if err := partial.ApplyOwnedMigrations(t.Context(), "notification", []modulehost.SchemaMigration{migration}); err == nil || !strings.Contains(err.Error(), "migration.partial_baseline") {
		t.Fatalf("partial baseline error=%v", err)
	}
}

func TestOwnedModuleMigrationVerifyModeRejectsPendingVersion(t *testing.T) {
	store := openModuleMigrationStore(t)
	store.config.DatabaseMigrationMode = "verify"
	migration := modulehost.SchemaMigration{Version: 1, Name: "pending_schema", Statements: []string{"CREATE TABLE notification_pending_test (id TEXT)"}}
	if err := store.ApplyOwnedMigrations(t.Context(), "notification", []modulehost.SchemaMigration{migration}); err == nil || !strings.Contains(err.Error(), "migration.pending") {
		t.Fatalf("verify pending error=%v", err)
	}
}
