package reportmodel

type ReportSnapshot struct {
	ID              string            `json:"id"`
	WorkspaceID     string            `json:"workspace_id"`
	ReportKey       string            `json:"report_key"`
	AccessScopeHash string            `json:"-"`
	IdempotencyKey  string            `json:"idempotency_key"`
	Status          string            `json:"status"`
	Summary         ReportSummary     `json:"summary,omitempty"`
	Watermark       string            `json:"watermark,omitempty"`
	SourceVersions  map[string]string `json:"source_versions,omitempty"`
	StartedAt       string            `json:"started_at"`
	RefreshedAt     string            `json:"refreshed_at,omitempty"`
	ErrorCode       string            `json:"error_code,omitempty"`
}

type ReportSnapshotSourceVersion struct {
	Watermark      string            `json:"watermark"`
	SourceVersions map[string]string `json:"source_versions"`
}
