package appschema

import (
	"context"
	"fmt"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-orm/query"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
)

func (s ApplicationSchemaStore) loadProjectedManifest(ctx context.Context) (manifestmodel.ManifestSchema, error) {
	catalog, err := s.loadMetadataCatalog(ctx)
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	definitions, err := s.metadataDefinitions()
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	snapshot, err := definitions.Snapshot(ctx)
	if err != nil {
		return manifestmodel.ManifestSchema{}, fmt.Errorf("load Metadata definition snapshot: %w", err)
	}
	result := manifestmodel.ManifestSchema{
		SchemaVersion: catalog["schema_version"], TemplateID: catalog["template_id"], Version: catalog["template_version"],
		DefaultLocale: catalog["default_locale"], Name: catalog["name"],
	}
	fields := []definitionmodel.FieldSchema{}
	validations := []definitionmodel.ValidationSchema{}
	for _, definition := range snapshot.Definitions {
		decode := func(target any) error {
			return decodeMetadataModuleDefinition(definition.ResourceKey, definition.Payload, target)
		}
		switch definition.ResourceType {
		case "object":
			var value definitionmodel.ObjectSchema
			if err := decode(&value); err != nil {
				return manifestmodel.ManifestSchema{}, err
			}
			result.Objects = append(result.Objects, value)
		case "field":
			var value definitionmodel.FieldSchema
			if err := decode(&value); err != nil {
				return manifestmodel.ManifestSchema{}, err
			}
			fields = append(fields, value)
		case "validation":
			var value definitionmodel.ValidationSchema
			if err := decode(&value); err != nil {
				return manifestmodel.ManifestSchema{}, err
			}
			validations = append(validations, value)
		case "action":
			var value definitionmodel.ActionSchema
			if err := decode(&value); err != nil {
				return manifestmodel.ManifestSchema{}, err
			}
			result.Actions = append(result.Actions, value)
		case "workflow":
			var value definitionmodel.WorkflowSchema
			if err := decode(&value); err != nil {
				return manifestmodel.ManifestSchema{}, err
			}
			result.Workflows = append(result.Workflows, value)
		case "automation_rule":
			var value automationmodel.AutomationRuleSchema
			if err := decode(&value); err != nil {
				return manifestmodel.ManifestSchema{}, err
			}
			result.AutomationRules = append(result.AutomationRules, value)
		case "dictionary":
			var value appschemamodel.DictionarySchema
			if err := decode(&value); err != nil {
				return manifestmodel.ManifestSchema{}, err
			}
			result.Dictionaries = append(result.Dictionaries, value)
		case "integration_event_mapping":
			var value appschemamodel.IntegrationEventMappingSchema
			if err := decode(&value); err != nil {
				return manifestmodel.ManifestSchema{}, err
			}
			result.Integrations.EventMappings = append(result.Integrations.EventMappings, value)
		case "report":
			var value reportmodel.ReportSchema
			if err := decode(&value); err != nil {
				return manifestmodel.ManifestSchema{}, err
			}
			result.Reports = append(result.Reports, value)
		case "operation_state_example":
			var value reportmodel.ReportOperationStateExampleSchema
			if err := decode(&value); err != nil {
				return manifestmodel.ManifestSchema{}, err
			}
			result.OperationStateExamples = append(result.OperationStateExamples, value)
		case "sensitive_field_policy":
			var value reportmodel.ReportSensitiveFieldPolicySchema
			if err := decode(&value); err != nil {
				return manifestmodel.ManifestSchema{}, err
			}
			result.SensitiveFieldPolicies = append(result.SensitiveFieldPolicies, value)
		case "report_export_control":
			var value reportmodel.ReportExportControlSchema
			if err := decode(&value); err != nil {
				return manifestmodel.ManifestSchema{}, err
			}
			result.ReportExportControls = append(result.ReportExportControls, value)
		case "identity_profile_binding":
			var value profilebindingmodel.Binding
			if err := decode(&value); err != nil {
				return manifestmodel.ManifestSchema{}, err
			}
			result.IdentityProfileExtensions = append(result.IdentityProfileExtensions, value)
		case "skill":
			var value agentsdk.SkillSchema
			if err := decode(&value); err != nil {
				return manifestmodel.ManifestSchema{}, err
			}
			result.Skills = append(result.Skills, value)
		case "agent":
			var value agentsdk.AgentSchema
			if err := decode(&value); err != nil {
				return manifestmodel.ManifestSchema{}, err
			}
			result.Agents = append(result.Agents, value)
		case "scheduler":
			var value map[string]any
			if err := decode(&value); err != nil {
				return manifestmodel.ManifestSchema{}, err
			}
			result.SchedulerDefinitions = append(result.SchedulerDefinitions, value)
		}
	}
	fieldsByObject := map[string][]definitionmodel.FieldSchema{}
	for _, field := range fields {
		objectKey := metadataFieldObjectKey(field)
		fieldsByObject[objectKey] = append(fieldsByObject[objectKey], field)
	}
	validationsByObject := map[string][]definitionmodel.ValidationSchema{}
	for _, validation := range validations {
		validationsByObject[validation.ObjectKey] = append(validationsByObject[validation.ObjectKey], validation)
	}
	for index := range result.Objects {
		objectKey := result.Objects[index].Key
		result.Objects[index].Fields = append([]definitionmodel.FieldSchema(nil), fieldsByObject[objectKey]...)
		result.Objects[index].Validations = append([]definitionmodel.ValidationSchema(nil), validationsByObject[objectKey]...)
	}
	if len(result.Objects) == 0 {
		return manifestmodel.ManifestSchema{}, fmt.Errorf("Metadata has no object definitions")
	}
	return result, nil
}

func (s ApplicationSchemaStore) loadMetadataCatalog(ctx context.Context) (map[string]string, error) {
	queryValue, args, err := query.NewSelectBuilder(s.store.SQLRenderer, "_application_schema_projection").
		Columns("template_id", "artifact_version", "default_locale", "name", "contract_version", "schema_hash", "source_hash").
		Where(query.Equal("id", "current")).Build()
	if err != nil {
		return nil, fmt.Errorf("build metadata catalog load: %w", err)
	}
	var templateID, artifactVersion, defaultLocale, name, contractVersion, schemaHash, sourceHash string
	if err := s.database().QueryRowContext(ctx, queryValue, args...).Scan(&templateID, &artifactVersion, &defaultLocale, &name, &contractVersion, &schemaHash, &sourceHash); err != nil {
		return nil, fmt.Errorf("load metadata projection: %w", err)
	}
	result := map[string]string{
		"template_id": templateID, "template_version": artifactVersion, "default_locale": defaultLocale,
		"name": name, "schema_version": contractVersion, "schema_hash": schemaHash, "source_hash": sourceHash,
	}
	if strings.TrimSpace(templateID) == "" || strings.TrimSpace(artifactVersion) == "" {
		return nil, fmt.Errorf("metadata projection is missing template identity")
	}
	return result, nil
}
