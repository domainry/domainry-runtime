package appschema

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	"github.com/domainry/domainry-orm/query"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	projectmodel "github.com/domainry/domainry-runtime/runtime/domain/project/model"
)

// ProjectModelMatches is the restart fast path for the single model.json
// contract. There is no upgrade, migration-plan, or previous-model fallback.
func (s ApplicationSchemaStore) ProjectModelMatches(ctx context.Context, scope principalmodel.SystemScope, model projectmodel.RuntimeModel) (bool, error) {
	if err := requireMetadataInstallationScope(scope); err != nil {
		return false, err
	}
	statement, args, err := query.NewSelectBuilder(s.store.SQLRenderer, "_project_model_state").
		Columns("model_hash", "initialized_at").Where(query.Equal("id", "current")).Build()
	if err != nil {
		return false, fmt.Errorf("build project model lookup: %w", err)
	}
	var installedHash, initializedAt string
	err = s.database().QueryRowContext(ctx, statement, args...).Scan(&installedHash, &initializedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load project model identity: %w", err)
	}
	if strings.TrimSpace(installedHash) == "" {
		return false, nil
	}
	if strings.TrimSpace(installedHash) != strings.TrimSpace(model.ContentHash) {
		return false, &apperror.AppError{
			Kind: apperror.KindConflict,
			Code: projectmodel.ChangedRequiresEmptyDatabaseCode,
			Params: map[string]string{
				"installed_model_hash": strings.TrimSpace(installedHash),
				"requested_model_hash": strings.TrimSpace(model.ContentHash),
			},
		}
	}
	return strings.TrimSpace(initializedAt) != "", nil
}

// InitializeProjectModel materializes storage and Metadata exactly once. A
// restart with the same hash is a no-op; a different hash is rejected.
func (s ApplicationSchemaStore) InitializeProjectModel(ctx context.Context, scope principalmodel.SystemScope, model projectmodel.RuntimeModel) error {
	if err := requireMetadataInstallationScope(scope); err != nil {
		return err
	}
	if strings.TrimSpace(model.ContentHash) == "" || len(model.Objects) == 0 {
		return fmt.Errorf("project model requires a content hash and at least one object")
	}
	matched, err := s.ProjectModelMatches(ctx, scope, model)
	if err != nil || matched {
		return err
	}
	if err := s.MaterializeProjectObjects(ctx, scope, model); err != nil {
		return err
	}
	if err := s.syncProjectModelDefinitions(ctx, model); err != nil {
		return err
	}
	return s.recordProjectModelProjection(ctx, model)
}

// MaterializeProjectObjects creates only physical object storage. The public
// host uses it before Identity creates the first Workspace; the full model
// identity is recorded later after Metadata is opened by Runtime startup.
func (s ApplicationSchemaStore) MaterializeProjectObjects(ctx context.Context, scope principalmodel.SystemScope, model projectmodel.RuntimeModel) error {
	if err := requireMetadataInstallationScope(scope); err != nil {
		return err
	}
	physicalSchema, err := s.loadPhysicalSchemaSnapshot(ctx, model.Objects)
	if err != nil {
		return err
	}
	for _, object := range model.Objects {
		if strings.TrimSpace(object.Key) == "" {
			continue
		}
		var columns map[string]string
		var indexes map[string]bool
		if physicalSchema != nil {
			columns = physicalSchema.ColumnsByTable[strings.TrimSpace(object.Key)]
			indexes = physicalSchema.IndexesByTable[strings.TrimSpace(object.Key)]
		}
		if err := s.ensureObjectStorageWithPhysicalSchema(ctx, object, columns, indexes); err != nil {
			return fmt.Errorf("initialize project object %s: %w", object.Key, err)
		}
	}
	return nil
}

func (s ApplicationSchemaStore) syncProjectModelDefinitions(ctx context.Context, model projectmodel.RuntimeModel) error {
	if s.metadata == nil || s.metadata.DefinitionStore() == nil {
		return fmt.Errorf("Metadata Definition store is unavailable")
	}
	metadataDefinitions := []metadatasdk.Definition{}
	identityDefinitions := []metadatasdk.Definition{}
	appendDefinition := func(target *[]metadatasdk.Definition, resourceType, key, objectKey, name string, value any) error {
		payload, err := encodeMetadataModuleDefinition(value)
		if err != nil {
			return err
		}
		*target = append(*target, metadatasdk.Definition{ResourceType: resourceType, ResourceKey: strings.TrimSpace(key), ObjectKey: strings.TrimSpace(objectKey), Name: strings.TrimSpace(name), Payload: payload})
		return nil
	}
	for _, object := range model.Objects {
		objectCopy := object
		objectCopy.Fields, objectCopy.Validations = nil, nil
		if err := appendDefinition(&metadataDefinitions, "object", object.Key, object.Key, object.Name, objectCopy); err != nil {
			return err
		}
		for _, field := range object.Fields {
			fieldCopy := field
			fieldCopy.Config = cloneMetadataModuleConfig(field.Config)
			fieldCopy.Config["_definition_object_key"] = object.Key
			if err := appendDefinition(&metadataDefinitions, "field", metadataJoinedKey(object.Key, field.Key), object.Key, field.Name, fieldCopy); err != nil {
				return err
			}
		}
		for index, validation := range object.Validations {
			if strings.TrimSpace(validation.ObjectKey) == "" {
				validation.ObjectKey = object.Key
			}
			key := strings.TrimSpace(validation.Key)
			if key == "" {
				key = validationMetadataKey(object.Key, index, validation)
			}
			if err := appendDefinition(&metadataDefinitions, "validation", key, validation.ObjectKey, validation.Message, validation); err != nil {
				return err
			}
		}
	}
	for _, binding := range model.IdentityProfiles {
		if err := appendDefinition(&identityDefinitions, "identity_profile_binding", binding.ObjectKey, binding.ObjectKey, binding.BusinessIdentity.Key, binding); err != nil {
			return err
		}
	}
	if err := s.metadata.DefinitionStore().ReplaceSourceSnapshot(ctx, metadatasdk.ProjectionSnapshot{
		Owner:         metadatasdk.DefinitionOwnerMetadata,
		SchemaVersion: model.SchemaVersion, SourceKind: "project_model", SourceID: model.ProjectKey,
		Name: strings.TrimSpace(model.ProjectName), DefaultLocale: projectModelDefaultLocale(model), Definitions: metadataDefinitions,
	}); err != nil {
		return err
	}
	return s.metadata.DefinitionStore().ReplaceSourceSnapshot(ctx, metadatasdk.ProjectionSnapshot{
		Owner:         metadatasdk.DefinitionOwnerIdentity,
		SchemaVersion: model.SchemaVersion, SourceKind: "project_model", SourceID: model.ProjectKey,
		Definitions: identityDefinitions,
	})
}

func (s ApplicationSchemaStore) recordProjectModelProjection(ctx context.Context, model projectmodel.RuntimeModel) error {
	now := time.Now().UTC().Format(time.RFC3339)
	columns := []string{"id", "schema_version", "model_hash", "catalog_hash", "project_key", "default_locale", "time_zone", "name", "initialized_at", "catalog_updated_at"}
	values := []any{"current", model.SchemaVersion, model.ContentHash, model.ContentHash, model.ProjectKey, projectModelDefaultLocale(model), model.EffectiveTimeZone(), model.ProjectName, now, now}
	insert := query.NewInsertBuilder(s.store.SQLRenderer, "_project_model_state").Columns(columns...).Values(values...)
	assignments := make([]query.Assignment, 0, len(columns)-1)
	for _, column := range columns[1:] {
		assignments = append(assignments, query.AssignExpression(column, query.InsertedValue(column)))
	}
	var err error
	insert, err = s.store.Engine.ApplyUpsert(insert, []string{"id"}, assignments...)
	if err != nil {
		return fmt.Errorf("build project model projection upsert: %w", err)
	}
	statement, args, err := insert.Build()
	if err != nil {
		return fmt.Errorf("build project model projection: %w", err)
	}
	if _, err := s.database().ExecContext(ctx, statement, args...); err != nil {
		return fmt.Errorf("record project model projection: %w", err)
	}
	return nil
}

func encodeMetadataModuleDefinition(value any) ([]byte, error) {
	return jsonMarshal(value)
}

func cloneMetadataModuleConfig(value map[string]any) map[string]any {
	result := make(map[string]any, len(value)+1)
	for key, item := range value {
		result[key] = item
	}
	return result
}

// indirection keeps the projection encoder replaceable in tests without
// exposing a JSON-shaped project contract.
var jsonMarshal = func(value any) ([]byte, error) {
	return json.Marshal(value)
}

func projectModelDefaultLocale(model projectmodel.RuntimeModel) string {
	if locale := strings.TrimSpace(model.DefaultLocale); locale != "" {
		return locale
	}
	return "en-US"
}
