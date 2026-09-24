package database

import (
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	auditmodule "github.com/domainry/domainry-audit/module"
	shareddefinition "github.com/domainry/domainry-foundation/definition"
	sharedoperation "github.com/domainry/domainry-foundation/operation"
	"github.com/domainry/domainry-foundation/schemaownership"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identitymodule "github.com/domainry/domainry-identity/module"
	integrationmodule "github.com/domainry/domainry-integration/module"
	lifecyclemodule "github.com/domainry/domainry-lifecycle/module"
	metadatamodule "github.com/domainry/domainry-metadata/module"
	"github.com/domainry/domainry-notification-sdk/modulehost"
	notificationmodule "github.com/domainry/domainry-notification/module"
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

func TestNotificationModuleInstallsItsOwnCanonicalSchema(t *testing.T) {
	store := openModuleMigrationStore(t)
	migrations, err := notificationmodule.SchemaMigrations(store.Driver(), store.DatabaseSchema(), "")
	if err != nil {
		t.Fatal(err)
	}
	assertSourceOwnedModuleSchema(t, store, "Notification", "notification", "_notification_%", migrations, notificationmodule.SchemaOwnership())
}

func TestLifecycleModuleInstallsItsOwnCanonicalSchema(t *testing.T) {
	store := openModuleMigrationStore(t)
	migrations, err := lifecyclemodule.SchemaMigrations(store.SQLRenderer)
	if err != nil {
		t.Fatal(err)
	}
	assertSourceOwnedModuleSchema(t, store, "Lifecycle", lifecyclemodule.MigrationOwner, "_lifecycle_%", migrations, lifecyclemodule.SchemaOwnership())
}

func TestIntegrationModuleInstallsItsOwnCanonicalSchema(t *testing.T) {
	store := openModuleMigrationStore(t)
	migrations, err := integrationmodule.SchemaMigrations(store.Driver(), store.DatabaseSchema())
	if err != nil {
		t.Fatal(err)
	}
	assertSourceOwnedModuleSchema(t, store, "Integration", integrationmodule.MigrationOwner, "_integration_%", migrations, integrationmodule.SchemaOwnership())
}

func TestIdentityModuleInstallsItsOwnCanonicalSchema(t *testing.T) {
	store := openModuleMigrationStore(t)
	handle := identitysdk.DatabaseHandle{
		Pool: store.DB(), Driver: store.Driver(), Migrations: store, ModuleMigrations: store,
	}
	binding, err := identitymodule.NewFactory(identitymodule.Options{
		DatabaseDriver:  store.Driver(),
		AuditFactory:    auditmodule.NewFactory(auditmodule.Options{}),
		MetadataFactory: metadatamodule.NewFactory(),
	}).OpenWithDatabase(
		t.Context(), identitysdk.ApplicationRef{WorkspaceID: "workspace-primary", ApplicationKey: "runtime"}, handle,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = binding.Close(t.Context()) })
	assertSourceOwnedPhysicalSchema(t, store, "Identity", identitymodule.MigrationOwner, "_identity_%", identitymodule.SchemaOwnership())

	var retired int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='_identity_managed_database'`).Scan(&retired); err != nil {
		t.Fatal(err)
	}
	if retired != 0 {
		t.Fatal("Identity retained the redundant managed-database marker table")
	}
	path := moduleMigrationPath(identitymodule.MigrationOwner, ormmigration.Migration{Version: 1, Name: "create_identity_schema"})
	var kind string
	if err := store.DB().QueryRowContext(t.Context(), `SELECT kind FROM _schema_migrations WHERE path = ?`, path).Scan(&kind); err != nil || kind != "module:"+identitymodule.MigrationOwner {
		t.Fatalf("Identity migration ledger kind=%q err=%v", kind, err)
	}
}

func assertSourceOwnedModuleSchema(t *testing.T, store *RuntimeStore, moduleName, owner, tablePattern string, migrations []modulehost.SchemaMigration, ownership []schemaownership.Table) {
	t.Helper()
	for _, migration := range migrations {
		if migration.Baseline != nil {
			t.Fatalf("%s migration %d still carries a host adoption baseline", moduleName, migration.Version)
		}
	}
	if err := store.ApplyOwnedMigrations(t.Context(), owner, migrations); err != nil {
		t.Fatal(err)
	}
	assertSourceOwnedPhysicalSchema(t, store, moduleName, owner, tablePattern, ownership)
}

func assertSourceOwnedPhysicalSchema(t *testing.T, store *RuntimeStore, moduleName, owner, tablePattern string, ownership []schemaownership.Table) {
	t.Helper()
	var count int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name LIKE ?`, tablePattern).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != len(ownership) {
		t.Fatalf("%s physical tables=%d ownership=%d", moduleName, count, len(ownership))
	}
	for _, table := range ownership {
		if table.Owner != owner {
			t.Fatalf("%s table %s owner=%q", moduleName, table.Name, table.Owner)
		}
		rows, err := store.DB().QueryContext(t.Context(), `PRAGMA table_info(`+store.Identifier(table.Name)+`)`)
		if err != nil {
			t.Fatal(err)
		}
		positions := map[int]string{}
		for rows.Next() {
			var cid, notNull, primaryKey int
			var name, columnType string
			var defaultValue sql.NullString
			if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if primaryKey > 0 {
				positions[primaryKey] = name
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		primaryKey := make([]string, len(positions))
		for position, name := range positions {
			primaryKey[position-1] = name
		}
		if !slices.Equal(primaryKey, table.PrimaryKey) {
			t.Fatalf("%s table %s physical primary key=%v ownership=%v", moduleName, table.Name, primaryKey, table.PrimaryKey)
		}
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
	operationMigration := modulehost.SchemaMigration{Version: 1, Name: "shared_operations", Statements: []string{"CREATE TABLE shared_operation_kernel_test (id TEXT NOT NULL PRIMARY KEY)"}}
	if err := store.ApplyOwnedMigrations(t.Context(), sharedoperation.MigrationOwner, []modulehost.SchemaMigration{operationMigration}); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(t.Context(), "SELECT kind FROM _schema_migrations WHERE path = ?", moduleMigrationPath(sharedoperation.MigrationOwner, ormmigration.Migration{Version: operationMigration.Version, Name: operationMigration.Name})).Scan(&kind); err != nil || kind != "module:"+sharedoperation.MigrationOwner {
		t.Fatalf("Operations ledger kind=%q err=%v", kind, err)
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
