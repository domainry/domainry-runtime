package testkit

import (
	"context"
	"time"

	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	reportsdk "github.com/domainry/domainry-report-sdk"
	reportmodulehost "github.com/domainry/domainry-report-sdk/modulehost"
	reportmodule "github.com/domainry/domainry-report/module"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func openTestkitReportBinding(ctx context.Context, store *database.RuntimeStore) (reportsdk.Binding, error) {
	host := testkitReportHost{store: store}
	return reportmodule.NewFactory().Open(ctx, reportsdk.ApplicationRef{RuntimeID: "runtime-testkit"}, host)
}

type testkitReportHost struct{ store *database.RuntimeStore }

type testkitReportApplicationHost struct {
	testkitReportHost
	ports     composition.ReportModuleApplicationPorts
	cursorKey []byte
}

func (h testkitReportApplicationHost) ReportSubjects() reportmodulehost.SubjectResolver {
	return h.ports.Subjects
}
func (h testkitReportApplicationHost) ReportObjectSQL() reportmodulehost.ObjectSQLExecutor {
	return h.ports.ObjectSQL
}
func (h testkitReportApplicationHost) ReportSourceVersions() reportmodulehost.SourceVersionReader {
	return h.ports.SourceVersions
}
func (h testkitReportApplicationHost) ReportExecutionAudit() reportmodulehost.ExecutionAudit {
	return h.ports.Audit
}
func (h testkitReportApplicationHost) ReportExportAuthorization() reportmodulehost.ExportAuthorization {
	return h.ports.Authorization
}
func (h testkitReportApplicationHost) ReportSnapshotTerminals() reportmodulehost.SnapshotTerminalCommitter {
	return h.ports.Terminals
}
func (h testkitReportApplicationHost) ReportExports() reportmodulehost.ExportGateway {
	return h.ports.Exports
}
func (h testkitReportApplicationHost) ReportCursorSigningKey() []byte {
	return append([]byte(nil), h.cursorKey...)
}
func (testkitReportApplicationHost) ReportClock() func() time.Time { return time.Now }

func (h testkitReportHost) Database() reportmodulehost.Database { return h.store.DB() }
func (h testkitReportHost) DatabaseFor(ctx context.Context) reportmodulehost.DBTX {
	if tx := database.ActionExecutionTransaction(ctx); tx != nil {
		return tx
	}
	return h.store.DB()
}
func (h testkitReportHost) Dialect() reportmodulehost.Dialect { return h.store.SQLRenderer }
func (h testkitReportHost) DefinitionStore() metadatasdk.DefinitionStore {
	if h.store == nil || h.store.Metadata() == nil {
		return nil
	}
	return h.store.Metadata().DefinitionStore()
}
func (h testkitReportHost) Migrations() reportmodulehost.MigrationRegistrar {
	return testkitReportMigrationRegistrar{store: h.store}
}

type testkitReportMigrationRegistrar struct{ store *database.RuntimeStore }

func (r testkitReportMigrationRegistrar) Driver() string { return r.store.Driver() }
func (r testkitReportMigrationRegistrar) Schema() string { return r.store.DatabaseSchema() }
func (r testkitReportMigrationRegistrar) ApplyOwnedMigrations(ctx context.Context, owner string, migrations []reportmodulehost.SchemaMigration) error {
	return r.store.ApplyORMOwnedMigrations(ctx, owner, migrations)
}
