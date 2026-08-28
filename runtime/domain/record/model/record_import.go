package recordmodel

type RecordImportRowIssue struct {
	Field    string            `json:"field,omitempty"`
	Message  string            `json:"message"`
	Code     string            `json:"code,omitempty"`
	Params   map[string]string `json:"params,omitempty"`
	Severity string            `json:"severity"`
}

type RecordImportPreviewRow struct {
	Row          int                    `json:"row"`
	Data         map[string]any         `json:"data"`
	RawValues    map[string]string      `json:"raw_values,omitempty"`
	Issues       []RecordImportRowIssue `json:"issues"`
	ErrorSummary string                 `json:"error_summary,omitempty"`
	Valid        bool                   `json:"valid"`
	Duplicate    bool                   `json:"duplicate"`
}

type RecordImportPreview struct {
	ObjectKey     string                   `json:"object_key"`
	Rows          []RecordImportPreviewRow `json:"rows"`
	ErrorRows     []RecordImportPreviewRow `json:"error_rows,omitempty"`
	ValidRows     int                      `json:"valid_rows"`
	InvalidRows   int                      `json:"invalid_rows"`
	DuplicateRows int                      `json:"duplicate_rows"`
	CanApply      bool                     `json:"can_apply"`
}

type RecordImportApplyResult struct {
	ObjectKey string              `json:"object_key"`
	Created   int                 `json:"created"`
	Skipped   int                 `json:"skipped"`
	Preview   RecordImportPreview `json:"preview"`
}
