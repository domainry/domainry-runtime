package appschema

import (
	"fmt"
	"strings"

	metadatarepository "github.com/domainry/domainry-metadata-sdk/repository"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

func metadataModuleOwnsDefinition(resourceType string) bool {
	switch strings.TrimSpace(resourceType) {
	case "object", "field", "validation", "action", "dictionary":
		return true
	default:
		return false
	}
}

func (s ApplicationSchemaStore) metadataModuleDefinitionStore() (metadatarepository.ExecutorDefinitionRepository, error) {
	repository := s.metadataDefinitions
	if repository == nil && s.store != nil {
		repository = s.store.MetadataDefinitions()
	}
	result, ok := repository.(metadatarepository.ExecutorDefinitionRepository)
	if !ok {
		return nil, fmt.Errorf("Metadata executor definition repository is unavailable")
	}
	return result, nil
}

func applicationDefinitionFromMetadata(value metadatarepository.StoredDefinition) appschemamodel.ApplicationDefinition {
	return appschemamodel.ApplicationDefinition{
		ResourceType: value.ResourceType, ResourceKey: value.Key, ObjectKey: value.ObjectKey, Name: value.Name,
		Payload: append([]byte(nil), value.Payload...), SchemaVersion: value.SchemaVersion, SchemaHash: value.SchemaHash,
		SourceKind: value.SourceKind, SourceID: value.SourceID, DisabledAt: value.DisabledAt,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}
