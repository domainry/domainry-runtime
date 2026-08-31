package appschema

import (
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"strings"

	"github.com/domainry/domainry-orm/query"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func (r ApplicationSchemaStore) LoadManifest(ctx context.Context, scope principalmodel.SystemScope) (manifestmodel.ManifestSchema, error) {
	if err := requireMetadataInstallationScope(scope); err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	catalog, err := r.loadCatalog(ctx)
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	objects, fields, validations, actions, dictionaries, err := r.loadMetadataModuleDefinitions(ctx)
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	workflows, err := loadMetadataSliceContext[definitionmodel.WorkflowSchema](ctx, r.database(), r.store, "_application_schema_workflow_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	automations, err := loadMetadataSliceContext[automationmodel.AutomationRuleSchema](ctx, r.database(), r.store, "_application_schema_automation_rule_definitions")
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
		Objects: objects, Actions: actions, Workflows: workflows, AutomationRules: automations,
		Dictionaries: dictionaries,
	}, nil
}

func (r ApplicationSchemaStore) loadCatalog(ctx context.Context) (map[string]string, error) {
	queryValue, args, buildErr := query.NewSelectBuilder(r.store.SQLRenderer, "_application_schema_projection").Columns("template_id", "artifact_version", "default_locale", "name", "contract_version", "schema_hash", "source_hash").Where(query.Equal("id", "current")).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build metadata catalog load: %w", buildErr)
	}
	var templateID, artifactVersion, defaultLocale, name, contractVersion, schemaHash, sourceHash string
	if err := r.database().QueryRowContext(ctx, queryValue, args...).Scan(&templateID, &artifactVersion, &defaultLocale, &name, &contractVersion, &schemaHash, &sourceHash); err != nil {
		return nil, fmt.Errorf("load metadata projection: %w", err)
	}
	out := map[string]string{"template_id": templateID, "template_version": artifactVersion, "default_locale": defaultLocale, "name": name, "schema_version": contractVersion, "schema_hash": schemaHash, "source_hash": sourceHash}
	if strings.TrimSpace(out["template_id"]) == "" || strings.TrimSpace(out["template_version"]) == "" {
		return nil, fmt.Errorf("metadata projection is missing template identity")
	}
	return out, nil
}

func loadMetadataSliceContext[T any](ctx context.Context, db *sql.DB, store *database.RuntimeStore, table string) ([]T, error) {
	queryValue, args, buildErr := query.NewSelectBuilder(store.SQLRenderer, table).Columns("payload_json").Where(query.IsNull("disabled_at")).OrderBy(query.Ascending("resource_key")).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build %s load: %w", table, buildErr)
	}
	rows, err := db.QueryContext(ctx, queryValue, args...)
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
	if metadataModuleOwnsDefinition(resourceType) {
		return r.ListApplicationDefinitions(ctx, resourceType, "")
	}
	table, err := metadataDefinitionTable(resourceType)
	if err != nil {
		return nil, err
	}
	queryValue, args, buildErr := query.NewSelectBuilder(r.store.SQLRenderer, table).Columns("resource_key", "object_key", "name", "payload_json", "schema_version", "schema_hash", "source_kind", "source_id", "disabled_at", "created_at", "updated_at").Where(query.IsNull("disabled_at")).OrderBy(query.Ascending("resource_key")).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build %s definition list: %w", resourceType, buildErr)
	}
	rows, err := r.database().QueryContext(ctx, queryValue, args...)
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
	if metadataModuleOwnsDefinition(resourceType) {
		return r.GetApplicationDefinition(ctx, resourceType, resourceKey)
	}
	table, err := metadataDefinitionTable(resourceType)
	if err != nil {
		return appschemamodel.ApplicationDefinition{}, false, err
	}
	queryValue, args, buildErr := query.NewSelectBuilder(r.store.SQLRenderer, table).Columns("resource_key", "object_key", "name", "payload_json", "schema_version", "schema_hash", "source_kind", "source_id", "disabled_at", "created_at", "updated_at").Where(query.Equal("resource_key", resourceKey)).Build()
	if buildErr != nil {
		return appschemamodel.ApplicationDefinition{}, false, fmt.Errorf("build %s definition lookup: %w", resourceType, buildErr)
	}
	definition, err := scanApplicationDefinition(r.database().QueryRowContext(ctx, queryValue, args...), resourceType)
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
