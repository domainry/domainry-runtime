package businessseedmodel

type SeedRecordSchema struct {
	ObjectKey  string         `json:"object_key"`
	Data       map[string]any `json:"data"`
	SourceKind string         `json:"source_kind,omitempty"`
	SourceID   string         `json:"source_id,omitempty"`
}

type SeedRecordAuthoringRequest struct {
	SeedKey    string         `json:"seed_key"`
	ObjectKey  string         `json:"object_key"`
	Data       map[string]any `json:"data"`
	SourceKind string         `json:"source_kind,omitempty"`
	SourceID   string         `json:"source_id,omitempty"`
}

type BusinessSeedProvenance struct {
	SeedKey         string `json:"seed_key"`
	ObjectKey       string `json:"object_key"`
	RecordID        string `json:"record_id"`
	SourceKind      string `json:"source_kind"`
	SourceID        string `json:"source_id"`
	TemplateID      string `json:"template_id,omitempty"`
	TemplateVersion string `json:"template_version,omitempty"`
	ContentHash     string `json:"content_hash"`
	MaterializedAt  string `json:"materialized_at"`
}

type SeedRecordAuthoringResult struct {
	SeedKey     string         `json:"seed_key"`
	ObjectKey   string         `json:"object_key"`
	RecordID    string         `json:"record_id"`
	Data        map[string]any `json:"data"`
	SourceKind  string         `json:"source_kind"`
	SourceID    string         `json:"source_id"`
	ContentHash string         `json:"content_hash"`
	Replayed    bool           `json:"replayed"`
}
