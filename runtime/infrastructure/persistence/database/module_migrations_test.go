package database

import (
	"path/filepath"
	"strings"
	"testing"

	shareddefinition "github.com/domainry/domainry-foundation/definition"
	"github.com/domainry/domainry-notification-sdk/modulehost"
	ormmigration "github.com/domainry/domainry-orm/migration"
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
	migration := modulehost.SchemaMigration{Version: 1, Name: "create_owned_schema", Statements: []string{"CREATE TABLE notification_owned_test (id TEXT NOT NULL PRIMARY KEY)"}, Baseline: &modulehost.SchemaBaseline{Tables: []modulehost.SchemaTable{{Name: "notification_owned_test", Columns: []modulehost.SchemaColumn{{Name: "id", Type: "TEXT", PrimaryKey: true}}}}}}
	if err := store.ApplyOwnedMigrations(t.Context(), "notification", []modulehost.SchemaMigration{migration}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO notification_owned_test(id) VALUES ('one')"); err != nil {
		t.Fatalf("owned table was not created: %v", err)
	}
	var kind string
	if err := store.DB().QueryRowContext(t.Context(), "SELECT kind FROM _schema_migrations WHERE path = ?", moduleMigrationPath("notification", ormmigration.Migration{Version: migration.Version, Name: migration.Name})).Scan(&kind); err != nil || kind != "module:notification" {
		t.Fatalf("ledger kind=%q err=%v", kind, err)
	}
	migration.Statements[0] = "CREATE TABLE notification_owned_test (id TEXT, changed TEXT)"
	if err := store.ApplyOwnedMigrations(t.Context(), "notification", []modulehost.SchemaMigration{migration}); err == nil || !strings.Contains(err.Error(), "migration.checksum_drift") {
		t.Fatalf("checksum drift error=%v", err)
	}
}

func TestOwnedModuleMigrationAcceptsOneSharedKernelNamespace(t *testing.T) {
	store := openModuleMigrationStore(t)
	migration := modulehost.SchemaMigration{Version: 1, Name: "shared_kernel", Statements: []string{"CREATE TABLE shared_kernel_test (id TEXT NOT NULL PRIMARY KEY)"}}
	if err := store.ApplyOwnedMigrations(t.Context(), shareddefinition.MigrationOwner, []modulehost.SchemaMigration{migration}); err != nil {
		t.Fatal(err)
	}
	var kind string
	if err := store.DB().QueryRowContext(t.Context(), "SELECT kind FROM _schema_migrations WHERE path = ?", moduleMigrationPath(shareddefinition.MigrationOwner, ormmigration.Migration{Version: migration.Version, Name: migration.Name})).Scan(&kind); err != nil || kind != "module:"+shareddefinition.MigrationOwner {
		t.Fatalf("ledger kind=%q err=%v", kind, err)
	}
	if err := store.ApplyOwnedMigrations(t.Context(), "shared/definitions/invalid", []modulehost.SchemaMigration{migration}); err == nil {
		t.Fatal("nested shared migration namespace was accepted")
	}
}

func TestOwnedModuleMigrationBaselinesOnlyCompleteLegacySchema(t *testing.T) {
	store := openModuleMigrationStore(t)
	migration := modulehost.SchemaMigration{Version: 1, Name: "legacy_schema", Statements: []string{"CREATE TABLE notification_legacy_a (id TEXT NOT NULL)", "CREATE TABLE notification_legacy_b (id TEXT NOT NULL)"}, Baseline: &modulehost.SchemaBaseline{Tables: []modulehost.SchemaTable{
		{Name: "notification_legacy_a", Columns: []modulehost.SchemaColumn{{Name: "id", Type: "TEXT"}}},
		{Name: "notification_legacy_b", Columns: []modulehost.SchemaColumn{{Name: "id", Type: "TEXT"}}},
	}}}
	for _, statement := range migration.Statements {
		if _, err := store.DB().ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.ApplyOwnedMigrations(t.Context(), "notification", []modulehost.SchemaMigration{migration}); err != nil {
		t.Fatalf("complete legacy baseline: %v", err)
	}
	var dirty bool
	if err := store.DB().QueryRowContext(t.Context(), "SELECT dirty FROM _schema_migrations WHERE path = ?", moduleMigrationPath("notification", ormmigration.Migration{Version: migration.Version, Name: migration.Name})).Scan(&dirty); err != nil || dirty {
		t.Fatalf("baseline dirty=%v err=%v", dirty, err)
	}

	partial := openModuleMigrationStore(t)
	if _, err := partial.DB().ExecContext(t.Context(), migration.Statements[0]); err != nil {
		t.Fatal(err)
	}
	if err := partial.ApplyOwnedMigrations(t.Context(), "notification", []modulehost.SchemaMigration{migration}); err == nil || !strings.Contains(err.Error(), "migration.baseline_mismatch") {
		t.Fatalf("partial baseline error=%v", err)
	}
}

func TestOwnedModuleMigrationRejectsLegacyColumnAndIndexDrift(t *testing.T) {
	baseline := &modulehost.SchemaBaseline{Tables: []modulehost.SchemaTable{{
		Name:    "notification_legacy_shape",
		Columns: []modulehost.SchemaColumn{{Name: "id", Type: "TEXT"}, {Name: "sequence", Type: "INTEGER"}},
		Indexes: []modulehost.SchemaIndex{{Name: "uniq_notification_legacy_shape", Unique: true, Columns: []string{"id", "sequence"}}},
	}}}
	migration := modulehost.SchemaMigration{Version: 1, Name: "legacy_shape", Statements: []string{
		"CREATE TABLE notification_legacy_shape (id TEXT NOT NULL, sequence INTEGER NOT NULL)",
		"CREATE UNIQUE INDEX uniq_notification_legacy_shape ON notification_legacy_shape (id, sequence)",
	}, Baseline: baseline}
	for _, fixture := range []struct {
		name       string
		statements []string
	}{
		{name: "column type", statements: []string{"CREATE TABLE notification_legacy_shape (id TEXT NOT NULL, sequence TEXT NOT NULL)", "CREATE UNIQUE INDEX uniq_notification_legacy_shape ON notification_legacy_shape (id, sequence)"}},
		{name: "column nullability", statements: []string{"CREATE TABLE notification_legacy_shape (id TEXT NOT NULL, sequence INTEGER)", "CREATE UNIQUE INDEX uniq_notification_legacy_shape ON notification_legacy_shape (id, sequence)"}},
		{name: "index uniqueness", statements: []string{"CREATE TABLE notification_legacy_shape (id TEXT NOT NULL, sequence INTEGER NOT NULL)", "CREATE INDEX uniq_notification_legacy_shape ON notification_legacy_shape (id, sequence)"}},
		{name: "index order", statements: []string{"CREATE TABLE notification_legacy_shape (id TEXT NOT NULL, sequence INTEGER NOT NULL)", "CREATE UNIQUE INDEX uniq_notification_legacy_shape ON notification_legacy_shape (sequence, id)"}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			store := openModuleMigrationStore(t)
			for _, statement := range fixture.statements {
				if _, err := store.DB().ExecContext(t.Context(), statement); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.ApplyOwnedMigrations(t.Context(), "notification", []modulehost.SchemaMigration{migration}); err == nil || !strings.Contains(err.Error(), "migration.baseline_mismatch") {
				t.Fatalf("drift error=%v", err)
			}
		})
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
