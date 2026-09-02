package businessseedmodel

type SeedRecordSchema struct {
	ObjectKey   string         `json:"object_key"`
	Data        map[string]any `json:"data"`
	OwnerUserID string         `json:"owner_user_id,omitempty"`
	OwnerOrgID  string         `json:"owner_org_id,omitempty"`
	SourceKind  string         `json:"source_kind,omitempty"`
	SourceID    string         `json:"source_id,omitempty"`
}

// BusinessSeedProvenance remains as a projection compatibility type. Runtime
// no longer persists it in a dedicated ledger.
type BusinessSeedProvenance struct {
	SeedKey, ObjectKey, RecordID, SourceKind, SourceID       string
	TemplateID, TemplateVersion, ContentHash, MaterializedAt string
}
