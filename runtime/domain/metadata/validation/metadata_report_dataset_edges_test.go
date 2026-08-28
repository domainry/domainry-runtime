package validation

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

func reportDatasetObjectMap() map[string]definitionmodel.ObjectSchema {
	return map[string]definitionmodel.ObjectSchema{
		"event": {Key: "event", Fields: []definitionmodel.FieldSchema{
			{Key: "name", Type: "text"}, {Key: "status", Type: "select"}, {Key: "amount", Type: "currency"},
			{Key: "quantity", Type: "integer"}, {Key: "created_at", Type: "datetime"}, {Key: "ended_at", Type: "datetime"},
			{Key: "event_date", Type: "date"}, {Key: "other_date", Type: "date"},
		}},
		"detail": {Key: "detail", Fields: []definitionmodel.FieldSchema{{Key: "event_id", Type: "relation"}, {Key: "value", Type: "number"}}},
	}
}

func reportDatasetValidator(dataset reportmodel.ReportDatasetSchema) *reportDefinitionValidator {
	return &reportDefinitionValidator{
		report: reportmodel.ReportSchema{Key: "report", Dataset: dataset}, objects: reportDatasetObjectMap(), issues: nil,
	}
}

func reportField(alias, field string) reportmodel.ReportDatasetField {
	return reportmodel.ReportDatasetField{SourceAlias: alias, FieldKey: field}
}

func reportFunnel(key string, timeField reportmodel.ReportDatasetField, eventField *reportmodel.ReportDatasetField, stages []reportmodel.ReportDatasetFunnelStage, window int) reportmodel.ReportDatasetAnalysis {
	return reportmodel.ReportDatasetAnalysis{Key: key, Type: "funnel", EntityField: reportField("events", "name"), TimeField: timeField, EventField: eventField, Stages: stages, WindowSeconds: window}
}

func validFunnelStages() []reportmodel.ReportDatasetFunnelStage {
	return []reportmodel.ReportDatasetFunnelStage{{Key: "started", Values: []any{"started"}}, {Key: "finished", Values: []any{"finished"}}}
}

func TestReportDatasetAnalysisValidationCoversFunnelCohortAndShortCircuitBoundaries(t *testing.T) {
	event := reportField("events", "status")
	dataset := reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "event", Alias: "events"}}
	dataset.Analyses = []reportmodel.ReportDatasetAnalysis{
		reportFunnel("valid", reportField("events", "created_at"), &event, validFunnelStages(), 0),
		reportFunnel("", reportField("missing", "created_at"), nil, nil, 0),
		reportFunnel("valid", reportField("events", "name"), &event, validFunnelStages(), -1),
		reportFunnel("bad-window", reportField("events", "event_date"), &event, validFunnelStages(), 31536001),
		reportFunnel("datetime", reportField("events", "created_at"), &event, []reportmodel.ReportDatasetFunnelStage{
			{Key: "", Values: []any{"x"}},
			{Key: "same", Values: []any{"x"}},
			{Key: "same", Values: []any{"y"}},
			{Key: "empty"},
		}, 1),
		{Key: "cohort-default", Type: "cohort_retention", EntityField: reportField("events", "name"), TimeField: reportField("events", "created_at")},
		{Key: "cohort-grain", Type: "cohort_retention", EntityField: reportField("events", "name"), TimeField: reportField("events", "created_at"), CohortGrain: "minute", PeriodGrain: "month"},
		{Key: "cohort-period", Type: "cohort_retention", EntityField: reportField("events", "name"), TimeField: reportField("events", "created_at"), CohortGrain: "month", PeriodGrain: "minute"},
		{Key: "cohort-negative", Type: "cohort_retention", EntityField: reportField("events", "name"), TimeField: reportField("events", "created_at"), CohortGrain: "month", PeriodGrain: "month", MaximumPeriods: -1},
		{Key: "cohort-large", Type: "cohort_retention", EntityField: reportField("events", "name"), TimeField: reportField("events", "created_at"), CohortGrain: "month", PeriodGrain: "month", MaximumPeriods: 121},
		{Key: "cohort-event", Type: "cohort_retention", EntityField: reportField("events", "name"), TimeField: reportField("events", "created_at"), CohortGrain: "month", PeriodGrain: "month", EventField: &event},
		{Key: "cohort-stages", Type: "cohort_retention", EntityField: reportField("events", "name"), TimeField: reportField("events", "created_at"), CohortGrain: "month", PeriodGrain: "month", Stages: validFunnelStages()},
		{Key: "unknown", Type: "unknown", EntityField: reportField("events", "name"), TimeField: reportField("events", "created_at")},
	}
	validator := reportDatasetValidator(dataset)
	validator.validateDatasetAnalyses()
	if len(validator.issues) == 0 {
		t.Fatal("invalid analyses produced no issues")
	}
	for _, grain := range []string{"day", "week", "month", "quarter", "year"} {
		if !reportCohortTimeGrain(grain) {
			t.Fatalf("cohort grain %q rejected", grain)
		}
	}
	if reportCohortTimeGrain("hour") {
		t.Fatal("unsupported cohort grain accepted")
	}
}

func TestReportDatasetComparisonValidationCoversEveryContractOperand(t *testing.T) {
	dataset := reportmodel.ReportDatasetSchema{
		Source:           reportmodel.ReportDatasetSource{ObjectKey: "event", Alias: "events"},
		DefaultTimeGrain: "month",
		Dimensions: []reportmodel.ReportDatasetDimension{
			{Key: "time", Field: reportField("events", "created_at")},
			{Key: "day", Field: reportField("events", "event_date"), TimeGrain: "day"},
			{Key: "bad-grain", Field: reportField("events", "event_date"), TimeGrain: "invalid"},
		},
		Measures: []reportmodel.ReportDatasetMeasure{{Key: "total", Operation: "count"}, {Key: "other", Operation: "count"}},
	}
	valid := func(key string) reportmodel.ReportDatasetComparison {
		return reportmodel.ReportDatasetComparison{Key: key, Type: "period_over_period", TimeDimensionKey: "time", MeasureKey: "total", Operation: "difference", OffsetPeriods: 1}
	}
	dataset.Comparisons = []reportmodel.ReportDatasetComparison{
		valid("change"), valid("change"), valid(""), valid("time"), valid("total"),
		{Key: "type", Type: "bad", TimeDimensionKey: "time", MeasureKey: "total", Operation: "ratio"},
		{Key: "dimension", Type: "period_over_period", TimeDimensionKey: "missing", MeasureKey: "total", Operation: "percent_change"},
		{Key: "grain", Type: "period_over_period", TimeDimensionKey: "bad-grain", MeasureKey: "total", Operation: "difference"},
		{Key: "measure", Type: "period_over_period", TimeDimensionKey: "day", MeasureKey: "missing", Operation: "difference"},
		{Key: "operation", Type: "period_over_period", TimeDimensionKey: "day", MeasureKey: "total", Operation: "bad"},
		{Key: "negative", Type: "period_over_period", TimeDimensionKey: "day", MeasureKey: "total", Operation: "ratio", OffsetPeriods: -1},
		{Key: "large", Type: "period_over_period", TimeDimensionKey: "day", MeasureKey: "total", Operation: "percent_change", OffsetPeriods: 101},
	}
	validator := reportDatasetValidator(dataset)
	validator.validateDatasetComparisons()
	if len(validator.issues) == 0 {
		t.Fatal("invalid comparisons produced no issues")
	}
}

func TestReportDatasetJoinFilterDimensionAndPrivacyBoundaries(t *testing.T) {
	base := reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "event", Alias: "events"}}
	validators := []*reportDefinitionValidator{
		reportDatasetValidator(reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "event", Alias: "", SourceType: "bad"}}),
		reportDatasetValidator(reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "event", Alias: "events", SourceType: "snapshot"}}),
		reportDatasetValidator(reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "event", Alias: "events"}, Joins: []reportmodel.ReportDatasetJoin{
			{Alias: "details", ObjectKey: "detail", Type: "inner", LeftAlias: "events", LeftField: "id", RightField: "event_id", Cardinality: "one_to_one"},
			{Alias: "left", ObjectKey: "detail", Type: "left", LeftAlias: "details", LeftField: "event_id", RightField: "id", Cardinality: "many_to_one", SourceType: "snapshot"},
			{Alias: "many", ObjectKey: "detail", Type: "inner", LeftAlias: "events", LeftField: "id", RightField: "event_id", Cardinality: "one_to_many"},
			{Alias: "details", ObjectKey: "detail", Type: "bad", LeftAlias: "missing", LeftField: "missing", RightField: "missing", Cardinality: "bad", SourceType: "bad"},
			{Alias: "", ObjectKey: "missing", Type: "inner", LeftAlias: "events", LeftField: "id", RightField: "id", Cardinality: "one_to_one"},
		}}),
	}
	for _, validator := range validators {
		validator.validateDatasetJoins()
	}

	base.Filters = []reportmodel.ReportDatasetFilter{
		{Field: reportField("events", "status"), Operator: "eq"},
		{Field: reportField("events", "status"), Operator: "in"},
		{Field: reportField("events", "status"), Operator: "in", Values: []any{"open"}},
		{Field: reportField("events", "status"), Operator: "not_in"},
		{Field: reportField("events", "status"), Operator: "not_in", Values: []any{"closed"}},
		{Field: reportField("events", "amount"), Operator: "between"},
		{Field: reportField("events", "amount"), Operator: "between", Values: []any{1, 2}},
		{Field: reportField("events", "status"), Operator: "bad"},
	}
	base.Dimensions = []reportmodel.ReportDatasetDimension{
		{Key: "", Field: reportField("events", "name")},
		{Key: "name", Field: reportField("events", "name")},
		{Key: "name", Field: reportField("events", "name")},
		{Key: "invalid-grain", Field: reportField("events", "event_date"), TimeGrain: "bad"},
		{Key: "missing-field-grain", Field: reportField("events", "missing"), TimeGrain: "day"},
		{Key: "text-grain", Field: reportField("events", "name"), TimeGrain: "day"},
		{Key: "date-grain", Field: reportField("events", "event_date"), TimeGrain: "day"},
		{Key: "datetime-grain", Field: reportField("events", "created_at"), TimeGrain: "hour"},
	}
	base.Privacy = &reportmodel.ReportDatasetPrivacy{MinimumGroupSize: 1, EntityField: reportField("events", "name")}
	validator := reportDatasetValidator(base)
	validator.validateDatasetFilters()
	validator.validateDatasetDimensionsAndMeasures()
	validator.validateDatasetReferences()
	if len(validator.issues) == 0 {
		t.Fatal("invalid dataset boundaries produced no issues")
	}
	base.Privacy.MinimumGroupSize = 1001
	reportDatasetValidator(base).validateDatasetReferences()
	base.Privacy.MinimumGroupSize = 2
	reportDatasetValidator(base).validateDatasetReferences()
}

func TestReportDatasetPredicateAndCompoundJoinValidationIsClosed(t *testing.T) {
	valid := reportmodel.ReportDatasetSchema{
		Source: reportmodel.ReportDatasetSource{ObjectKey: "event", Alias: "events"},
		Joins: []reportmodel.ReportDatasetJoin{{Alias: "details", ObjectKey: "detail", Type: "inner", LeftAlias: "events", FieldEqualities: []reportmodel.ReportDatasetJoinFieldEquality{
			{LeftField: "id", RightField: "event_id"}, {LeftField: "status", RightField: "value"},
		}, Cardinality: "one_to_one"}},
		QueryPredicates: []reportmodel.ReportDatasetPredicate{{Key: "open", Filters: []reportmodel.ReportDatasetFilter{{Field: reportField("events", "status"), Operator: "eq", Value: "open"}}}},
		TagPredicates:   []reportmodel.ReportDatasetPredicate{{Key: "large", Filters: []reportmodel.ReportDatasetFilter{{Field: reportField("events", "amount"), Operator: "gte", Value: 100}}}},
	}
	validator := reportDatasetValidator(valid)
	validator.validateDatasetJoins()
	validator.validateDatasetPredicates("query_predicates", valid.QueryPredicates)
	validator.validateDatasetPredicates("tag_predicates", valid.TagPredicates)
	if len(validator.issues) != 0 {
		t.Fatalf("valid issues=%#v", validator.issues)
	}

	invalid := valid
	invalid.Joins = append([]reportmodel.ReportDatasetJoin(nil), valid.Joins...)
	invalid.Joins[0].LeftField, invalid.Joins[0].RightField = "id", "event_id"
	invalid.QueryPredicates = []reportmodel.ReportDatasetPredicate{
		{Key: "same", Filters: []reportmodel.ReportDatasetFilter{{Field: reportField("events", "status"), Operator: "sql", Value: "x"}}},
		{Key: "same"},
	}
	validator = reportDatasetValidator(invalid)
	validator.validateDatasetJoins()
	validator.validateDatasetPredicates("query_predicates", invalid.QueryPredicates)
	if len(validator.issues) < 3 {
		t.Fatalf("invalid issues=%#v", validator.issues)
	}
}

func TestReportDatasetMeasureValidationCoversEveryOperationAndDependencyBoundary(t *testing.T) {
	dataset := reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "event", Alias: "events"}}
	field := func(key string) *reportmodel.ReportDatasetField {
		value := reportField("events", key)
		return &value
	}
	missingAlias := reportField("missing", "amount")
	missingField := reportField("events", "missing")
	measures := map[string]reportmodel.ReportDatasetMeasure{
		"a":       {Key: "a", Operation: "count"},
		"b":       {Key: "b", Operation: "count"},
		"ratio-a": {Key: "ratio-a", Operation: "ratio", NumeratorKey: "a", DenominatorKey: "b"},
	}
	validator := reportDatasetValidator(dataset)
	tests := []reportmodel.ReportDatasetMeasure{
		{Key: "invalid", Operation: "invalid"},
		{Key: "count", Operation: "count"},
		{Key: "count-field", Operation: "count", Field: field("name")},
		{Key: "count-alias", Operation: "count", SourceAlias: "events"},
		{Key: "count-missing-alias", Operation: "count", SourceAlias: "missing"},
		{Key: "distinct-missing", Operation: "distinct_count"},
		{Key: "distinct", Operation: "distinct_count", Field: field("name")},
		{Key: "min", Operation: "min", Field: field("name")},
		{Key: "max", Operation: "max", Field: field("name")},
		{Key: "sum-missing", Operation: "sum"},
		{Key: "sum-invalid-ref", Operation: "sum", Field: &missingField},
		{Key: "sum-text", Operation: "sum", Field: field("name")},
		{Key: "sum", Operation: "sum", Field: field("amount")},
		{Key: "avg", Operation: "avg", Field: field("quantity")},
		{Key: "sum-alias", Operation: "sum", Field: field("amount"), SourceAlias: "events"},
		{Key: "percentile-bad", Operation: "percentile", Field: field("amount"), Percentile: "bad"},
		{Key: "percentile-zero", Operation: "percentile", Field: field("amount"), Percentile: "0"},
		{Key: "percentile-large", Operation: "percentile", Field: field("amount"), Percentile: "101"},
		{Key: "percentile", Operation: "percentile", Field: field("amount"), Percentile: "50"},
		{Key: "duration-start", Operation: "duration"},
		{Key: "duration-end", Operation: "duration", StartField: field("created_at")},
		{Key: "duration-start-invalid", Operation: "duration", StartField: &missingAlias, EndField: field("ended_at")},
		{Key: "duration-end-invalid", Operation: "duration", StartField: field("created_at"), EndField: &missingAlias},
		{Key: "duration-mismatch", Operation: "duration", StartField: field("created_at"), EndField: field("event_date")},
		{Key: "duration-text", Operation: "duration", StartField: field("name"), EndField: field("name")},
		{Key: "duration-date", Operation: "duration", StartField: field("event_date"), EndField: field("other_date")},
		{Key: "duration-datetime", Operation: "duration", StartField: field("created_at"), EndField: field("ended_at")},
	}
	for index, measure := range tests {
		validator.validateDatasetMeasure("measure", measure, measures)
		if index == 0 && len(validator.issues) == 0 {
			t.Fatal("invalid operation produced no issue")
		}
	}

	ratioCases := []struct {
		measure  reportmodel.ReportDatasetMeasure
		measures map[string]reportmodel.ReportDatasetMeasure
	}{
		{measure: reportmodel.ReportDatasetMeasure{Key: "r", Operation: "ratio", NumeratorKey: "missing", DenominatorKey: "b"}, measures: measures},
		{measure: reportmodel.ReportDatasetMeasure{Key: "r", Operation: "ratio", NumeratorKey: "a", DenominatorKey: "missing"}, measures: measures},
		{measure: reportmodel.ReportDatasetMeasure{Key: "r", Operation: "ratio", NumeratorKey: "r", DenominatorKey: "b"}, measures: map[string]reportmodel.ReportDatasetMeasure{"r": {Key: "r", Operation: "count"}, "b": measures["b"]}},
		{measure: reportmodel.ReportDatasetMeasure{Key: "r", Operation: "ratio", NumeratorKey: "a", DenominatorKey: "r"}, measures: map[string]reportmodel.ReportDatasetMeasure{"r": {Key: "r", Operation: "count"}, "a": measures["a"]}},
		{measure: reportmodel.ReportDatasetMeasure{Key: "r", Operation: "ratio", NumeratorKey: "ratio-a", DenominatorKey: "b"}, measures: measures},
		{measure: reportmodel.ReportDatasetMeasure{Key: "r", Operation: "ratio", NumeratorKey: "a", DenominatorKey: "ratio-a"}, measures: measures},
		{measure: reportmodel.ReportDatasetMeasure{Key: "r", Operation: "ratio", NumeratorKey: "a", DenominatorKey: "b"}, measures: measures},
	}
	for _, test := range ratioCases {
		validator.validateDatasetMeasure("ratio", test.measure, test.measures)
	}

	dataset.Measures = []reportmodel.ReportDatasetMeasure{
		{Key: "", Operation: "count"},
		{Key: "name", Operation: "count"},
		{Key: "name", Operation: "count"},
	}
	dataset.Dimensions = []reportmodel.ReportDatasetDimension{{Key: "name", Field: reportField("events", "name")}}
	reportDatasetValidator(dataset).validateDatasetDimensionsAndMeasures()
}

func TestReportDatasetSortFieldAndScalarHelpersCoverAllOutcomes(t *testing.T) {
	dataset := reportmodel.ReportDatasetSchema{
		Source:           reportmodel.ReportDatasetSource{ObjectKey: "event", Alias: "events"},
		Dimensions:       []reportmodel.ReportDatasetDimension{{Key: "name", Field: reportField("events", "name")}},
		Measures:         []reportmodel.ReportDatasetMeasure{{Key: "total", Operation: "count"}},
		Comparisons:      []reportmodel.ReportDatasetComparison{{Key: "change"}},
		Sort:             []reportmodel.ReportDatasetSort{{Key: "missing", Direction: "asc"}, {Key: "name", Direction: "asc"}, {Key: "total", Direction: "desc"}, {Key: "change", Direction: "bad"}},
		Limit:            -1,
		DefaultTimeGrain: "bad",
	}
	validator := reportDatasetValidator(dataset)
	validator.validateDatasetSortAndLimit()
	dataset.Limit, dataset.DefaultTimeGrain = 10001, "day"
	reportDatasetValidator(dataset).validateDatasetSortAndLimit()
	dataset.Limit, dataset.DefaultTimeGrain = 100, ""
	reportDatasetValidator(dataset).validateDatasetSortAndLimit()

	validator = reportDatasetValidator(reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "event", Alias: "events"}})
	for _, reference := range []reportmodel.ReportDatasetField{
		{}, reportField("missing", "name"), reportField("events", ""), reportField("events", "missing"), reportField("events", "id"), reportField("events", "name"),
	} {
		validator.validateDatasetField("field", reference)
	}
	for _, test := range []struct{ object, field string }{
		{"missing", "name"}, {"event", ""}, {"event", "id"}, {"event", "name"}, {"event", "missing"},
	} {
		validator.validateDatasetObjectField("field", test.object, test.field)
	}

	for _, grain := range []string{"minute", "hour", "day", "week", "month", "quarter", "year"} {
		if !reportTimeGrain(grain) {
			t.Fatalf("time grain %q rejected", grain)
		}
	}
	if reportTimeGrain("bad") {
		t.Fatal("bad time grain accepted")
	}
	for _, fieldType := range []string{"currency", "decimal", "integer", "number", "percent"} {
		if !reportNumericField(fieldType) {
			t.Fatalf("numeric field %q rejected", fieldType)
		}
	}
	if reportNumericField("text") {
		t.Fatal("text considered numeric")
	}
	if values := reportStringSet([]string{"a", "b"}); !values["a"] || !values["b"] {
		t.Fatalf("string set=%v", values)
	}
}
