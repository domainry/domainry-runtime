package validation

import (
	"fmt"
	"strings"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
)

func (state *validationState) validateReportExportScope(path string, report reportmodel.ReportSchema) {
	if report.ExportScope == nil {
		return
	}
	aliases := reportmodel.ReportDatasetAliasObjects(report.Dataset)
	checkExportField := func(fieldPath string, reference reportmodel.ReportDatasetField, extraAliases map[string]string) {
		alias, fieldKey := strings.TrimSpace(reference.SourceAlias), strings.TrimSpace(reference.FieldKey)
		objectKey := aliases[alias]
		if objectKey == "" {
			objectKey = extraAliases[alias]
		}
		if objectKey == "" || fieldKey == "" || fieldKey != "id" && state.fields[objectKey][fieldKey].Key == "" {
			state.add(fieldPath, "backend.report.export_scope_field_invalid: %s.%s", alias, fieldKey)
			return
		}
	}
	if query := report.ExportScope.Query; query != nil {
		mode := strings.TrimSpace(query.Mode)
		if mode == "" {
			mode = "any"
		}
		if mode != "any" && mode != "all" || len(query.Predicates) == 0 || len(query.Predicates) > 16 {
			state.add(path+".export_scope.query", "backend.report.export_query_definition_invalid")
		}
		seen := map[string]bool{}
		for index, predicate := range query.Predicates {
			predicatePath := fmt.Sprintf("%s.export_scope.query.predicates[%d]", path, index)
			operator := strings.TrimSpace(predicate.Operator)
			identity := strings.TrimSpace(predicate.Field.SourceAlias) + "\x00" + strings.TrimSpace(predicate.Field.FieldKey) + "\x00" + operator
			if seen[identity] || !manifestReportExportQueryOperator(operator) {
				state.add(predicatePath, "backend.report.export_query_definition_invalid")
			}
			seen[identity] = true
			checkExportField(predicatePath+".field", predicate.Field, nil)
		}
	}
	tags := report.ExportScope.Tags
	if tags == nil {
		return
	}
	extraAliases := map[string]string{strings.TrimSpace(tags.Join.Alias): strings.TrimSpace(tags.Join.ObjectKey)}
	if tags.Join.Type != "inner" || tags.Join.Cardinality != "one_to_many" || aliases[strings.TrimSpace(tags.Join.Alias)] != "" || aliases[strings.TrimSpace(tags.Join.LeftAlias)] == "" || state.objects[strings.TrimSpace(tags.Join.ObjectKey)].Key == "" ||
		strings.TrimSpace(tags.TargetField.SourceAlias) != strings.TrimSpace(tags.Join.LeftAlias) || strings.TrimSpace(tags.TargetField.FieldKey) != strings.TrimSpace(tags.Join.LeftField) || !manifestReportExportTagMatchDefinition(*tags) {
		state.add(path+".export_scope.tags", "backend.report.export_tag_definition_invalid")
	}
	if tags.FamilyJoin == nil {
		if strings.TrimSpace(tags.TagField.SourceAlias) != strings.TrimSpace(tags.Join.Alias) {
			state.add(path+".export_scope.tags.tag_field", "backend.report.export_tag_definition_invalid")
		}
	} else {
		family := *tags.FamilyJoin
		familyAlias, familyObject := strings.TrimSpace(family.Alias), strings.TrimSpace(family.ObjectKey)
		if familyAlias == "" || familyAlias == strings.TrimSpace(tags.Join.Alias) || aliases[familyAlias] != "" || state.objects[familyObject].Key == "" || family.Type != "inner" || family.Cardinality != "many_to_one" || strings.TrimSpace(family.LeftAlias) != strings.TrimSpace(tags.Join.Alias) || strings.TrimSpace(family.LeftField) == "" || strings.TrimSpace(family.RightField) == "" || strings.TrimSpace(tags.TagField.SourceAlias) != familyAlias {
			state.add(path+".export_scope.tags.family_join", "backend.report.export_tag_definition_invalid")
		}
		extraAliases[familyAlias] = familyObject
		checkExportField(path+".export_scope.tags.family_join.left_field", reportmodel.ReportDatasetField{SourceAlias: tags.Join.Alias, FieldKey: family.LeftField}, extraAliases)
		checkExportField(path+".export_scope.tags.family_join.right_field", reportmodel.ReportDatasetField{SourceAlias: family.Alias, FieldKey: family.RightField}, extraAliases)
	}
	checkExportField(path+".export_scope.tags.target_field", tags.TargetField, extraAliases)
	checkExportField(path+".export_scope.tags.join.right_field", reportmodel.ReportDatasetField{SourceAlias: tags.Join.Alias, FieldKey: tags.Join.RightField}, extraAliases)
	checkExportField(path+".export_scope.tags.tag_field", tags.TagField, extraAliases)
	for index, filter := range tags.FixedFilters {
		filterPath := fmt.Sprintf("%s.export_scope.tags.fixed_filters[%d]", path, index)
		if extraAliases[strings.TrimSpace(filter.Field.SourceAlias)] == "" || !manifestReportExportFixedFilter(filter) {
			state.add(filterPath, "backend.report.export_tag_definition_invalid")
		}
		checkExportField(filterPath+".field", filter.Field, extraAliases)
	}
}

func manifestReportExportTagMatchDefinition(tags reportmodel.ReportExportTagScopeSchema) bool {
	legacy, defaultMode := strings.TrimSpace(tags.Match), strings.TrimSpace(tags.DefaultMatchMode)
	if legacy != "" {
		return len(tags.AllowedMatchModes) == 0 && defaultMode == "" && (legacy == "any" || legacy == "all")
	}
	if len(tags.AllowedMatchModes) == 0 {
		return defaultMode == ""
	}
	seen := map[string]bool{}
	for _, raw := range tags.AllowedMatchModes {
		mode := strings.TrimSpace(raw)
		if mode != "any" && mode != "all" || seen[mode] {
			return false
		}
		seen[mode] = true
	}
	if defaultMode == "" {
		return len(seen) == 1
	}
	return seen[defaultMode]
}

func manifestReportExportQueryOperator(value string) bool {
	switch strings.TrimSpace(value) {
	case "contains", "starts_with", "eq":
		return true
	default:
		return false
	}
}

func manifestReportExportFixedFilter(filter reportmodel.ReportDatasetFilter) bool {
	switch strings.TrimSpace(filter.Operator) {
	case "eq", "ne", "gt", "gte", "lt", "lte":
		return len(filter.Values) == 0
	case "in", "not_in":
		return filter.Value == nil && len(filter.Values) > 0 && len(filter.Values) <= 32
	case "is_null", "not_null":
		return filter.Value == nil && len(filter.Values) == 0
	default:
		return false
	}
}
