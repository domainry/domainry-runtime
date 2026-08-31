package runtime

import (
	"context"
	"database/sql"
	"strings"

	auditcontract "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/modulehttp"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	notificationmodulehost "github.com/domainry/domainry-notification-sdk/modulehost"
	partymodulehost "github.com/domainry/domainry-party-sdk/modulehost"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type partySDKModuleHost struct {
	store     *persistence.RuntimeStore
	directory identitysdk.Directory
	audit     auditcontract.Appender
}

func (h partySDKModuleHost) Database() *sql.DB { return h.store.DB() }
func (h partySDKModuleHost) Driver() string    { return h.store.Driver() }
func (h partySDKModuleHost) Schema() string    { return h.store.DatabaseSchema() }
func (h partySDKModuleHost) Migrations() partymodulehost.MigrationRegistrar {
	return partySDKMigrationRegistrar{h.store}
}
func (h partySDKModuleHost) WorkforceProfiles() partymodulehost.WorkforceProfileDirectory {
	return partySDKWorkforceProfiles{h.directory}
}
func (h partySDKModuleHost) Audit() modulehttp.AuditRecorder {
	return partySDKAuditRecorder{h.audit}
}

type partySDKAuditRecorder struct{ appender auditcontract.Appender }

func (r partySDKAuditRecorder) Record(ctx context.Context, event modulehttp.AuditEvent) error {
	_, err := r.appender.Append(ctx, auditcontract.AppendRequest{
		Event: event.Event, ObjectKey: event.ObjectKey, RecordID: event.RecordID,
		Actor:   auditcontract.Actor{WorkspaceID: event.WorkspaceID, SubjectID: event.ActorID, RoleKey: event.RoleKey, Kind: "user"},
		Summary: event.Summary, Metadata: event.Metadata,
	})
	return err
}

type partySDKMigrationRegistrar struct{ store *persistence.RuntimeStore }

func (r partySDKMigrationRegistrar) ApplyOwnedMigrations(ctx context.Context, owner string, migrations []partymodulehost.SchemaMigration) error {
	values := make([]notificationmodulehost.SchemaMigration, len(migrations))
	for i, m := range migrations {
		values[i] = notificationmodulehost.SchemaMigration{Version: uint(m.Version), Name: m.Name, Statements: append([]string(nil), m.Statements...)}
		if m.Baseline != nil {
			baseline := notificationmodulehost.SchemaBaseline{Tables: make([]notificationmodulehost.SchemaTable, len(m.Baseline.Tables))}
			for tableIndex, table := range m.Baseline.Tables {
				baseline.Tables[tableIndex] = notificationmodulehost.SchemaTable{Name: table.Name, Columns: make([]notificationmodulehost.SchemaColumn, len(table.Columns)), Indexes: make([]notificationmodulehost.SchemaIndex, len(table.Indexes))}
				for columnIndex, column := range table.Columns {
					baseline.Tables[tableIndex].Columns[columnIndex] = notificationmodulehost.SchemaColumn{Name: column.Name, Type: column.Type, Nullable: column.Nullable, PrimaryKey: column.PrimaryKey}
				}
				for indexIndex, index := range table.Indexes {
					baseline.Tables[tableIndex].Indexes[indexIndex] = notificationmodulehost.SchemaIndex{Name: index.Name, Unique: index.Unique, Columns: append([]string(nil), index.Columns...)}
				}
			}
			values[i].Baseline = &baseline
		}
	}
	return r.store.ApplyOwnedMigrations(ctx, owner, values)
}

type partySDKWorkforceProfiles struct{ directory identitysdk.Directory }

func (d partySDKWorkforceProfiles) Exists(ctx context.Context, workspaceID, id string) (bool, error) {
	if d.directory == nil {
		return false, nil
	}
	values, err := d.directory.ListWorkforce(requestcontext.WithWorkspaceID(ctx, strings.TrimSpace(workspaceID)), identitysdk.DirectoryQuery{})
	if err != nil {
		return false, err
	}
	id = strings.TrimSpace(id)
	for _, value := range values {
		if strings.TrimSpace(value.WorkforceProfileID) == id {
			return true, nil
		}
	}
	return false, nil
}
