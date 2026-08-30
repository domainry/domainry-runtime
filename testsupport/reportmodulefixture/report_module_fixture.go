package reportmodulefixture

import (
	"context"

	notificationmodulehost "github.com/domainry/domainry-notification-sdk/modulehost"
	reportsdk "github.com/domainry/domainry-report-sdk"
	reportmodulehost "github.com/domainry/domainry-report-sdk/modulehost"
	reportmodule "github.com/domainry/domainry-report/module"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

// EnsureSchema opens the real Report Module against the host database so tests
// exercise the same source-owned migration registration used at Runtime
// startup. EnsureRuntimeSchema intentionally cannot create Report-owned tables.
func EnsureSchema(ctx context.Context, store *database.RuntimeStore) error {
	binding, err := reportmodule.NewFactory().OpenModule(ctx, reportsdk.ApplicationRef{RuntimeID: "report-module-fixture"}, host{store: store})
	if err != nil {
		return err
	}
	return binding.Close(context.WithoutCancel(ctx))
}

type host struct{ store *database.RuntimeStore }

func (h host) Database() reportmodulehost.Database { return h.store.DB() }
func (h host) Dialect() reportmodulehost.Dialect   { return h.store.SQLRenderer }
func (h host) Migrations() reportmodulehost.MigrationRegistrar {
	return registrar{store: h.store}
}

type registrar struct{ store *database.RuntimeStore }

func (r registrar) Driver() string { return r.store.Driver() }
func (r registrar) Schema() string { return r.store.DatabaseSchema() }
func (r registrar) ApplyOwnedMigrations(ctx context.Context, owner string, migrations []reportmodulehost.SchemaMigration) error {
	values := make([]notificationmodulehost.SchemaMigration, len(migrations))
	for index, migration := range migrations {
		values[index] = notificationmodulehost.SchemaMigration{
			Version: migration.Version, Name: migration.Name,
			Statements: append([]string(nil), migration.Statements...),
		}
	}
	return r.store.ApplyOwnedMigrations(ctx, owner, values)
}
