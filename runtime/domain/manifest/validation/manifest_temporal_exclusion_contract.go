package validation

import (
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func (state *validationState) validateTemporalExclusion(object definitionmodel.ObjectSchema, validation definitionmodel.ValidationSchema, path string) {
	startField := cleanManifestReference(validation.Config["start_field"])
	endField := cleanManifestReference(validation.Config["end_field"])
	if startField == "" || endField == "" {
		state.add(path+".config", "backend.validation.temporal_exclusion_range_fields_required")
	}
	if startField != "" && startField == endField {
		state.add(path+".config.end_field", "backend.validation.temporal_exclusion_range_fields_distinct")
	}
	fieldMap := map[string]definitionmodel.FieldSchema{}
	for _, field := range object.Fields {
		fieldMap[strings.TrimSpace(field.Key)] = field
	}
	for key, fieldName := range map[string]string{"start_field": startField, "end_field": endField} {
		if fieldName == "" {
			continue
		}
		field, ok := fieldMap[fieldName]
		if !ok {
			state.add(path+".config."+key, "backend.validation.temporal_exclusion_field_unknown: %s", fieldName)
			continue
		}
		if field.Type != "date" && field.Type != "datetime" {
			state.add(path+".config."+key, "backend.validation.temporal_exclusion_field_type_invalid: %s", field.Type)
		}
	}
	if start, startOK := fieldMap[startField]; startOK {
		if end, endOK := fieldMap[endField]; endOK && start.Type != end.Type {
			state.add(path+".config", "backend.validation.temporal_exclusion_range_type_mismatch: %s/%s", start.Type, end.Type)
		}
	}
	scopeFields := validationStringList(validation.Config["scope_fields"])
	if len(scopeFields) == 0 {
		state.add(path+".config.scope_fields", "backend.validation.temporal_exclusion_scope_fields_required")
	}
	seen := map[string]bool{}
	for _, fieldName := range scopeFields {
		fieldName = strings.TrimSpace(fieldName)
		if seen[fieldName] {
			state.add(path+".config.scope_fields", "backend.validation.temporal_exclusion_scope_field_duplicate: %s", fieldName)
		}
		seen[fieldName] = true
		if _, ok := fieldMap[fieldName]; !ok {
			state.add(path+".config.scope_fields", "backend.validation.temporal_exclusion_field_unknown: %s", fieldName)
		}
	}
	statusField := cleanManifestReference(validation.Config["status_field"])
	if statusField != "" {
		if _, ok := fieldMap[statusField]; !ok {
			state.add(path+".config.status_field", "backend.validation.temporal_exclusion_field_unknown: %s", statusField)
		}
	}
	if len(validationStringList(validation.Config["excluded_statuses"])) > 0 && statusField == "" {
		state.add(path+".config.excluded_statuses", "backend.validation.temporal_exclusion_status_field_required")
	}
	for _, oldKey := range []string{"ignored_statuses", "cancelled_statuses"} {
		if _, exists := validation.Config[oldKey]; exists {
			state.add(path+".config."+oldKey, "backend.definition.validation_config_unsupported: %s", oldKey)
		}
	}
}
