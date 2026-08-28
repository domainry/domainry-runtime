package metadata

import (
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"context"
	"encoding/json"
	"fmt"

	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"

	"strings"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
)

func (s MetadataStore) MetadataMigrationPlan(ctx context.Context, manifest manifestmodel.ManifestSchema) ([]metadatamodel.MetadataMigrationStep, error) {
	steps := []metadatamodel.MetadataMigrationStep{}
	for _, object := range manifest.Objects {
		table := strings.TrimSpace(object.Key)
		existingCols, err := s.tableColumns(ctx, table)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// Table does not exist yet.
			steps = append(steps, metadatamodel.MetadataMigrationStep{
				ObjectKey:   object.Key,
				Table:       table,
				Operation:   "create_table",
				Description: "backend.metadata.migration.createTable",
			})
			continue
		}
		for _, field := range object.Fields {
			col := strings.TrimSpace(field.Key)
			if col == "" || existingCols[col] {
				continue
			}
			steps = append(steps, metadatamodel.MetadataMigrationStep{
				ObjectKey:   object.Key,
				Table:       table,
				Operation:   "add_column",
				ColumnKey:   col,
				ColumnType:  s.metadataSQLTypeForField(field),
				Description: "backend.metadata.migration.addColumn",
			})
		}
	}
	return steps, nil
}

func (s MetadataStore) LoadManifestMetadata(ctx context.Context) (manifestmodel.ManifestSchema, error) {
	catalog, err := s.loadMetadataCatalog(ctx)
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	objects, err := loadMetadataSlice[definitionmodel.ObjectSchema](ctx, s, "object_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	fields, err := loadMetadataSlice[definitionmodel.FieldSchema](ctx, s, "field_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	validations, err := loadMetadataSlice[definitionmodel.ValidationSchema](ctx, s, "validation_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	views, err := loadMetadataSlice[definitionmodel.ViewSchema](ctx, s, "view_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	actions, err := loadMetadataSlice[definitionmodel.ActionSchema](ctx, s, "action_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	workflows, err := loadMetadataSlice[definitionmodel.WorkflowSchema](ctx, s, "workflow_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	schedulerDefinitions, err := loadMetadataSlice[map[string]any](ctx, s, "scheduler_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	automationRules, err := loadMetadataSlice[automationmodel.AutomationRuleSchema](ctx, s, "automation_rule_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	dictionaries, err := loadMetadataSlice[metadatamodel.DictionarySchema](ctx, s, "dictionary_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	connectors, err := loadMetadataSlice[integrationmodel.ConnectorSchema](ctx, s, "connector_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	eventMappings, err := loadMetadataSlice[integrationmodel.IntegrationEventMappingSchema](ctx, s, "integration_event_mapping_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	reports, err := loadMetadataSlice[reportmodel.ReportSchema](ctx, s, "report_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	operationStateExamples, err := loadMetadataSlice[reportmodel.ReportOperationStateExampleSchema](ctx, s, "operation_state_example_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	sensitiveFieldPolicies, err := loadMetadataSlice[reportmodel.ReportSensitiveFieldPolicySchema](ctx, s, "sensitive_field_policy_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	reportExportControls, err := loadMetadataSlice[reportmodel.ReportExportControlSchema](ctx, s, "report_export_control_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	entrypoints, err := loadMetadataSlice[definitionmodel.EntryPointSchema](ctx, s, "entrypoint_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	skills, err := loadMetadataSlice[agentmodel.SkillSchema](ctx, s, "skill_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	agents, err := loadMetadataSlice[agentmodel.AgentSchema](ctx, s, "agent_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	agentTasks, err := loadMetadataSlice[agentmodel.AgentTaskDefinition](ctx, s, "agent_task_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	agentEntrypoints, err := loadMetadataSlice[agentmodel.AgentEntrypointAssignment](ctx, s, "agent_entrypoint_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	agentServicePrincipals, err := loadMetadataSlice[agentmodel.AgentServicePrincipalBinding](ctx, s, "agent_service_principal_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	profileBindings, err := loadMetadataSlice[profilebindingmodel.Binding](ctx, s, "identity_profile_binding_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
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
	for index := range objects {
		objects[index].Fields = append([]definitionmodel.FieldSchema(nil), fieldsByObject[objects[index].Key]...)
		objects[index].Validations = append([]definitionmodel.ValidationSchema(nil), validationsByObject[objects[index].Key]...)
	}
	if len(objects) == 0 {
		return manifestmodel.ManifestSchema{}, fmt.Errorf("metadata DB has no object definitions")
	}
	return manifestmodel.ManifestSchema{
		TemplateID:                catalog["template_id"],
		Version:                   catalog["template_version"],
		DefaultLocale:             catalog["default_locale"],
		Name:                      catalog["name"],
		Objects:                   objects,
		Views:                     views,
		Actions:                   actions,
		Workflows:                 workflows,
		SchedulerDefinitions:      schedulerDefinitions,
		AutomationRules:           automationRules,
		Dictionaries:              dictionaries,
		Integrations:              integrationmodel.IntegrationSchema{Connectors: connectors, EventMappings: eventMappings},
		Reports:                   reports,
		OperationStateExamples:    operationStateExamples,
		SensitiveFieldPolicies:    sensitiveFieldPolicies,
		ReportExportControls:      reportExportControls,
		EntryPoints:               entrypoints,
		Skills:                    skills,
		Agents:                    agents,
		AgentTasks:                agentTasks,
		AgentEntrypoints:          agentEntrypoints,
		AgentServicePrincipals:    agentServicePrincipals,
		IdentityProfileExtensions: profileBindings,
	}, nil
}

func (s MetadataStore) loadMetadataCatalog(ctx context.Context) (map[string]string, error) {
	rows, err := s.database().QueryContext(ctx, "SELECT "+s.store.Identifier("key")+", "+s.store.Identifier("value")+" FROM "+s.store.TableIdentifier("metadata_catalog"))
	if err != nil {
		return nil, fmt.Errorf("load metadata catalog: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key string
		var value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan metadata catalog: %w", err)
		}
		out[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read metadata catalog: %w", err)
	}
	if strings.TrimSpace(out["template_id"]) == "" || strings.TrimSpace(out["template_version"]) == "" {
		return nil, fmt.Errorf("metadata catalog is missing template identity")
	}
	return out, nil
}

func loadMetadataSlice[T any](ctx context.Context, s MetadataStore, table string) ([]T, error) {
	rows, err := s.database().QueryContext(ctx, "SELECT "+s.store.Identifier("payload_json")+" FROM "+s.store.TableIdentifier(table)+" WHERE "+s.store.Identifier("disabled_at")+" IS NULL ORDER BY "+s.store.Identifier("resource_key")+" ASC")
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", table, err)
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan %s: %w", table, err)
		}
		var value T
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, fmt.Errorf("decode %s payload: %w", table, err)
		}
		out = append(out, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", table, err)
	}
	return out, nil
}
