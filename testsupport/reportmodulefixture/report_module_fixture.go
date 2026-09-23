package reportmodulefixture

import (
	"context"

	metadatasdk "github.com/domainry/domainry-metadata-sdk"
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
	binding, err := Open(ctx, store)
	if err != nil {
		return err
	}
	return binding.Close(context.WithoutCancel(ctx))
}

func Open(ctx context.Context, store *database.RuntimeStore) (reportsdk.Binding, error) {
	return reportmodule.NewFactory().Open(ctx, reportsdk.ApplicationRef{RuntimeID: "report-module-fixture"}, host{store: store})
}

type host struct{ store *database.RuntimeStore }

func (h host) Database() reportmodulehost.Database { return h.store.DB() }
func (h host) DatabaseFor(ctx context.Context) reportmodulehost.DBTX {
	if tx := database.ActionExecutionTransaction(ctx); tx != nil {
		return tx
	}
	return h.store.DB()
}
func (h host) Dialect() reportmodulehost.Dialect { return h.store.SQLRenderer }
func (h host) Migrations() reportmodulehost.MigrationRegistrar {
	return registrar{store: h.store}
}
func (h host) DefinitionStore() metadatasdk.DefinitionStore {
	if h.store != nil && h.store.Metadata() != nil {
		return h.store.Metadata().DefinitionStore()
	}
	return fixtureDefinitionStore{}
}

type fixtureDefinitionStore struct{ metadatasdk.Definitions }

func (fixtureDefinitionStore) List(context.Context, metadatasdk.DefinitionQuery) ([]metadatasdk.Definition, error) {
	return nil, nil
}
func (fixtureDefinitionStore) Get(context.Context, string, string, string) (metadatasdk.Definition, bool, error) {
	return metadatasdk.Definition{}, false, nil
}
func (fixtureDefinitionStore) Snapshot(context.Context, metadatasdk.DefinitionQuery) (metadatasdk.DefinitionSnapshot, error) {
	return metadatasdk.DefinitionSnapshot{}, nil
}
func (fixtureDefinitionStore) ReplaceSourceSnapshot(context.Context, metadatasdk.ProjectionSnapshot) error {
	return nil
}
func (fixtureDefinitionStore) Publish(context.Context, metadatasdk.DefinitionPublishCommand) (metadatasdk.DefinitionPublishResult, error) {
	return metadatasdk.DefinitionPublishResult{}, nil
}
func (fixtureDefinitionStore) Disable(context.Context, metadatasdk.DefinitionDisableCommand) error {
	return nil
}
func (fixtureDefinitionStore) GetVersion(context.Context, metadatasdk.DefinitionVersionQuery) (metadatasdk.DefinitionVersion, bool, error) {
	return metadatasdk.DefinitionVersion{}, false, nil
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
