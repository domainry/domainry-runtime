package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	notificationmodulehost "github.com/domainry/domainry-notification-sdk/modulehost"
	reportsdk "github.com/domainry/domainry-report-sdk"
	reportmodulehost "github.com/domainry/domainry-report-sdk/modulehost"
	reportrepository "github.com/domainry/domainry-report-sdk/repository"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type runtimeReportModuleHost struct{ store *persistence.RuntimeStore }

func (h runtimeReportModuleHost) Database() reportmodulehost.Database { return h.store.DB() }
func (h runtimeReportModuleHost) Dialect() reportmodulehost.Dialect   { return h.store.SQLRenderer }
func (h runtimeReportModuleHost) Migrations() reportmodulehost.MigrationRegistrar {
	return runtimeReportMigrationRegistrar{store: h.store}
}

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
	repositories, ok := binding.(reportrepository.Binding)
	if !ok || repositories.DefinitionRepository() == nil {
		return fmt.Errorf("Report Binding returned no definition repository")
	}
	definitions := make([]reportrepository.Definition, 0, len(manifest.Reports)+len(manifest.OperationStateExamples)+len(manifest.SensitiveFieldPolicies)+len(manifest.ReportExportControls))
	appendDefinition := func(resourceType, key, objectKey, name string, value any) error {
		payload, err := json.Marshal(value)
		if err != nil {
			return err
		}
		definitions = append(definitions, reportrepository.Definition{ResourceType: resourceType, Key: strings.TrimSpace(key), ObjectKey: strings.TrimSpace(objectKey), Name: strings.TrimSpace(name), Payload: payload})
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
	return repositories.DefinitionRepository().SyncDefinitions(ctx, reportrepository.DefinitionSnapshot{SchemaVersion: version, SourceKind: "manifest", SourceID: sourceID, Definitions: definitions})
}
