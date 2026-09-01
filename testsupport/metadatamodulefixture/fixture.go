package metadatamodulefixture

import (
	"context"

	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	metadatamodulehost "github.com/domainry/domainry-metadata-sdk/modulehost"
	metadatamodule "github.com/domainry/domainry-metadata/module"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

// EnsureBinding equips an independently opened Runtime test store with the
// Metadata-owned persistence binding used by production composition.
func EnsureBinding(ctx context.Context, store *persistence.RuntimeStore) {
	if store == nil || store.Metadata() != nil {
		return
	}
	binding, err := metadatamodule.NewFactory().OpenModule(ctx, metadatasdk.ApplicationRef{InstallationID: "runtime-testkit"}, host{store: store})
	if err != nil {
		panic("open Runtime testkit Metadata module: " + err.Error())
	}
	if err := store.BindMetadata(binding); err != nil {
		_ = binding.Close(ctx)
		panic("bind Runtime testkit Metadata module: " + err.Error())
	}
}

type host struct{ store *persistence.RuntimeStore }

func (h host) Database() metadatamodulehost.Database { return h.store.DB() }
func (h host) Dialect() metadatamodulehost.Dialect   { return h.store.SQLRenderer }
func (h host) Migrations() metadatamodulehost.MigrationRegistrar {
	return migrations{store: h.store}
}

type migrations struct{ store *persistence.RuntimeStore }

func (m migrations) Driver() string { return m.store.Driver() }
func (m migrations) Schema() string { return m.store.DatabaseSchema() }
func (m migrations) ApplyOwnedMigrations(ctx context.Context, owner string, values []metadatamodulehost.SchemaMigration) error {
	return m.store.ApplyORMOwnedMigrations(ctx, owner, values)
}
