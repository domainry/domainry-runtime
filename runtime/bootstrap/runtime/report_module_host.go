package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	notificationmodulehost "github.com/domainry/domainry-notification-sdk/modulehost"
	reportsdk "github.com/domainry/domainry-report-sdk"
	reportmodulehost "github.com/domainry/domainry-report-sdk/modulehost"
	reportpersistence "github.com/domainry/domainry-report-sdk/persistence"
	"github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
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
func (h runtimeReportApplicationHost) ReportDatasets() reportmodulehost.DatasetReader {
	return h.ports.Datasets
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

func synchronizeReportDefinitions(ctx context.Context, binding reportsdk.Binding, manifest manifestmodel.ManifestSchema) error {
	repository := binding.Definitions()
	if repository == nil {
		return fmt.Errorf("Report Binding returned no definition repository")
	}
	definitions := make([]reportpersistence.Definition, 0, len(manifest.Reports)+len(manifest.OperationStateExamples)+len(manifest.SensitiveFieldPolicies)+len(manifest.ReportExportControls))
	appendDefinition := func(resourceType, key, objectKey, name string, value any) error {
		payload, err := json.Marshal(value)
		if err != nil {
			return err
		}
		definitions = append(definitions, reportpersistence.Definition{ResourceType: resourceType, Key: strings.TrimSpace(key), ObjectKey: strings.TrimSpace(objectKey), Name: strings.TrimSpace(name), Payload: payload})
		return nil
	}
	for _, value := range manifest.Reports {
		if err := appendDefinition("report", value.Key, "", value.Name, value); err != nil {
			return err
		}
	}
	for _, value := range manifest.OperationStateExamples {
		if err := appendDefinition("operation_state_example", value.Key, value.ObjectKey, value.Name, value); err != nil {
			return err
		}
	}
	for _, value := range manifest.SensitiveFieldPolicies {
		if err := appendDefinition("sensitive_field_policy", value.Key, value.ObjectKey, value.Name, value); err != nil {
			return err
		}
	}
	for _, value := range manifest.ReportExportControls {
		if err := appendDefinition("report_export_control", value.Key, value.ReportKey, value.Name, value); err != nil {
			return err
		}
	}
	version := strings.TrimSpace(manifest.Version)
	if version == "" {
		version = "1"
	}
	sourceID := strings.TrimSpace(manifest.TemplateID)
	if sourceID == "" {
		sourceID = "generated-template"
	}
	return repository.SyncDefinitions(ctx, reportpersistence.DefinitionSnapshot{SchemaVersion: version, SourceKind: "manifest", SourceID: sourceID, Definitions: definitions})
}
