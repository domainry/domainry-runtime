package appschema

import (
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"context"
	"encoding/json"
	"fmt"

	ormbuilder "github.com/domainry/domainry-orm/query"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	"strings"
)

func (s ApplicationSchemaStore) ApplicationSchemaMigrationPlan(ctx context.Context, manifest manifestmodel.ManifestSchema) ([]appschemamodel.ApplicationSchemaMigrationStep, error) {
	steps := []appschemamodel.ApplicationSchemaMigrationStep{}
	for _, object := range manifest.Objects {
		table := strings.TrimSpace(object.Key)
		existingCols, err := s.tableColumns(ctx, table)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// Table does not exist yet.
			steps = append(steps, appschemamodel.ApplicationSchemaMigrationStep{
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
			steps = append(steps, appschemamodel.ApplicationSchemaMigrationStep{
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

func (s ApplicationSchemaStore) LoadManifestMetadata(ctx context.Context) (manifestmodel.ManifestSchema, error) {
	catalog, err := s.loadMetadataCatalog(ctx)
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	objects, fields, validations, actions, dictionaries, err := s.loadMetadataModuleDefinitions(ctx)
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	workflows, err := loadMetadataSlice[definitionmodel.WorkflowSchema](ctx, s, "_application_schema_workflow_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	automationRules, err := loadMetadataSlice[automationmodel.AutomationRuleSchema](ctx, s, "_application_schema_automation_rule_definitions")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	connectors, err := loadMetadataSlice[integrationmodel.ConnectorSchema](ctx, s, "_application_schema_connector_requirements")
	if err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	eventMappings, err := loadMetadataSlice[integrationmodel.IntegrationEventMappingSchema](ctx, s, "_application_schema_integration_event_mapping_requirements")
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
		TemplateID:      catalog["template_id"],
		Version:         catalog["template_version"],
		DefaultLocale:   catalog["default_locale"],
		Name:            catalog["name"],
		Objects:         objects,
		Actions:         actions,
		Workflows:       workflows,
		AutomationRules: automationRules,
		Dictionaries:    dictionaries,
		Integrations:    integrationmodel.IntegrationSchema{Connectors: connectors, EventMappings: eventMappings},
	}, nil
}

func (s ApplicationSchemaStore) loadMetadataCatalog(ctx context.Context) (map[string]string, error) {
	query, args, buildErr := ormbuilder.NewSelectBuilder(s.store.SQLRenderer, "_application_schema_projection").Columns("template_id", "artifact_version", "default_locale", "name", "contract_version", "schema_hash", "source_hash").Where(ormbuilder.Equal("id", "current")).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build metadata catalog load: %w", buildErr)
	}
	var templateID, artifactVersion, defaultLocale, name, contractVersion, schemaHash, sourceHash string
	if err := s.database().QueryRowContext(ctx, query, args...).Scan(&templateID, &artifactVersion, &defaultLocale, &name, &contractVersion, &schemaHash, &sourceHash); err != nil {
		return nil, fmt.Errorf("load metadata projection: %w", err)
	}
	out := map[string]string{"template_id": templateID, "template_version": artifactVersion, "default_locale": defaultLocale, "name": name, "schema_version": contractVersion, "schema_hash": schemaHash, "source_hash": sourceHash}
	if strings.TrimSpace(out["template_id"]) == "" || strings.TrimSpace(out["template_version"]) == "" {
		return nil, fmt.Errorf("metadata projection is missing template identity")
	}
	return out, nil
}

func loadMetadataSlice[T any](ctx context.Context, s ApplicationSchemaStore, table string) ([]T, error) {
	query, args, buildErr := ormbuilder.NewSelectBuilder(s.store.SQLRenderer, table).Columns("payload_json").Where(ormbuilder.IsNull("disabled_at")).OrderBy(ormbuilder.Ascending("resource_key")).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build %s load: %w", table, buildErr)
	}
	rows, err := s.database().QueryContext(ctx, query, args...)
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
