package reportmodel

// ReportSummary is the canonical dataset result. The pre-publication Runtime
// has no legacy per-object summary representation: every response is produced
// from dimensions and measures declared by the Report Dataset.
type ReportSummary struct {
	Key            string                     `json:"key"`
	Name           string                     `json:"name,omitempty"`
	Rows           []ReportResultRow          `json:"rows"`
	RowCount       int                        `json:"row_count"`
	SourceRowCount int                        `json:"source_row_count"`
	Analyses       []ReportAnalysisResult     `json:"analyses,omitempty"`
	ExecutionMode  string                     `json:"execution_mode"`
	Snapshot       *ReportSnapshotFreshness   `json:"snapshot,omitempty"`
	ResultSchema   []ReportResultColumnSchema `json:"result_schema,omitempty"`
	PageSize       int                        `json:"page_size,omitempty"`
	NextCursor     string                     `json:"next_cursor,omitempty"`
	Truncated      bool                       `json:"truncated"`
	Total          int                        `json:"total"`
	TotalSemantics string                     `json:"total_semantics"`
}

const (
	ReportPageDefaultSize = 100
	ReportPageMaximumSize = 200
	ReportTotalExact      = "exact"
	ReportTotalAtLeast    = "at_least"
)

type ReportPageRequest struct {
	PageSize int    `json:"page_size,omitempty"`
	Cursor   string `json:"cursor,omitempty"`
}

type ReportSnapshotFreshness struct {
	SnapshotID     string            `json:"snapshot_id"`
	Watermark      string            `json:"watermark"`
	SourceVersions map[string]string `json:"source_versions"`
	RefreshedAt    string            `json:"refreshed_at"`
	LagSeconds     int64             `json:"lag_seconds"`
	Stale          bool              `json:"stale"`
}

type ReportAnalysisResult struct {
	Key  string            `json:"key"`
	Type string            `json:"type"`
	Rows []ReportResultRow `json:"rows"`
}

type ReportResultRow struct {
	Dimensions map[string]string `json:"dimensions,omitempty"`
	Measures   map[string]string `json:"measures,omitempty"`
}
