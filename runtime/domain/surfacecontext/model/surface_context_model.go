package surfacecontextmodel

import recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

type SurfaceContextObjectRequest struct {
	ObjectKey    string                       `json:"object_key"`
	ViewKey      string                       `json:"view,omitempty"`
	Page         int                          `json:"page,omitempty"`
	PageSize     int                          `json:"page_size,omitempty"`
	Search       string                       `json:"search,omitempty"`
	SearchFields []string                     `json:"search_fields,omitempty"`
	Filters      map[string]any               `json:"filters,omitempty"`
	Sort         []recordmodel.RecordSortRule `json:"sort,omitempty"`
}

type SurfaceContextRequest struct {
	SurfaceKey        string                        `json:"surface_key,omitempty"`
	SelectedObjectKey string                        `json:"selected_object_key,omitempty"`
	SelectedRecordID  string                        `json:"selected_record_id,omitempty"`
	Include           []string                      `json:"include,omitempty"`
	Objects           []SurfaceContextObjectRequest `json:"objects"`
}

type SurfaceContextObjectResult struct {
	ObjectKey string                       `json:"object_key"`
	Page      recordmodel.RecordPageResult `json:"page"`
	Error     string                       `json:"error,omitempty"`
}

type SurfaceContextSelectedResult struct {
	ObjectKey string             `json:"object_key,omitempty"`
	Record    recordmodel.Record `json:"record,omitempty"`
	Error     string             `json:"error,omitempty"`
}

type SurfaceContextDiagnostics struct {
	RequestedObjectCount int      `json:"requested_object_count"`
	ReadableObjectCount  int      `json:"readable_object_count"`
	DeniedObjectCount    int      `json:"denied_object_count"`
	RelationLabelCount   int      `json:"relation_label_count"`
	MaskedFieldCount     int      `json:"masked_field_count"`
	Include              []string `json:"include,omitempty"`
}

type SurfaceContextResult struct {
	SurfaceKey          string                                `json:"surface_key,omitempty"`
	Selected            *SurfaceContextSelectedResult         `json:"selected,omitempty"`
	Objects             map[string]SurfaceContextObjectResult `json:"objects"`
	RelationLabels      map[string]map[string]string          `json:"relation_labels,omitempty"`
	RelationProjections []RelationProjection                  `json:"relation_projections,omitempty"`
	Metrics             map[string]any                        `json:"metrics,omitempty"`
	Reports             []map[string]any                      `json:"reports,omitempty"`
	Permissions         map[string]any                        `json:"permissions,omitempty"`
	Diagnostics         SurfaceContextDiagnostics             `json:"diagnostics"`
}

// RelationProjection is emitted when the target is readable or when the role
// has a label-only reference permission for the source relation. Absence is
// intentionally ambiguous between missing and unauthorized targets.
type RelationProjection struct {
	SourceObjectKey string `json:"source_object_key"`
	SourceRecordID  string `json:"source_record_id"`
	FieldKey        string `json:"field_key"`
	TargetObjectKey string `json:"target_object_key"`
	TargetRecordID  string `json:"target_record_id"`
	DisplayField    string `json:"display_field"`
	DisplayValue    string `json:"display_value"`
	DetailRoute     string `json:"detail_route,omitempty"`
	Mode            string `json:"mode,omitempty"`
}
