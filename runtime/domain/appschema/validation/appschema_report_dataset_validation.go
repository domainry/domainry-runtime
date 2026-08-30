package validation

import (
	"fmt"
	"strconv"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

func (v *reportDefinitionValidator) validateDatasetReferences() {
	v.validateDatasetJoins()
	v.validateDatasetFilters()
	v.validateDatasetPredicates("query_predicates", v.report.Dataset.QueryPredicates)
	v.validateDatasetPredicates("tag_predicates", v.report.Dataset.TagPredicates)
	v.validateDatasetDimensionsAndMeasures()
	v.validateDatasetSortAndLimit()
	if privacy := v.report.Dataset.Privacy; privacy != nil {
		if privacy.MinimumGroupSize < 2 || privacy.MinimumGroupSize > 1000 {
			v.issue("backend.report.privacy_invalid", "dataset.privacy.minimum_group_size", map[string]string{"actual": fmt.Sprint(privacy.MinimumGroupSize)})
		}
		v.validateDatasetField("dataset.privacy.entity_field", privacy.EntityField)
	}
	v.validateDatasetComparisons()
	v.validateDatasetAnalyses()
	v.validateExportScope()
	for _, diagnostic := range reportcontract.ReportDatasetIndexDiagnostics(v.report, v.objects) {
		v.issue(diagnostic.Code, diagnostic.Path, map[string]string{
			"object": diagnostic.ObjectKey, "field_key": diagnostic.FieldKey,
			"usage": diagnostic.Usage, "recommended_index": strings.Join(diagnostic.Fields, ","),
		})
	}
	if _, err := reportcontract.BuildReportDatasetPlan(v.report); err != nil {
		// BuildReportDatasetPlan owns this contract and returns only the typed
		// authoring diagnostic; retaining a second untyped fallback would hide a
		// broken internal boundary behind an unreachable compatibility branch.
		planErr := err.(*reportmodel.ReportDatasetPlanError)
		v.issue(planErr.Code, planErr.Path, planErr.Params)
	}
}

func (v *reportDefinitionValidator) validateDatasetAnalyses() {
	keys := map[string]bool{}
	for index, analysis := range v.report.Dataset.Analyses {
		path := fmt.Sprintf("dataset.analyses[%d]", index)
		key := strings.TrimSpace(analysis.Key)
		if key == "" || keys[key] {
			v.issue("backend.report.analysis_invalid", path+".key", map[string]string{"key": key})
		}
		keys[key] = true
		v.validateDatasetField(path+".entity_field", analysis.EntityField)
		timeField, timeOK := v.validateDatasetField(path+".time_field", analysis.TimeField)
		if timeOK && timeField.Type != "date" && timeField.Type != "datetime" {
			v.issue("backend.report.analysis_invalid", path+".time_field", map[string]string{"reason": "time_field must be date or datetime"})
		}
		switch analysis.Type {
		case "funnel":
			if analysis.EventField == nil {
				v.issue("backend.report.analysis_invalid", path+".event_field", map[string]string{"reason": "funnel requires event_field"})
			} else {
				v.validateDatasetField(path+".event_field", *analysis.EventField)
			}
			if len(analysis.Stages) < 2 || analysis.WindowSeconds < 0 || analysis.WindowSeconds > 31536000 {
				v.issue("backend.report.analysis_invalid", path, map[string]string{"reason": "funnel requires at least two stages and a valid window"})
			}
			stageKeys := map[string]bool{}
			for stageIndex, stage := range analysis.Stages {
				stagePath := fmt.Sprintf("%s.stages[%d]", path, stageIndex)
				stageKey := strings.TrimSpace(stage.Key)
				if stageKey == "" || stageKeys[stageKey] || len(stage.Values) == 0 {
					v.issue("backend.report.analysis_invalid", stagePath, map[string]string{"stage": stageKey})
				}
				stageKeys[stageKey] = true
			}
		case "cohort_retention":
			cohortGrain, periodGrain := strings.TrimSpace(analysis.CohortGrain), strings.TrimSpace(analysis.PeriodGrain)
			if cohortGrain == "" {
				cohortGrain = "month"
			}
			if periodGrain == "" {
				periodGrain = cohortGrain
			}
			if !reportCohortTimeGrain(cohortGrain) || !reportCohortTimeGrain(periodGrain) || analysis.MaximumPeriods < 0 || analysis.MaximumPeriods > 120 || analysis.EventField != nil || len(analysis.Stages) != 0 {
				v.issue("backend.report.analysis_invalid", path, map[string]string{"reason": "invalid cohort retention contract"})
			}
		default:
			v.issue("backend.report.analysis_invalid", path+".type", map[string]string{"actual": analysis.Type})
		}
	}
}

func reportCohortTimeGrain(value string) bool {
	switch strings.TrimSpace(value) {
	case "day", "week", "month", "quarter", "year":
		return true
	default:
		return false
	}
}

func (v *reportDefinitionValidator) validateDatasetComparisons() {
	dimensions := map[string]reportmodel.ReportDatasetDimension{}
	for _, dimension := range v.report.Dataset.Dimensions {
		dimensions[strings.TrimSpace(dimension.Key)] = dimension
	}
	measures := map[string]bool{}
	for _, measure := range v.report.Dataset.Measures {
		measures[strings.TrimSpace(measure.Key)] = true
	}
	keys := map[string]bool{}
	for index, comparison := range v.report.Dataset.Comparisons {
		path := fmt.Sprintf("dataset.comparisons[%d]", index)
		key := strings.TrimSpace(comparison.Key)
		dimension, dimensionOK := dimensions[strings.TrimSpace(comparison.TimeDimensionKey)]
		grain := strings.TrimSpace(dimension.TimeGrain)
		if grain == "" {
			grain = strings.TrimSpace(v.report.Dataset.DefaultTimeGrain)
		}
		if key == "" || keys[key] || dimensions[key].Key != "" || measures[key] {
			v.issue("backend.report.comparison_invalid", path+".key", map[string]string{"key": key})
		}
		keys[key] = true
		if comparison.Type != "period_over_period" || !dimensionOK || !reportTimeGrain(grain) || !measures[strings.TrimSpace(comparison.MeasureKey)] {
			v.issue("backend.report.comparison_invalid", path, map[string]string{"type": comparison.Type, "time_dimension_key": comparison.TimeDimensionKey, "measure_key": comparison.MeasureKey})
		}
		if comparison.Operation != "difference" && comparison.Operation != "ratio" && comparison.Operation != "percent_change" {
			v.issue("backend.report.comparison_invalid", path+".operation", map[string]string{"actual": comparison.Operation})
		}
		if comparison.OffsetPeriods < 0 || comparison.OffsetPeriods > 100 {
			v.issue("backend.report.comparison_invalid", path+".offset_periods", map[string]string{"actual": fmt.Sprint(comparison.OffsetPeriods)})
		}
	}
}

func (v *reportDefinitionValidator) validateDatasetJoins() {
	dataset := v.report.Dataset
	aliases := map[string]string{}
	rootAlias, rootObject := strings.TrimSpace(dataset.Source.Alias), strings.TrimSpace(dataset.Source.ObjectKey)
	if rootAlias == "" {
		v.issue("backend.report.dataset_alias_invalid", "dataset.source.alias", map[string]string{"alias": rootAlias})
	}
	if sourceType := valueOrDefault(dataset.Source.SourceType, "records"); sourceType != "records" && sourceType != "snapshot" {
		v.issue("backend.report.dataset_source_invalid", "dataset.source.source_type", map[string]string{"actual": sourceType})
	}
	if rootAlias != "" {
		aliases[rootAlias] = rootObject
	}
	for index, join := range dataset.Joins {
		path := fmt.Sprintf("dataset.joins[%d]", index)
		alias, objectKey, leftAlias := strings.TrimSpace(join.Alias), strings.TrimSpace(join.ObjectKey), strings.TrimSpace(join.LeftAlias)
		if alias == "" || aliases[alias] != "" {
			v.issue("backend.report.dataset_alias_invalid", path+".alias", map[string]string{"alias": alias})
		}
		leftObjectKey, leftExists := aliases[leftAlias]
		if !leftExists {
			v.issue("backend.report.join_invalid", path+".left_alias", map[string]string{"alias": leftAlias, "reason": "left alias must reference the root or an earlier join"})
		}
		if join.Type != "inner" && join.Type != "left" {
			v.issue("backend.report.join_invalid", path+".type", map[string]string{"actual": join.Type, "allowed": "inner,left"})
		}
		if sourceType := valueOrDefault(join.SourceType, "records"); sourceType != "records" && sourceType != "snapshot" {
			v.issue("backend.report.dataset_source_invalid", path+".source_type", map[string]string{"actual": sourceType})
		}
		switch strings.TrimSpace(join.Cardinality) {
		case "one_to_one", "many_to_one", "one_to_many":
		default:
			v.issue("backend.report.join_cardinality_invalid", path+".cardinality", map[string]string{"actual": join.Cardinality, "allowed": "one_to_one,many_to_one,one_to_many"})
		}
		equalities := join.Equalities()
		mixedShape := len(join.FieldEqualities) > 0 && (strings.TrimSpace(join.LeftField) != "" || strings.TrimSpace(join.RightField) != "")
		if mixedShape || len(equalities) == 0 || len(equalities) > 8 {
			v.issue("backend.report.join_invalid", path+".field_equalities", map[string]string{"reason": "declare either one legacy field pair or 1..8 field_equalities"})
		}
		seenEqualities := map[string]bool{}
		for equalityIndex, equality := range equalities {
			equalityPath := fmt.Sprintf("%s.field_equalities[%d]", path, equalityIndex)
			if len(join.FieldEqualities) == 0 {
				equalityPath = path
			}
			pairKey := strings.TrimSpace(equality.LeftField) + "\x00" + strings.TrimSpace(equality.RightField)
			if seenEqualities[pairKey] {
				v.issue("backend.report.join_invalid", equalityPath, map[string]string{"reason": "duplicate field equality"})
			}
			seenEqualities[pairKey] = true
			v.validateDatasetObjectField(equalityPath+".left_field", leftObjectKey, equality.LeftField)
			v.validateDatasetObjectField(equalityPath+".right_field", objectKey, equality.RightField)
		}
		if alias != "" {
			aliases[alias] = objectKey
		}
	}
}

func (v *reportDefinitionValidator) validateDatasetFilters() {
	for index, filter := range v.report.Dataset.Filters {
		path := fmt.Sprintf("dataset.filters[%d]", index)
		v.validateDatasetFilter(path, filter)
	}
}

func (v *reportDefinitionValidator) validateDatasetPredicates(kind string, predicates []reportmodel.ReportDatasetPredicate) {
	seen := map[string]bool{}
	for index, predicate := range predicates {
		path := fmt.Sprintf("dataset.%s[%d]", kind, index)
		key := strings.TrimSpace(predicate.Key)
		if key == "" || seen[key] || len(predicate.Filters) == 0 || len(predicate.Filters) > 16 {
			v.issue("backend.report.predicate_invalid", path, map[string]string{"key": key, "reason": "unique key and 1..16 filters are required"})
		}
		seen[key] = true
		for filterIndex, filter := range predicate.Filters {
			v.validateDatasetFilter(fmt.Sprintf("%s.filters[%d]", path, filterIndex), filter)
		}
	}
}

func (v *reportDefinitionValidator) validateDatasetFilter(path string, filter reportmodel.ReportDatasetFilter) {
	v.validateDatasetField(path+".field", filter.Field)
	operator := strings.TrimSpace(filter.Operator)
	allowed := reportStringSet([]string{"eq", "ne", "gt", "gte", "lt", "lte", "in", "not_in", "contains", "starts_with", "ends_with", "is_null", "not_null", "between"})
	if !allowed[operator] {
		v.issue("backend.report.filter_invalid", path+".operator", map[string]string{"actual": operator})
	}
	if (operator == "in" || operator == "not_in") && (len(filter.Values) == 0 || len(filter.Values) > 64) || operator == "between" && len(filter.Values) != 2 {
		v.issue("backend.report.filter_invalid", path+".values", map[string]string{"reason": "operator values are invalid"})
	}
}

func (v *reportDefinitionValidator) validateDatasetDimensionsAndMeasures() {
	keys := map[string]bool{}
	for index, dimension := range v.report.Dataset.Dimensions {
		path := fmt.Sprintf("dataset.dimensions[%d]", index)
		key := strings.TrimSpace(dimension.Key)
		if key == "" || keys[key] {
			v.issue("backend.report.dimension_invalid", path+".key", map[string]string{"key": key})
		}
		keys[key] = true
		field, ok := v.validateDatasetField(path+".field", dimension.Field)
		grain := strings.TrimSpace(dimension.TimeGrain)
		if grain != "" && !reportTimeGrain(grain) {
			v.issue("backend.report.time_grain_invalid", path+".time_grain", map[string]string{"actual": grain})
		} else if grain != "" && ok && field.Type != "date" && field.Type != "datetime" {
			v.issue("backend.report.time_grain_invalid", path+".time_grain", map[string]string{"actual": grain, "reason": "time grain requires date or datetime"})
		}
	}
	measureKeys := map[string]reportmodel.ReportDatasetMeasure{}
	for _, measure := range v.report.Dataset.Measures {
		measureKeys[strings.TrimSpace(measure.Key)] = measure
	}
	for index, measure := range v.report.Dataset.Measures {
		path := fmt.Sprintf("dataset.measures[%d]", index)
		key := strings.TrimSpace(measure.Key)
		if key == "" || keys[key] {
			v.issue("backend.report.measure_invalid", path+".key", map[string]string{"key": key})
		}
		keys[key] = true
		v.validateDatasetMeasure(path, measure, measureKeys)
	}
}

func (v *reportDefinitionValidator) validateDatasetMeasure(path string, measure reportmodel.ReportDatasetMeasure, measures map[string]reportmodel.ReportDatasetMeasure) {
	operation := strings.TrimSpace(measure.Operation)
	allowed := reportStringSet([]string{"count", "distinct_count", "sum", "avg", "min", "max", "ratio", "duration", "percentile"})
	if !allowed[operation] {
		v.issue("backend.report.measure_invalid", path+".operation", map[string]string{"actual": operation})
		return
	}
	field, hasField := definitionmodel.FieldSchema{}, false
	if measure.Field != nil {
		field, hasField = v.validateDatasetField(path+".field", *measure.Field)
	}
	switch operation {
	case "count":
		if measure.Field != nil {
			v.issue("backend.report.measure_invalid", path+".field", map[string]string{"reason": "count does not accept field"})
		}
		if alias := strings.TrimSpace(measure.SourceAlias); alias != "" && reportmodel.ReportDatasetAliasObjects(v.report.Dataset)[alias] == "" {
			v.issue("backend.report.measure_invalid", path+".source_alias", map[string]string{"reason": "count source_alias must reference a dataset alias", "alias": alias})
		}
	case "distinct_count", "min", "max":
		if measure.Field == nil {
			v.issue("backend.report.measure_invalid", path+".field", map[string]string{"reason": "operation requires field"})
		}
	case "sum", "avg", "percentile":
		if measure.Field == nil || hasField && !reportNumericField(field.Type) {
			v.issue("backend.report.measure_invalid", path+".field", map[string]string{"reason": "operation requires a numeric or currency field"})
		}
		if operation == "percentile" {
			percentile, err := strconv.ParseFloat(strings.TrimSpace(measure.Percentile), 64)
			if err != nil || percentile <= 0 || percentile > 100 {
				v.issue("backend.report.measure_invalid", path+".percentile", map[string]string{"reason": "percentile must be greater than 0 and at most 100"})
			}
		}
	case "duration":
		if measure.StartField == nil || measure.EndField == nil {
			v.issue("backend.report.measure_invalid", path, map[string]string{"reason": "duration requires start_field and end_field"})
			break
		}
		start, startOK := v.validateDatasetField(path+".start_field", *measure.StartField)
		end, endOK := v.validateDatasetField(path+".end_field", *measure.EndField)
		if startOK && endOK && (start.Type != end.Type || start.Type != "date" && start.Type != "datetime") {
			v.issue("backend.report.measure_invalid", path, map[string]string{"reason": "duration fields must use the same date or datetime type"})
		}
	case "ratio":
		numerator, numeratorOK := measures[strings.TrimSpace(measure.NumeratorKey)]
		denominator, denominatorOK := measures[strings.TrimSpace(measure.DenominatorKey)]
		if !numeratorOK || !denominatorOK || numerator.Key == measure.Key || denominator.Key == measure.Key || numerator.Operation == "ratio" || denominator.Operation == "ratio" {
			v.issue("backend.report.measure_invalid", path, map[string]string{"reason": "ratio requires two non-ratio measure keys"})
		}
	}
	if operation != "count" && strings.TrimSpace(measure.SourceAlias) != "" {
		v.issue("backend.report.measure_invalid", path+".source_alias", map[string]string{"reason": "source_alias is only valid for count"})
	}
}

func (v *reportDefinitionValidator) validateDatasetSortAndLimit() {
	dataset := v.report.Dataset
	keys := map[string]bool{}
	for _, dimension := range dataset.Dimensions {
		keys[strings.TrimSpace(dimension.Key)] = true
	}
	for _, measure := range dataset.Measures {
		keys[strings.TrimSpace(measure.Key)] = true
	}
	for _, comparison := range dataset.Comparisons {
		keys[strings.TrimSpace(comparison.Key)] = true
	}
	for index, sortRule := range dataset.Sort {
		path := fmt.Sprintf("dataset.sort[%d]", index)
		if !keys[strings.TrimSpace(sortRule.Key)] || sortRule.Direction != "asc" && sortRule.Direction != "desc" {
			v.issue("backend.report.sort_invalid", path, map[string]string{"key": sortRule.Key, "direction": sortRule.Direction})
		}
	}
	if dataset.Limit < 0 || dataset.Limit > 10000 {
		v.issue("backend.report.limit_invalid", "dataset.limit", map[string]string{"actual": fmt.Sprint(dataset.Limit), "maximum": "10000"})
	}
	if grain := strings.TrimSpace(dataset.DefaultTimeGrain); grain != "" && !reportTimeGrain(grain) {
		v.issue("backend.report.time_grain_invalid", "dataset.time_grain", map[string]string{"actual": grain})
	}
}

func (v *reportDefinitionValidator) validateDatasetField(path string, reference reportmodel.ReportDatasetField) (definitionmodel.FieldSchema, bool) {
	alias := strings.TrimSpace(reference.SourceAlias)
	fieldKey := strings.TrimSpace(reference.FieldKey)
	objectKey := reportmodel.ReportDatasetAliasObjects(v.report.Dataset)[alias]
	if alias == "" || objectKey == "" {
		v.issue("backend.report.field_reference_invalid", path+".source_alias", map[string]string{"alias": alias})
		return definitionmodel.FieldSchema{}, false
	}
	if fieldKey == "" {
		v.issue("backend.report.field_reference_invalid", path+".field_key", map[string]string{"alias": alias, "field_key": fieldKey})
		return definitionmodel.FieldSchema{}, false
	}
	field, ok := v.validateDatasetObjectField(path+".field_key", objectKey, reference.FieldKey)
	if ok {
		v.validateAudienceFieldPermission(path, objectKey, strings.TrimSpace(reference.FieldKey), "read")
	}
	return field, ok
}

func (v *reportDefinitionValidator) validateDatasetObjectField(path, objectKey, rawField string) (definitionmodel.FieldSchema, bool) {
	fieldKey := strings.TrimSpace(rawField)
	object, exists := v.objects[strings.TrimSpace(objectKey)]
	if !exists || fieldKey == "" {
		v.issue("backend.report.field_not_found", path, map[string]string{"object": objectKey, "field_key": fieldKey})
		return definitionmodel.FieldSchema{}, false
	}
	if fieldKey == "id" {
		return definitionmodel.FieldSchema{Key: "id", Type: "text"}, true
	}
	for _, field := range object.Fields {
		if strings.TrimSpace(field.Key) == fieldKey {
			return field, true
		}
	}
	v.issue("backend.report.field_not_found", path, map[string]string{"object": objectKey, "field_key": fieldKey})
	return definitionmodel.FieldSchema{}, false
}

func reportTimeGrain(value string) bool {
	return reportStringSet([]string{"minute", "hour", "day", "week", "month", "quarter", "year"})[strings.TrimSpace(value)]
}

func reportNumericField(value string) bool {
	switch strings.TrimSpace(value) {
	case "currency", "decimal", "integer", "number", "percent":
		return true
	default:
		return false
	}
}

func reportStringSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}
