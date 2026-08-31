package validation

import (
	"fmt"
	"strings"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
)

func (v *reportDefinitionValidator) validateExportScope() {
	scope := v.report.ExportScope
	if scope == nil {
		return
	}
	aliases := reportmodel.ReportDatasetAliasObjects(v.report.Dataset)
	if scope.Query != nil {
		mode := strings.TrimSpace(scope.Query.Mode)
		if mode == "" {
			mode = "any"
		}
		if mode != "any" && mode != "all" || len(scope.Query.Predicates) == 0 || len(scope.Query.Predicates) > 16 {
			v.issue("backend.report.export_query_definition_invalid", "export_scope.query", map[string]string{"mode": mode})
		}
		seen := map[string]bool{}
		for index, predicate := range scope.Query.Predicates {
			path := fmt.Sprintf("export_scope.query.predicates[%d]", index)
			alias, fieldKey, operator := strings.TrimSpace(predicate.Field.SourceAlias), strings.TrimSpace(predicate.Field.FieldKey), strings.TrimSpace(predicate.Operator)
			identity := alias + "\x00" + fieldKey + "\x00" + operator
			if seen[identity] || !reportExportQueryDefinitionOperator(operator) {
				v.issue("backend.report.export_query_definition_invalid", path, map[string]string{"operator": operator})
			}
			seen[identity] = true
			objectKey := aliases[alias]
			if objectKey == "" {
				v.issue("backend.report.field_reference_invalid", path+".field.source_alias", map[string]string{"alias": alias})
				continue
			}
			if _, ok := v.validateDatasetObjectField(path+".field.field_key", objectKey, fieldKey); ok {
				v.validateAudienceFieldPermission(path+".field", objectKey, fieldKey, "export")
			}
		}
	}
	if scope.Tags == nil {
		return
	}
	tags := scope.Tags
	path := "export_scope.tags"
	joinAlias, objectKey, leftAlias := strings.TrimSpace(tags.Join.Alias), strings.TrimSpace(tags.Join.ObjectKey), strings.TrimSpace(tags.Join.LeftAlias)
	if joinAlias == "" || aliases[joinAlias] != "" || objectKey == "" || tags.Join.Type != "inner" || tags.Join.Cardinality != "one_to_many" || aliases[leftAlias] == "" || !reportExportTagMatchDefinition(*tags) {
		v.issue("backend.report.export_tag_definition_invalid", path, map[string]string{"alias": joinAlias, "object": objectKey})
	}
	if _, ok := v.objects[objectKey]; !ok {
		v.issue("backend.report.source_object_not_found", path+".join.object_key", map[string]string{"object": objectKey})
	}
	if tags.TargetField.SourceAlias != tags.Join.LeftAlias || tags.TargetField.FieldKey != tags.Join.LeftField {
		v.issue("backend.report.export_tag_definition_invalid", path, map[string]string{"reason": "target/tag fields must bind the declared join"})
	}
	allowedAliases := map[string]string{joinAlias: objectKey}
	if tags.FamilyJoin == nil {
		if strings.TrimSpace(tags.TagField.SourceAlias) != joinAlias {
			v.issue("backend.report.export_tag_definition_invalid", path+".tag_field", map[string]string{"reason": "tag field must bind assignment join"})
		}
	} else {
		family := *tags.FamilyJoin
		familyAlias, familyObject := strings.TrimSpace(family.Alias), strings.TrimSpace(family.ObjectKey)
		if familyAlias == "" || familyAlias == joinAlias || aliases[familyAlias] != "" || familyObject == "" || family.Type != "inner" || family.Cardinality != "many_to_one" || strings.TrimSpace(family.LeftAlias) != joinAlias || strings.TrimSpace(family.LeftField) == "" || strings.TrimSpace(family.RightField) == "" || strings.TrimSpace(tags.TagField.SourceAlias) != familyAlias {
			v.issue("backend.report.export_tag_definition_invalid", path+".family_join", map[string]string{"alias": familyAlias, "object": familyObject})
		}
		allowedAliases[familyAlias] = familyObject
		if _, ok := v.objects[familyObject]; !ok {
			v.issue("backend.report.source_object_not_found", path+".family_join.object_key", map[string]string{"object": familyObject})
		}
		if _, ok := v.validateDatasetObjectField(path+".family_join.left_field", objectKey, family.LeftField); ok {
			v.validateAudienceFieldPermission(path+".family_join", objectKey, strings.TrimSpace(family.LeftField), "export")
		}
		if _, ok := v.validateDatasetObjectField(path+".family_join.right_field", familyObject, family.RightField); ok {
			v.validateAudienceFieldPermission(path+".family_join", familyObject, strings.TrimSpace(family.RightField), "export")
		}
	}
	leftObject := aliases[leftAlias]
	if _, ok := v.validateDatasetObjectField(path+".target_field.field_key", leftObject, tags.TargetField.FieldKey); ok {
		v.validateAudienceFieldPermission(path+".target_field", leftObject, strings.TrimSpace(tags.TargetField.FieldKey), "export")
	}
	if _, ok := v.validateDatasetObjectField(path+".join.right_field", objectKey, tags.Join.RightField); ok {
		v.validateAudienceFieldPermission(path+".join", objectKey, strings.TrimSpace(tags.Join.RightField), "export")
	}
	tagObject := allowedAliases[strings.TrimSpace(tags.TagField.SourceAlias)]
	if _, ok := v.validateDatasetObjectField(path+".tag_field.field_key", tagObject, tags.TagField.FieldKey); ok {
		v.validateAudienceFieldPermission(path+".tag_field", tagObject, strings.TrimSpace(tags.TagField.FieldKey), "export")
	}
	for index, filter := range tags.FixedFilters {
		filterPath := fmt.Sprintf("%s.fixed_filters[%d]", path, index)
		filterObject := allowedAliases[strings.TrimSpace(filter.Field.SourceAlias)]
		if filterObject == "" || !reportExportFixedDefinitionFilter(filter) {
			v.issue("backend.report.export_tag_definition_invalid", filterPath, map[string]string{"operator": filter.Operator})
		}
		if _, ok := v.validateDatasetObjectField(filterPath+".field.field_key", filterObject, filter.Field.FieldKey); ok {
			v.validateAudienceFieldPermission(filterPath+".field", filterObject, strings.TrimSpace(filter.Field.FieldKey), "export")
		}
	}
}

func reportExportTagMatchDefinition(tags reportmodel.ReportExportTagScopeSchema) bool {
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

func reportExportQueryDefinitionOperator(operator string) bool {
	switch strings.TrimSpace(operator) {
	case "contains", "starts_with", "eq":
		return true
	default:
		return false
	}
}

func reportExportFixedDefinitionFilter(filter reportmodel.ReportDatasetFilter) bool {
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
