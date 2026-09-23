package appschemamodel

import (
	"encoding/json"

	metadatasdk "github.com/domainry/domainry-metadata-sdk"
)

type ApplicationSchemaPhysicalSchemaMismatchError struct {
	ObjectKey    string
	ColumnKey    string
	ExpectedType string
	ActualType   string
}

// ApplicationDefinition remains as a source-compatible alias while Metadata
// SDK owns the canonical business DTO.
type ApplicationDefinition = metadatasdk.Definition

func (e *ApplicationSchemaPhysicalSchemaMismatchError) Error() string {
	return "backend.metadata.physical_schema_incompatible"
}

func (e *ApplicationSchemaPhysicalSchemaMismatchError) ErrorCode() string {
	return e.Error()
}

func (e *ApplicationSchemaPhysicalSchemaMismatchError) ErrorParams() map[string]string {
	return map[string]string{
		"object_key":    e.ObjectKey,
		"column_key":    e.ColumnKey,
		"expected_type": e.ExpectedType,
		"actual_type":   e.ActualType,
	}
}

type ApplicationDefinitionUpsertRequest struct {
	ObjectKey string          `json:"object_key,omitempty"`
	Name      string          `json:"name,omitempty"`
	Payload   json.RawMessage `json:"payload"`
}

type ApplicationDefinitionMutation struct {
	Operation    string
	ResourceType string
	ResourceKey  string
	Request      ApplicationDefinitionUpsertRequest
}
