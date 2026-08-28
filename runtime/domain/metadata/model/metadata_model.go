package metadatamodel

import (
	"encoding/json"
)

type LocalizedText struct {
	WorkspaceID string `json:"workspace_id"`
	EntityType  string `json:"entity_type"`
	EntityKey   string `json:"entity_key"`
	Property    string `json:"property"`
	Locale      string `json:"locale"`
	Text        string `json:"text"`
	SourceKind  string `json:"source_kind,omitempty"`
	SourceID    string `json:"source_id,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

type LocalizedTextUpsertRequest struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
	EntityType  string `json:"entity_type"`
	EntityKey   string `json:"entity_key"`
	Property    string `json:"property"`
	Locale      string `json:"locale"`
	Text        string `json:"text"`
	SourceKind  string `json:"source_kind,omitempty"`
	SourceID    string `json:"source_id,omitempty"`
}

type LocalizedTextQuery struct {
	WorkspaceID string
	EntityType  string
	EntityKey   string
	Property    string
	Locale      string
}

type LocalizedTextCoverageItem struct {
	WorkspaceID    string `json:"workspace_id"`
	EntityType     string `json:"entity_type"`
	EntityKey      string `json:"entity_key"`
	Property       string `json:"property"`
	Locale         string `json:"locale"`
	RequestedText  string `json:"requested_text,omitempty"`
	FallbackLocale string `json:"fallback_locale,omitempty"`
	FallbackText   string `json:"fallback_text,omitempty"`
	DefaultText    string `json:"default_text,omitempty"`
	ResolvedText   string `json:"resolved_text"`
	ResolvedSource string `json:"resolved_source"`
	SourceKind     string `json:"source_kind,omitempty"`
	SourceID       string `json:"source_id,omitempty"`
	Missing        bool   `json:"missing"`
}

type LocalizedTextCoverageResult struct {
	WorkspaceID    string                      `json:"workspace_id"`
	Locale         string                      `json:"locale"`
	FallbackLocale string                      `json:"fallback_locale,omitempty"`
	Items          []LocalizedTextCoverageItem `json:"items"`
	MissingCount   int                         `json:"missing_count"`
	TotalCount     int                         `json:"total_count"`
}

type MetadataDefinitionVersion struct {
	ResourceType  string          `json:"resource_type"`
	ResourceKey   string          `json:"resource_key"`
	SchemaVersion string          `json:"schema_version"`
	SchemaHash    string          `json:"schema_hash"`
	Payload       json.RawMessage `json:"payload"`
	CreatedAt     string          `json:"created_at"`
}

type MetadataMigrationStep struct {
	ObjectKey   string `json:"object_key"`
	Table       string `json:"table"`
	Operation   string `json:"operation"`
	ColumnKey   string `json:"column_key,omitempty"`
	ColumnType  string `json:"column_type,omitempty"`
	Reversible  bool   `json:"reversible"`
	Description string `json:"description"`
}

type MetadataPhysicalSchemaMismatchError struct {
	ObjectKey    string
	ColumnKey    string
	ExpectedType string
	ActualType   string
}

func (e *MetadataPhysicalSchemaMismatchError) Error() string {
	return "backend.metadata.physical_schema_incompatible"
}

func (e *MetadataPhysicalSchemaMismatchError) ErrorCode() string {
	return e.Error()
}

func (e *MetadataPhysicalSchemaMismatchError) ErrorParams() map[string]string {
	return map[string]string{
		"object_key":    e.ObjectKey,
		"column_key":    e.ColumnKey,
		"expected_type": e.ExpectedType,
		"actual_type":   e.ActualType,
	}
}

type MetadataDefinition struct {
	ResourceType  string          `json:"resource_type"`
	ResourceKey   string          `json:"resource_key"`
	ObjectKey     string          `json:"object_key,omitempty"`
	Name          string          `json:"name,omitempty"`
	Payload       json.RawMessage `json:"payload"`
	SchemaVersion string          `json:"schema_version,omitempty"`
	SchemaHash    string          `json:"schema_hash,omitempty"`
	SourceKind    string          `json:"source_kind,omitempty"`
	SourceID      string          `json:"source_id,omitempty"`
	DisabledAt    string          `json:"disabled_at,omitempty"`
	CreatedAt     string          `json:"created_at,omitempty"`
	UpdatedAt     string          `json:"updated_at,omitempty"`
}

type MetadataDefinitionUpsertRequest struct {
	ObjectKey          string          `json:"object_key,omitempty"`
	Name               string          `json:"name,omitempty"`
	SourceKind         string          `json:"source_kind,omitempty"`
	SourceID           string          `json:"source_id,omitempty"`
	ExpectedSchemaHash *string         `json:"expected_schema_hash,omitempty"`
	Payload            json.RawMessage `json:"payload"`
}

type MetadataDefinitionConflictError struct {
	ResourceType string
	ResourceKey  string
	ExpectedHash string
	CurrentHash  string
}

func (e *MetadataDefinitionConflictError) Error() string {
	return "metadata.definition.versionConflict"
}

type MetadataDefinitionMutation struct {
	Operation    string
	ResourceType string
	ResourceKey  string
	Request      MetadataDefinitionUpsertRequest
}

type MetadataDefinitionRollbackRequest struct {
	TargetVersion              string `json:"target_version"`
	ExpectedSchemaHash         string `json:"expected_schema_hash"`
	ExpectedReferenceGraphHash string `json:"expected_reference_graph_hash"`
	BusinessReason             string `json:"business_reason"`
	ChangePlanID               string `json:"change_plan_id"`
	ChangePlanRevision         int    `json:"change_plan_revision,omitempty"`
	BuilderTaskID              string `json:"builder_task_id,omitempty"`
	AuthoringContractVersion   string `json:"authoring_contract_version"`
	AuthoringContractHash      string `json:"authoring_contract_hash"`
}
