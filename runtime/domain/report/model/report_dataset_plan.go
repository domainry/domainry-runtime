package reportmodel

// ReportDatasetPlan is the normalized, publication-safe execution shape for a
// report dataset. It deliberately contains only metadata-derived aliases and
// never carries industry object semantics.
type ReportDatasetPlan struct {
	ReportKey      string
	Dataset        ReportDatasetSchema
	AliasObjects   map[string]string
	AliasParents   map[string]string
	MeasureSources map[string][]string
}

// ReportDatasetPlanError is stable across authoring, simulation, and runtime
// execution so an unsafe definition cannot pass publication and fail later.
type ReportDatasetPlanError struct {
	Code   string
	Path   string
	Params map[string]string
}

func (e *ReportDatasetPlanError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code + " at " + e.Path
}

func (e *ReportDatasetPlanError) ErrorCode() string {
	if e == nil {
		return ""
	}
	return e.Code
}
