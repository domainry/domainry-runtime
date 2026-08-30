package appschema

import (
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"

	"sort"
	"strconv"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

// ApplicationSchemaStore is the request-aware storage boundary for metadata.
// Compatibility methods on Store remain available while callers migrate.
func (r ApplicationSchemaStore) LoadManifest(ctx context.Context, scope principalmodel.SystemScope) (manifestmodel.ManifestSchema, error) {
	if err := requireMetadataInstallationScope(scope); err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	catalog, err := r.loadCatalog(ctx)
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	objects, err := loadMetadataSliceContext[definitionmodel.ObjectSchema](ctx, r.database(), r.store, "object_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	fields, err := loadMetadataSliceContext[definitionmodel.FieldSchema](ctx, r.database(), r.store, "field_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	validations, err := loadMetadataSliceContext[definitionmodel.ValidationSchema](ctx, r.database(), r.store, "validation_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	views, err := loadMetadataSliceContext[definitionmodel.ViewSchema](ctx, r.database(), r.store, "view_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	actions, err := loadMetadataSliceContext[definitionmodel.ActionSchema](ctx, r.database(), r.store, "action_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	workflows, err := loadMetadataSliceContext[definitionmodel.WorkflowSchema](ctx, r.database(), r.store, "workflow_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	schedulerDefinitions, err := loadMetadataSliceContext[map[string]any](ctx, r.database(), r.store, "scheduler_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	automations, err := loadMetadataSliceContext[automationmodel.AutomationRuleSchema](ctx, r.database(), r.store, "automation_rule_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	dictionaries, err := loadMetadataSliceContext[appschemamodel.DictionarySchema](ctx, r.database(), r.store, "dictionary_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	connectors, err := loadMetadataSliceContext[integrationmodel.ConnectorSchema](ctx, r.database(), r.store, "connector_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	eventMappings, err := loadMetadataSliceContext[integrationmodel.IntegrationEventMappingSchema](ctx, r.database(), r.store, "integration_event_mapping_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	reports, err := loadMetadataSliceContext[reportmodel.ReportSchema](ctx, r.database(), r.store, "report_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	operationExamples, err := loadMetadataSliceContext[reportmodel.ReportOperationStateExampleSchema](ctx, r.database(), r.store, "operation_state_example_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	sensitivePolicies, err := loadMetadataSliceContext[reportmodel.ReportSensitiveFieldPolicySchema](ctx, r.database(), r.store, "sensitive_field_policy_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	exportControls, err := loadMetadataSliceContext[reportmodel.ReportExportControlSchema](ctx, r.database(), r.store, "report_export_control_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	entrypoints, err := loadMetadataSliceContext[definitionmodel.EntryPointSchema](ctx, r.database(), r.store, "entrypoint_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	skills, err := loadMetadataSliceContext[agentmodel.SkillSchema](ctx, r.database(), r.store, "skill_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	agents, err := loadMetadataSliceContext[agentmodel.AgentSchema](ctx, r.database(), r.store, "agent_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	agentTasks, err := loadMetadataSliceContext[agentmodel.AgentTaskDefinition](ctx, r.database(), r.store, "agent_task_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	agentEntrypoints, err := loadMetadataSliceContext[agentmodel.AgentEntrypointAssignment](ctx, r.database(), r.store, "agent_entrypoint_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	agentServicePrincipals, err := loadMetadataSliceContext[agentmodel.AgentServicePrincipalBinding](ctx, r.database(), r.store, "agent_service_principal_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	profileBindings, err := loadMetadataSliceContext[profilebindingmodel.Binding](ctx, r.database(), r.store, "identity_profile_binding_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	fieldsByObject := map[string][]definitionmodel.FieldSchema{}
	for _, field := range fields {
		fieldsByObject[metadataFieldObjectKey(field)] = append(fieldsByObject[metadataFieldObjectKey(field)], field)
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
		TemplateID: catalog["template_id"], Version: catalog["template_version"], DefaultLocale: catalog["default_locale"], Name: catalog["name"],
		Objects: objects, Views: views, Actions: actions, Workflows: workflows, SchedulerDefinitions: schedulerDefinitions, AutomationRules: automations,
		Dictionaries: dictionaries,
		Integrations: integrationmodel.IntegrationSchema{Connectors: connectors, EventMappings: eventMappings}, Reports: reports,
		OperationStateExamples: operationExamples, SensitiveFieldPolicies: sensitivePolicies, ReportExportControls: exportControls,
		EntryPoints: entrypoints, Skills: skills, Agents: agents, AgentTasks: agentTasks, AgentEntrypoints: agentEntrypoints, AgentServicePrincipals: agentServicePrincipals, IdentityProfileExtensions: profileBindings,
	}, nil
}

func (r ApplicationSchemaStore) loadCatalog(ctx context.Context) (map[string]string, error) {
	query, args, buildErr := ormbuilder.NewSelectBuilder(r.store.SQLRenderer, "metadata_catalog").Columns("key", "value").Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build metadata catalog load: %w", buildErr)
	}
	rows, err := r.database().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("load metadata catalog: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, value string
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

func loadMetadataSliceContext[T any](ctx context.Context, db *sql.DB, store *database.RuntimeStore, table string) ([]T, error) {
	query, args, buildErr := ormbuilder.NewSelectBuilder(store.SQLRenderer, table).Columns("payload_json").Where(ormbuilder.IsNull("disabled_at")).OrderBy(ormbuilder.Ascending("resource_key")).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build %s load: %w", table, buildErr)
	}
	rows, err := db.QueryContext(ctx, query, args...)
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

func (r ApplicationSchemaStore) ListDefinitions(ctx context.Context, scope principalmodel.SystemScope, resourceType string) ([]appschemamodel.ApplicationDefinition, error) {
	if err := requireMetadataInstallationScope(scope); err != nil {
		return nil, err
	}
	table, err := metadataDefinitionTable(resourceType)
	if err != nil {
		return nil, err
	}
	query, args, buildErr := ormbuilder.NewSelectBuilder(r.store.SQLRenderer, table).Columns("resource_key", "object_key", "name", "payload_json", "schema_version", "schema_hash", "source_kind", "source_id", "disabled_at", "created_at", "updated_at").Where(ormbuilder.IsNull("disabled_at")).OrderBy(ormbuilder.Ascending("resource_key")).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build %s definition list: %w", resourceType, buildErr)
	}
	rows, err := r.database().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list %s definitions: %w", resourceType, err)
	}
	defer rows.Close()
	out := []appschemamodel.ApplicationDefinition{}
	for rows.Next() {
		definition, err := scanApplicationDefinition(rows, resourceType)
		if err != nil {
			return nil, err
		}
		out = append(out, definition)
	}
	return out, rows.Err()
}

func (r ApplicationSchemaStore) GetDefinition(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string) (appschemamodel.ApplicationDefinition, bool, error) {
	if err := requireMetadataInstallationScope(scope); err != nil {
		return appschemamodel.ApplicationDefinition{}, false, err
	}
	table, err := metadataDefinitionTable(resourceType)
	if err != nil {
		return appschemamodel.ApplicationDefinition{}, false, err
	}
	query, args, buildErr := ormbuilder.NewSelectBuilder(r.store.SQLRenderer, table).Columns("resource_key", "object_key", "name", "payload_json", "schema_version", "schema_hash", "source_kind", "source_id", "disabled_at", "created_at", "updated_at").Where(ormbuilder.Equal("resource_key", resourceKey)).Build()
	if buildErr != nil {
		return appschemamodel.ApplicationDefinition{}, false, fmt.Errorf("build %s definition lookup: %w", resourceType, buildErr)
	}
	definition, err := scanApplicationDefinition(r.database().QueryRowContext(ctx, query, args...), resourceType)
	if err == sql.ErrNoRows {
		return appschemamodel.ApplicationDefinition{}, false, nil
	}
	return definition, err == nil, err
}

type metadataDefinitionScanner interface{ Scan(...any) error }

func scanApplicationDefinition(scanner metadataDefinitionScanner, resourceType string) (appschemamodel.ApplicationDefinition, error) {
	var definition appschemamodel.ApplicationDefinition
	var payload string
	var disabled sql.NullString
	if err := scanner.Scan(&definition.ResourceKey, &definition.ObjectKey, &definition.Name, &payload, &definition.SchemaVersion, &definition.SchemaHash, &definition.SourceKind, &definition.SourceID, &disabled, &definition.CreatedAt, &definition.UpdatedAt); err != nil {
		return appschemamodel.ApplicationDefinition{}, err
	}
	definition.ResourceType = resourceType
	definition.Payload = json.RawMessage(payload)
	if disabled.Valid {
		definition.DisabledAt = disabled.String
	}
	return definition, nil
}

func (r ApplicationSchemaStore) ListDefinitionVersions(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string) ([]appschemamodel.ApplicationDefinitionVersion, error) {
	if err := requireMetadataInstallationScope(scope); err != nil {
		return nil, err
	}
	query, args, buildErr := ormbuilder.NewSelectBuilder(r.store.SQLRenderer, "metadata_definition_versions").Columns("schema_version", "schema_hash", "payload_json", "created_at").Where(ormbuilder.And(ormbuilder.Equal("resource_type", resourceType), ormbuilder.Equal("resource_key", resourceKey))).OrderBy(ormbuilder.Descending("created_at")).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build versions %s %s: %w", resourceType, resourceKey, buildErr)
	}
	rows, err := r.database().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list versions %s %s: %w", resourceType, resourceKey, err)
	}
	defer rows.Close()
	out := []appschemamodel.ApplicationDefinitionVersion{}
	for rows.Next() {
		var version appschemamodel.ApplicationDefinitionVersion
		var payload string
		if err := rows.Scan(&version.SchemaVersion, &version.SchemaHash, &payload, &version.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan version: %w", err)
		}
		version.ResourceType, version.ResourceKey, version.Payload = resourceType, resourceKey, json.RawMessage(payload)
		out = append(out, version)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		left, leftErr := strconv.Atoi(strings.TrimSpace(out[i].SchemaVersion))
		right, rightErr := strconv.Atoi(strings.TrimSpace(out[j].SchemaVersion))
		if leftErr == nil && rightErr == nil && left != right {
			return left > right
		}
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt > out[j].CreatedAt
		}
		return out[i].SchemaVersion > out[j].SchemaVersion
	})
	return out, nil
}

func (r ApplicationSchemaStore) metadataDefinitionReplay(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey, targetHash string, expectedHash *string) (appschemamodel.ApplicationDefinition, bool, error) {
	current, found, err := r.GetDefinition(ctx, scope, resourceType, resourceKey)
	if err != nil {
		return appschemamodel.ApplicationDefinition{}, false, err
	}
	if !found {
		if expectedHash != nil && strings.TrimSpace(*expectedHash) != "" {
			return appschemamodel.ApplicationDefinition{}, false, &appschemamodel.ApplicationDefinitionConflictError{ResourceType: resourceType, ResourceKey: resourceKey, ExpectedHash: strings.TrimSpace(*expectedHash)}
		}
		return appschemamodel.ApplicationDefinition{}, false, nil
	}
	expected := ""
	if expectedHash != nil {
		expected = strings.TrimSpace(*expectedHash)
	}
	if current.SchemaHash != targetHash {
		if expectedHash != nil && current.SchemaHash != expected {
			return appschemamodel.ApplicationDefinition{}, false, &appschemamodel.ApplicationDefinitionConflictError{ResourceType: resourceType, ResourceKey: resourceKey, ExpectedHash: expected, CurrentHash: current.SchemaHash}
		}
		return appschemamodel.ApplicationDefinition{}, false, nil
	}
	if expectedHash == nil || expected == current.SchemaHash {
		return current, true, nil
	}
	if expected == "" && strings.TrimSpace(current.SchemaVersion) == "1" {
		return current, true, nil
	}
	versions, err := r.ListDefinitionVersions(ctx, scope, resourceType, resourceKey)
	if err != nil {
		return appschemamodel.ApplicationDefinition{}, false, err
	}
	for index, version := range versions {
		if version.SchemaVersion == current.SchemaVersion && version.SchemaHash == current.SchemaHash && index+1 < len(versions) && versions[index+1].SchemaHash == expected {
			return current, true, nil
		}
	}
	return appschemamodel.ApplicationDefinition{}, false, &appschemamodel.ApplicationDefinitionConflictError{ResourceType: resourceType, ResourceKey: resourceKey, ExpectedHash: expected, CurrentHash: current.SchemaHash}
}
