package runtime

import (
	"context"
	"time"

	notificationmodulehost "github.com/domainry/domainry-notification-sdk/modulehost"
	reportsdk "github.com/domainry/domainry-report-sdk"
	reportmodulehost "github.com/domainry/domainry-report-sdk/modulehost"
	"github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	runtimereportmodulehost "github.com/domainry/domainry-runtime/runtime/modulehost/report"
)

type runtimeReportModuleHost struct{ store *persistence.RuntimeStore }

func (h runtimeReportModuleHost) Database() reportmodulehost.Database { return h.store.DB() }
func (h runtimeReportModuleHost) DatabaseFor(ctx context.Context) reportmodulehost.DBTX {
	if tx := persistence.ActionExecutionTransaction(ctx); tx != nil {
		return tx
	}
	return h.store.DB()
}
func (h runtimeReportModuleHost) Dialect() reportmodulehost.Dialect { return h.store.SQLRenderer }
func (h runtimeReportModuleHost) Migrations() reportmodulehost.MigrationRegistrar {
	return runtimeReportMigrationRegistrar{store: h.store}
}

type runtimeReportApplicationHost struct {
	runtimeReportModuleHost
	ports     composition.ReportModuleApplicationPorts
	cursorKey []byte
}

func (h runtimeReportApplicationHost) ReportSubjects() reportmodulehost.SubjectResolver {
	return h.ports.Subjects
}
func (h runtimeReportApplicationHost) ReportObjectSQL() reportmodulehost.ObjectSQLExecutor {
	return h.ports.ObjectSQL
}
func (h runtimeReportApplicationHost) ReportSourceVersions() reportmodulehost.SourceVersionReader {
	return h.ports.SourceVersions
}
func (h runtimeReportApplicationHost) ReportExecutionAudit() reportmodulehost.ExecutionAudit {
	return h.ports.Audit
}
func (h runtimeReportApplicationHost) ReportExportAuthorization() reportmodulehost.ExportAuthorization {
	return h.ports.Authorization
}
func (h runtimeReportApplicationHost) ReportSnapshotTerminals() reportmodulehost.SnapshotTerminalCommitter {
	return h.ports.Terminals
}
func (h runtimeReportApplicationHost) ReportExports() reportmodulehost.ExportGateway {
	return h.ports.Exports
}
func (h runtimeReportApplicationHost) ReportAnalysisTables() reportmodulehost.AnalysisTableSource {
	return h.ports.Tables
}
func (h runtimeReportApplicationHost) ReportCursorSigningKey() []byte {
	return append([]byte(nil), h.cursorKey...)
}
func (runtimeReportApplicationHost) ReportClock() func() time.Time { return time.Now }

type runtimeReportMigrationRegistrar struct{ store *persistence.RuntimeStore }

func (r runtimeReportMigrationRegistrar) Driver() string { return r.store.Driver() }
func (r runtimeReportMigrationRegistrar) Schema() string { return r.store.DatabaseSchema() }
func (r runtimeReportMigrationRegistrar) ApplyOwnedMigrations(ctx context.Context, owner string, migrations []reportmodulehost.SchemaMigration) error {
	values := make([]notificationmodulehost.SchemaMigration, len(migrations))
	for index, migration := range migrations {
		values[index] = notificationmodulehost.SchemaMigration{Version: migration.Version, Name: migration.Name, Statements: append([]string(nil), migration.Statements...)}
	}
	return r.store.ApplyOwnedMigrations(ctx, owner, values)
}

// SynchronizeReportDefinitions projects Runtime manifest definitions into the
// Report-owned repository before the Report application host is bound.
func SynchronizeReportDefinitions(ctx context.Context, binding reportsdk.Binding, manifest manifestmodel.ManifestSchema) error {
	return runtimereportmodulehost.SynchronizeDefinitions(ctx, binding, manifest)
}
