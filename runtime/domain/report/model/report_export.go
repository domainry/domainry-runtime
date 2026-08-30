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

// ReportExportAuthorizationSnapshot is the immutable authorization evidence
// carried by a Data Exchange export request and revalidated before download.
type ReportExportAuthorizationSnapshot struct {
	ObjectKey                string
	Scope                    ReportExportScopeRequest
	ScopeSHA256              string
	AuthorizationScopeSHA256 string
	ReportDefinitionSHA256   string
	ControlDefinitionSHA256  string
}
