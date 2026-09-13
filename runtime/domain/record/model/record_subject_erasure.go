package recordmodel

type SubjectRecordReference struct {
	ObjectKey string `json:"object_key"`
	RecordID  string `json:"record_id"`
}

// SubjectErasureMutation freezes only the target, timestamps and sanitized
// replacement values. Personal values are never copied into cleanup journals.
type SubjectErasureMutation struct {
	ObjectKey       string         `json:"object_key"`
	RecordID        string         `json:"record_id"`
	BeforeUpdatedAt string         `json:"before_updated_at"`
	AfterUpdatedAt  string         `json:"after_updated_at"`
	Values          map[string]any `json:"values"`
}
