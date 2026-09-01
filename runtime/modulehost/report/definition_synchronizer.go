package reportmodulehost

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	reportsdk "github.com/domainry/domainry-report-sdk"
	reportpersistence "github.com/domainry/domainry-report-sdk/persistence"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

// SynchronizeDefinitions projects Runtime manifest definitions into the
// Report-owned repository without coupling callers to Runtime bootstrap.
func SynchronizeDefinitions(ctx context.Context, binding reportsdk.Binding, manifest manifestmodel.ManifestSchema) error {
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
