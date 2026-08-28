package reportmodel

import "reflect"

type ReportDatasetSchema struct {
	Source           ReportDatasetSource        `json:"source"`
	Joins            []ReportDatasetJoin        `json:"joins,omitempty"`
	Filters          []ReportDatasetFilter      `json:"filters,omitempty"`
	QueryPredicates  []ReportDatasetPredicate   `json:"query_predicates,omitempty"`
	TagPredicates    []ReportDatasetPredicate   `json:"tag_predicates,omitempty"`
	Dimensions       []ReportDatasetDimension   `json:"dimensions,omitempty"`
	Measures         []ReportDatasetMeasure     `json:"measures,omitempty"`
	DefaultTimeGrain string                     `json:"time_grain,omitempty"`
	TimeZone         string                     `json:"time_zone,omitempty"`
	Sort             []ReportDatasetSort        `json:"sort,omitempty"`
	Limit            int                        `json:"limit,omitempty"`
	Privacy          *ReportDatasetPrivacy      `json:"privacy,omitempty"`
	Comparisons      []ReportDatasetComparison  `json:"comparisons,omitempty"`
	Analyses         []ReportDatasetAnalysis    `json:"analyses,omitempty"`
	RuntimeQuery     *ReportDatasetRuntimeQuery `json:"-"`
	RuntimeTags      *ReportDatasetRuntimeTags  `json:"-"`
}

type ReportDatasetSource struct {
	ObjectKey  string `json:"object_key"`
	Alias      string `json:"alias"`
	SourceType string `json:"source_type,omitempty"`
}

type ReportDatasetJoin struct {
	Alias            string                           `json:"alias"`
	ObjectKey        string                           `json:"object_key"`
	Type             string                           `json:"type"`
	LeftAlias        string                           `json:"left_alias"`
	LeftField        string                           `json:"left_field,omitempty"`
	RightField       string                           `json:"right_field,omitempty"`
	FieldEqualities  []ReportDatasetJoinFieldEquality `json:"field_equalities,omitempty"`
	Cardinality      string                           `json:"cardinality"`
	SourceType       string                           `json:"source_type,omitempty"`
	RuntimeScopeOnly bool                             `json:"-"`
}

// ReportDatasetRuntimeQuery and ReportDatasetRuntimeTags are derived only
// from a validated Report-owned export scope and never decoded from metadata.
type ReportDatasetRuntimeQuery struct {
	Mode       string
	Value      string
	Predicates []ReportExportQueryPredicate
}

type ReportDatasetRuntimeTags struct {
	ScopeJoinAliases []string
	TargetField      ReportDatasetField
	TagField         ReportDatasetField
	Values           []string
	Match            string
}

// ReportDatasetJoinFieldEquality is one allowlisted column comparison in a
// dataset join. Runtime accepts only equality between declared fields; it never
// evaluates author-provided SQL or expression text.
type ReportDatasetJoinFieldEquality struct {
	LeftField  string `json:"left_field"`
	RightField string `json:"right_field"`
}

// Equalities returns the canonical join predicate while preserving the legacy
// single-key shape. Metadata validation rejects mixed or incomplete shapes.
func (j ReportDatasetJoin) Equalities() []ReportDatasetJoinFieldEquality {
	if len(j.FieldEqualities) > 0 {
		return append([]ReportDatasetJoinFieldEquality(nil), j.FieldEqualities...)
	}
	if j.LeftField == "" && j.RightField == "" {
		return nil
	}
	return []ReportDatasetJoinFieldEquality{{LeftField: j.LeftField, RightField: j.RightField}}
}

type ReportDatasetField struct {
	SourceAlias string `json:"source_alias"`
	FieldKey    string `json:"field_key"`
}

type ReportDatasetFilter struct {
	Field    ReportDatasetField `json:"field"`
	Operator string             `json:"operator"`
	Value    any                `json:"value,omitempty"`
	Values   []any              `json:"values,omitempty"`
}

// ReportDatasetPredicate binds a stable public query or tag key to a closed
// conjunction of ordinary dataset filters. It is executable by both summary
// and export scoping and cannot carry arbitrary query text.
type ReportDatasetPredicate struct {
	Key     string                `json:"key"`
	Filters []ReportDatasetFilter `json:"filters"`
}

type ReportDatasetDimension struct {
	Key       string             `json:"key"`
	Field     ReportDatasetField `json:"field"`
	TimeGrain string             `json:"time_grain,omitempty"`
}

type ReportDatasetMeasure struct {
	Key            string              `json:"key"`
	Operation      string              `json:"operation"`
	SourceAlias    string              `json:"source_alias,omitempty"`
	Field          *ReportDatasetField `json:"field,omitempty"`
	StartField     *ReportDatasetField `json:"start_field,omitempty"`
	EndField       *ReportDatasetField `json:"end_field,omitempty"`
	NumeratorKey   string              `json:"numerator_key,omitempty"`
	DenominatorKey string              `json:"denominator_key,omitempty"`
	Percentile     string              `json:"percentile,omitempty"`
}

type ReportDatasetSort struct {
	Key       string `json:"key"`
	Direction string `json:"direction"`
}

// ReportDatasetPrivacy suppresses groups before results leave the Report
// owner. EntityField is counted distinctly so join fan-out cannot fake the
// minimum group size.
type ReportDatasetPrivacy struct {
	MinimumGroupSize int                `json:"minimum_group_size"`
	EntityField      ReportDatasetField `json:"entity_field"`
}

type ReportDatasetComparison struct {
	Key              string `json:"key"`
	Type             string `json:"type"`
	TimeDimensionKey string `json:"time_dimension_key"`
	MeasureKey       string `json:"measure_key"`
	Operation        string `json:"operation"`
	OffsetPeriods    int    `json:"offset_periods,omitempty"`
}

type ReportDatasetAnalysis struct {
	Key            string                     `json:"key"`
	Type           string                     `json:"type"`
	EntityField    ReportDatasetField         `json:"entity_field"`
	EventField     *ReportDatasetField        `json:"event_field,omitempty"`
	TimeField      ReportDatasetField         `json:"time_field"`
	Stages         []ReportDatasetFunnelStage `json:"stages,omitempty"`
	WindowSeconds  int                        `json:"window_seconds,omitempty"`
	CohortGrain    string                     `json:"cohort_grain,omitempty"`
	PeriodGrain    string                     `json:"period_grain,omitempty"`
	MaximumPeriods int                        `json:"maximum_periods,omitempty"`
}

type ReportDatasetFunnelStage struct {
	Key    string `json:"key"`
	Values []any  `json:"values"`
}

func ReportDatasetObjectKeys(dataset ReportDatasetSchema) []string {
	keys := make([]string, 0, len(dataset.Joins)+1)
	if dataset.Source.ObjectKey != "" {
		keys = append(keys, dataset.Source.ObjectKey)
	}
	for _, join := range dataset.Joins {
		if join.ObjectKey != "" {
			keys = append(keys, join.ObjectKey)
		}
	}
	return keys
}

// ReportDatasetDefined distinguishes the absent zero-value execution branch
// from any authored Dataset content. Runtime-only query decorations are not
// authoring fields, but still count as a defined Dataset inside Runtime.
func ReportDatasetDefined(dataset ReportDatasetSchema) bool {
	return !reflect.DeepEqual(dataset, ReportDatasetSchema{})
}

// ReportExecutionObjectKeys returns the declared source objects for whichever
// mutually exclusive Report execution definition is selected.
func ReportExecutionObjectKeys(dataset *ReportDatasetSchema, objectSQL *ReportObjectSQLSchema) []string {
	if objectSQL != nil {
		return ReportObjectSQLObjectKeys(objectSQL)
	}
	if dataset == nil {
		return nil
	}
	return ReportDatasetObjectKeys(*dataset)
}

func ReportDatasetSnapshotObjectKeys(dataset ReportDatasetSchema) []string {
	keys := []string{}
	if dataset.Source.SourceType == "snapshot" && dataset.Source.ObjectKey != "" {
		keys = append(keys, dataset.Source.ObjectKey)
	}
	for _, join := range dataset.Joins {
		if join.SourceType == "snapshot" && join.ObjectKey != "" {
			keys = append(keys, join.ObjectKey)
		}
	}
	return keys
}

func ReportDatasetAliasObjects(dataset ReportDatasetSchema) map[string]string {
	result := map[string]string{}
	if dataset.Source.Alias != "" {
		result[dataset.Source.Alias] = dataset.Source.ObjectKey
	}
	for _, join := range dataset.Joins {
		if join.Alias != "" {
			result[join.Alias] = join.ObjectKey
		}
	}
	return result
}

func ReportDatasetFieldObjectKey(dataset ReportDatasetSchema, field ReportDatasetField) string {
	return ReportDatasetAliasObjects(dataset)[field.SourceAlias]
}
