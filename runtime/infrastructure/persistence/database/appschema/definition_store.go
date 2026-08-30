package appschema

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/builder"
)

func (s ApplicationSchemaStore) ListApplicationDefinitions(ctx context.Context, resourceType string, workspaceID string) ([]appschemamodel.ApplicationDefinition, error) {
	if metadataModuleOwnsDefinition(resourceType) {
		repository, err := s.metadataModuleDefinitionStore()
		if err != nil {
			return nil, err
		}
		values, err := repository.ListDefinitionsWithExecutor(ctx, s.database(), resourceType, workspaceID)
		if err != nil {
			return nil, fmt.Errorf("list %s definitions: %w", resourceType, err)
		}
		out := make([]appschemamodel.ApplicationDefinition, len(values))
		for index, value := range values {
			out[index] = applicationDefinitionFromMetadata(value)
		}
		return out, nil
	}
	table, err := metadataDefinitionTable(resourceType)
	if err != nil {
		return nil, err
	}
	workspaceID = strings.TrimSpace(workspaceID)
	predicates := []ormbuilder.Predicate{ormbuilder.IsNull("disabled_at")}
	if workspaceID != "" {
		predicates = append(predicates, ormbuilder.Equal("source_id", workspaceID))
	}
	query, args, buildErr := ormbuilder.NewSelectBuilder(s.store.SQLRenderer, table).Columns("resource_key", "object_key", "name", "payload_json", "schema_version", "schema_hash", "source_kind", "source_id", "disabled_at", "created_at", "updated_at").Where(ormbuilder.And(predicates...)).OrderBy(ormbuilder.Ascending("resource_key")).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build %s definition list: %w", resourceType, buildErr)
	}
	rows, err := s.database().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list %s definitions: %w", resourceType, err)
	}
	defer rows.Close()
	var out []appschemamodel.ApplicationDefinition
	for rows.Next() {
		var d appschemamodel.ApplicationDefinition
		var payloadJSON string
		var disabledAt sql.NullString
		if err := rows.Scan(&d.ResourceKey, &d.ObjectKey, &d.Name, &payloadJSON, &d.SchemaVersion, &d.SchemaHash, &d.SourceKind, &d.SourceID, &disabledAt, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan %s definition: %w", resourceType, err)
		}
		d.ResourceType = resourceType
		d.Payload = json.RawMessage(payloadJSON)
		if disabledAt.Valid {
			d.DisabledAt = disabledAt.String
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetApplicationDefinition returns a single definition by type and key.
func (s ApplicationSchemaStore) GetApplicationDefinition(ctx context.Context, resourceType string, resourceKey string) (appschemamodel.ApplicationDefinition, bool, error) {
	if metadataModuleOwnsDefinition(resourceType) {
		repository, err := s.metadataModuleDefinitionStore()
		if err != nil {
			return appschemamodel.ApplicationDefinition{}, false, err
		}
		value, found, err := repository.GetDefinitionWithExecutor(ctx, s.database(), resourceType, resourceKey)
		if err != nil {
			return appschemamodel.ApplicationDefinition{}, false, fmt.Errorf("get %s definition: %w", resourceType, err)
		}
		if !found {
			return appschemamodel.ApplicationDefinition{}, false, nil
		}
		return applicationDefinitionFromMetadata(value), true, nil
	}
	table, err := metadataDefinitionTable(resourceType)
	if err != nil {
		return appschemamodel.ApplicationDefinition{}, false, err
	}
	query, args, buildErr := ormbuilder.NewSelectBuilder(s.store.SQLRenderer, table).Columns("resource_key", "object_key", "name", "payload_json", "schema_version", "schema_hash", "source_kind", "source_id", "disabled_at", "created_at", "updated_at").Where(ormbuilder.Equal("resource_key", resourceKey)).Build()
	if buildErr != nil {
		return appschemamodel.ApplicationDefinition{}, false, fmt.Errorf("build %s definition lookup: %w", resourceType, buildErr)
	}
	var d appschemamodel.ApplicationDefinition
	var payloadJSON string
	var disabledAt sql.NullString
	err = s.database().QueryRowContext(ctx, query, args...).Scan(&d.ResourceKey, &d.ObjectKey, &d.Name, &payloadJSON, &d.SchemaVersion, &d.SchemaHash, &d.SourceKind, &d.SourceID, &disabledAt, &d.CreatedAt, &d.UpdatedAt)
	if err == sql.ErrNoRows {
		return appschemamodel.ApplicationDefinition{}, false, nil
	}
	if err != nil {
		return appschemamodel.ApplicationDefinition{}, false, fmt.Errorf("get %s definition: %w", resourceType, err)
	}
	d.ResourceType = resourceType
	d.Payload = json.RawMessage(payloadJSON)
	if disabledAt.Valid {
		d.DisabledAt = disabledAt.String
	}
	return d, true, nil
}
