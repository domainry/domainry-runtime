package runtime

import (
	"context"

	metadatamodulehost "github.com/domainry/domainry-metadata-sdk/modulehost"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type runtimeMetadataModuleHost struct{ store *persistence.RuntimeStore }

func (h runtimeMetadataModuleHost) Database() metadatamodulehost.Database { return h.store.DB() }
func (h runtimeMetadataModuleHost) Dialect() metadatamodulehost.Dialect   { return h.store.SQLRenderer }
func (h runtimeMetadataModuleHost) Migrations() metadatamodulehost.MigrationRegistrar {
	return runtimeMetadataMigrationRegistrar{store: h.store}
}

type runtimeMetadataMigrationRegistrar struct{ store *persistence.RuntimeStore }

func (r runtimeMetadataMigrationRegistrar) Driver() string { return r.store.Driver() }
func (r runtimeMetadataMigrationRegistrar) Schema() string { return r.store.DatabaseSchema() }
func (r runtimeMetadataMigrationRegistrar) ApplyOwnedMigrations(ctx context.Context, owner string, migrations []metadatamodulehost.SchemaMigration) error {
	return r.store.ApplyORMOwnedMigrations(ctx, owner, migrations)
}
