package validation

import (
	"fmt"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func (state *validationState) validateConditionalUnique(object definitionmodel.ObjectSchema, validation definitionmodel.ValidationSchema, path string) {
	fields := make(map[string]definitionmodel.FieldSchema, len(object.Fields))
	for _, field := range object.Fields {
		fields[strings.TrimSpace(field.Key)] = field
	}
	if len(validation.Fields) == 0 {
		state.add(path+".fields", "backend.definition.conditional_unique_fields_required")
	}
	seen := map[string]bool{}
	for _, raw := range validation.Fields {
		fieldKey := strings.TrimSpace(raw)
		field, ok := fields[fieldKey]
		switch {
		case !ok || fieldKey == "":
			state.add(path+".fields", "backend.definition.conditional_unique_field_unknown: %s", raw)
		case seen[fieldKey]:
			state.add(path+".fields", "backend.definition.conditional_unique_field_duplicate: %s", fieldKey)
		case strings.TrimSpace(field.DisabledAt) != "":
			state.add(path+".fields", "backend.definition.conditional_unique_field_disabled: %s", fieldKey)
		case recordmodel.RecordIsStructuredFieldType(field.Type):
			state.add(path+".fields", "backend.definition.structured_index_unsupported: %s", fieldKey)
		}
		seen[fieldKey] = true
	}
	conditionField := cleanManifestReference(validation.Config["condition_field"])
	condition, ok := fields[conditionField]
	if !ok || conditionField == "" {
		state.add(path+".config.condition_field", "backend.definition.conditional_unique_condition_field_required")
	} else if strings.TrimSpace(condition.DisabledAt) != "" {
		state.add(path+".config.condition_field", "backend.definition.conditional_unique_condition_field_disabled: %s", conditionField)
	} else if recordmodel.RecordIsStructuredFieldType(condition.Type) {
		state.add(path+".config.condition_field", "backend.definition.structured_index_unsupported: %s", conditionField)
	}
	values := conditionalUniqueStringList(validation.Config["condition_values"])
	if len(values) == 0 {
		state.add(path+".config.condition_values", "backend.definition.conditional_unique_condition_values_required")
	}
	seenValues := map[string]bool{}
	for _, value := range values {
		switch {
		case strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value:
			state.add(path+".config.condition_values", "backend.definition.conditional_unique_condition_value_invalid")
		case seenValues[value]:
			state.add(path+".config.condition_values", "backend.definition.conditional_unique_condition_value_duplicate: %s", value)
		}
		seenValues[value] = true
	}
}

func conditionalUniqueStringList(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, fmt.Sprint(item))
		}
		return out
	default:
		return nil
	}
}
