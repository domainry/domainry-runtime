package auditmodule

import (
	"context"

	"github.com/domainry/domainry-audit-sdk/modulehost"
	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	notificationmodulehost "github.com/domainry/domainry-notification-sdk/modulehost"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type Host struct {
	store   *database.RuntimeStore
	content sharedartifact.ContentStore
	writer  sharedartifact.ContentWriter
}

func NewHost(store *database.RuntimeStore, content sharedartifact.ContentStore, writer sharedartifact.ContentWriter) Host {
	return Host{store: store, content: content, writer: writer}
}
func (h Host) Database() modulehost.Database                       { return h.store.DB() }
func (h Host) Dialect() modulehost.Dialect                         { return h.store.RuntimeRenderer() }
func (h Host) Migrations() modulehost.MigrationRegistrar           { return migrationRegistrar{store: h.store} }
func (h Host) ArtifactContentStore() sharedartifact.ContentStore   { return h.content }
func (h Host) ArtifactContentWriter() sharedartifact.ContentWriter { return h.writer }

type migrationRegistrar struct{ store *database.RuntimeStore }

func (r migrationRegistrar) Driver() string { return r.store.Driver() }
func (r migrationRegistrar) Schema() string { return r.store.Schema() }
func (r migrationRegistrar) ApplyOwnedMigrations(ctx context.Context, owner string, migrations []modulehost.SchemaMigration) error {
	values := make([]notificationmodulehost.SchemaMigration, len(migrations))
	for index, migration := range migrations {
		values[index] = notificationmodulehost.SchemaMigration{Version: migration.Version, Name: migration.Name, Statements: append([]string(nil), migration.Statements...)}
		if migration.Baseline != nil {
			baseline := notificationmodulehost.SchemaBaseline{Tables: make([]notificationmodulehost.SchemaTable, len(migration.Baseline.Tables))}
			for tableIndex, table := range migration.Baseline.Tables {
				baseline.Tables[tableIndex] = notificationmodulehost.SchemaTable{Name: table.Name, Columns: make([]notificationmodulehost.SchemaColumn, len(table.Columns)), Indexes: make([]notificationmodulehost.SchemaIndex, len(table.Indexes))}
				for columnIndex, column := range table.Columns {
					baseline.Tables[tableIndex].Columns[columnIndex] = notificationmodulehost.SchemaColumn{Name: column.Name, Type: column.Type, Nullable: column.Nullable, PrimaryKey: column.PrimaryKey}
				}
				for indexIndex, item := range table.Indexes {
					baseline.Tables[tableIndex].Indexes[indexIndex] = notificationmodulehost.SchemaIndex{Name: item.Name, Unique: item.Unique, Columns: append([]string(nil), item.Columns...)}
				}
			}
			values[index].Baseline = &baseline
		}
	}
	return r.store.ApplyOwnedMigrations(ctx, owner, values)
}
