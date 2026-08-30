package appschema

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	metadatarepository "github.com/domainry/domainry-metadata-sdk/repository"
	ormbuilder "github.com/domainry/domainry-orm/builder"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func (s ApplicationSchemaStore) syncMetadataModuleDefinitions(ctx context.Context, manifest manifestmodel.ManifestSchema) error {
	repository := s.metadataDefinitions
	if repository == nil && s.store != nil {
		repository = s.store.MetadataDefinitions()
	}
	if repository == nil {
		return fmt.Errorf("Metadata definition repository is unavailable")
	}
	definitions := []metadatarepository.Definition{}
	appendDefinition := func(resourceType, key, objectKey, name string, value any) error {
		payload, err := json.Marshal(value)
		if err != nil {
			return err
		}
		definitions = append(definitions, metadatarepository.Definition{ResourceType: resourceType, Key: strings.TrimSpace(key), ObjectKey: strings.TrimSpace(objectKey), Name: strings.TrimSpace(name), Payload: payload})
		return nil
	}
	for _, object := range manifest.Objects {
		objectCopy := object
		objectCopy.Fields, objectCopy.Validations = nil, nil
		if err := appendDefinition("object", object.Key, object.Key, object.Name, objectCopy); err != nil {
			return err
		}
		for _, field := range object.Fields {
			fieldCopy := field
			fieldCopy.Config = cloneMetadataModuleConfig(field.Config)
			fieldCopy.Config["_definition_object_key"] = object.Key
			if err := appendDefinition("field", metadataJoinedKey(object.Key, field.Key), object.Key, field.Name, fieldCopy); err != nil {
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
			if err := appendDefinition("validation", key, validation.ObjectKey, validation.Message, validation); err != nil {
				return err
			}
		}
	}
	for _, action := range manifest.Actions {
		if err := appendDefinition("action", action.Key, action.ObjectKey, action.Label, action); err != nil {
			return err
		}
	}
	for _, dictionary := range manifest.Dictionaries {
		if err := appendDefinition("dictionary", dictionary.Key, "", dictionary.Name, dictionary); err != nil {
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
	if err := repository.SyncDefinitions(ctx, metadatarepository.Snapshot{SchemaVersion: version, SourceKind: "generated", SourceID: sourceID, Definitions: definitions}); err != nil {
		return err
	}
	return s.purgeRetiredMetadataProjectionRows(ctx, "generated", sourceID)
}

func (s ApplicationSchemaStore) purgeRetiredMetadataProjectionRows(ctx context.Context, sourceKind, sourceID string) error {
	for _, table := range []string{"object_definitions", "field_definitions", "validation_definitions", "action_definitions", "dictionary_definitions"} {
		statement, args, err := ormbuilder.NewDeleteBuilder(s.store.SQLRenderer, table).Where(ormbuilder.And(
			ormbuilder.Equal("source_kind", sourceKind), ormbuilder.Equal("source_id", sourceID), ormbuilder.IsNotNull("disabled_at"),
		)).Build()
		if err != nil {
			return fmt.Errorf("build retired %s projection cleanup: %w", table, err)
		}
		if _, err := s.database().ExecContext(ctx, statement, args...); err != nil {
			return fmt.Errorf("purge retired %s projection rows: %w", table, err)
		}
	}
	return nil
}

func cloneMetadataModuleConfig(value map[string]any) map[string]any {
	result := make(map[string]any, len(value)+1)
	for key, item := range value {
		result[key] = item
	}
	return result
}

func (s ApplicationSchemaStore) loadMetadataModuleDefinitions(ctx context.Context) ([]definitionmodel.ObjectSchema, []definitionmodel.FieldSchema, []definitionmodel.ValidationSchema, []definitionmodel.ActionSchema, []appschemamodel.DictionarySchema, error) {
	repository := s.metadataDefinitions
	if repository == nil && s.store != nil {
		repository = s.store.MetadataDefinitions()
	}
	if repository == nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("Metadata definition repository is unavailable")
	}
	snapshot, err := repository.DefinitionSnapshot(ctx)
	if err != nil {
		return nil, nil, nil, nil, nil, err
	}
	objects, fields := []definitionmodel.ObjectSchema{}, []definitionmodel.FieldSchema{}
	validations, actions := []definitionmodel.ValidationSchema{}, []definitionmodel.ActionSchema{}
	dictionaries := []appschemamodel.DictionarySchema{}
	for _, definition := range snapshot.Definitions {
		switch definition.ResourceType {
		case "object":
			var value definitionmodel.ObjectSchema
			if err := decodeMetadataModuleDefinition(definition.Key, definition.Payload, &value); err != nil {
				return nil, nil, nil, nil, nil, err
			}
			objects = append(objects, value)
		case "field":
			var value definitionmodel.FieldSchema
			if err := decodeMetadataModuleDefinition(definition.Key, definition.Payload, &value); err != nil {
				return nil, nil, nil, nil, nil, err
			}
			fields = append(fields, value)
		case "validation":
			var value definitionmodel.ValidationSchema
			if err := decodeMetadataModuleDefinition(definition.Key, definition.Payload, &value); err != nil {
				return nil, nil, nil, nil, nil, err
			}
			validations = append(validations, value)
		case "action":
			var value definitionmodel.ActionSchema
			if err := decodeMetadataModuleDefinition(definition.Key, definition.Payload, &value); err != nil {
				return nil, nil, nil, nil, nil, err
			}
			actions = append(actions, value)
		case "dictionary":
			var value appschemamodel.DictionarySchema
			if err := decodeMetadataModuleDefinition(definition.Key, definition.Payload, &value); err != nil {
				return nil, nil, nil, nil, nil, err
			}
			dictionaries = append(dictionaries, value)
		}
	}
	return objects, fields, validations, actions, dictionaries, nil
}

func decodeMetadataModuleDefinition(key string, payload json.RawMessage, target any) error {
	if err := json.Unmarshal(payload, target); err != nil {
		return fmt.Errorf("decode Metadata definition %s: %w", key, err)
	}
	return nil
}
