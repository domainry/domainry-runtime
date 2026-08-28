package reportmodel

// ReportExportScopeRequest is the closed, user-controlled part of a governed
// export. Dataset keys select only declared fields/dimensions/measures/analyses;
// Object SQL values bind only declared typed parameters. Neither form accepts
// SQL text or request-selected identifiers.
type ReportExportScopeRequest struct {
	Parameters        map[string]any              `json:"parameters,omitempty"`
	QueryKey          string                      `json:"query_key,omitempty"`
	AnalysisKey       string                      `json:"analysis_key,omitempty"`
	Filters           []ReportExportFilter        `json:"filters,omitempty"`
	DateRange         *ReportExportDateRange      `json:"date_range,omitempty"`
	TimeZone          string                      `json:"timezone,omitempty"`
	Tags              []string                    `json:"tags,omitempty"`
	TagMatch          string                      `json:"tag_match,omitempty"`
	FieldProjection   []string                    `json:"field_projection,omitempty"`
	Purpose           string                      `json:"purpose"`
	MetricDefinitions []ReportMetricDefinitionRef `json:"metric_definitions,omitempty"`
	Freshness         ReportExportFreshness       `json:"freshness"`
	RoleKey           string                      `json:"role_key,omitempty"`
	DataScopes        map[string]string           `json:"data_scopes,omitempty"`
}

type ReportExportFilter struct {
	DimensionKey string   `json:"dimension_key"`
	Operator     string   `json:"operator"`
	Values       []string `json:"values,omitempty"`
}

type ReportExportDateRange struct {
	DimensionKey string `json:"dimension_key"`
	From         string `json:"from"`
	To           string `json:"to"`
}

type ReportMetricDefinitionRef struct {
	Key     string `json:"key"`
	Version string `json:"version"`
}

type ReportExportFreshness struct {
	Mode              string `json:"mode"`
	SnapshotID        string `json:"snapshot_id,omitempty"`
	MaximumLagSeconds int64  `json:"maximum_lag_seconds,omitempty"`
}

// ReportExportArtifact is the Runtime-owned durable download record. Content
// is final (including watermark), so ContentSHA256 always describes exactly
// the bytes returned by GET download.
type ReportExportArtifact struct {
	ID                       string                   `json:"id"`
	WorkspaceID              string                   `json:"workspace_id"`
	ReportKey                string                   `json:"report_key"`
	ObjectKey                string                   `json:"object_key"`
	AuditID                  string                   `json:"audit_id"`
	BusinessDownloadID       string                   `json:"business_download_id,omitempty"`
	RequesterUserID          string                   `json:"requester_user_id"`
	RoleKey                  string                   `json:"role_key"`
	IdempotencyKey           string                   `json:"idempotency_key"`
	Token                    string                   `json:"token"`
	Filename                 string                   `json:"filename"`
	Scope                    ReportExportScopeRequest `json:"scope"`
	ScopeSHA256              string                   `json:"scope_sha256"`
	AuthorizationScopeSHA256 string                   `json:"authorization_scope_sha256"`
	ReportDefinitionSHA256   string                   `json:"report_definition_sha256"`
	ControlDefinitionSHA256  string                   `json:"control_definition_sha256"`
	ContentSHA256            string                   `json:"content_sha256"`
	RowCount                 int                      `json:"row_count"`
	Content                  []byte                   `json:"-"`
	Watermarked              bool                     `json:"watermarked"`
	CreatedAt                string                   `json:"created_at"`
	ExpiresAt                string                   `json:"expires_at"`
}
