package validation

import (
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func (state *validationState) validateRelatedAggregateInvariant(object definitionmodel.ObjectSchema, validation definitionmodel.ValidationSchema, path string) {
	fields := map[string]definitionmodel.FieldSchema{}
	for _, field := range object.Fields {
		fields[strings.TrimSpace(field.Key)] = field
	}
	relationField := cleanManifestReference(validation.Config["relation_field"])
	relation, relationOK := fields[relationField]
	if !relationOK || relation.Type != "relation" || strings.TrimSpace(relation.Validation.Target) == "" {
		state.add(path+".config.relation_field", "backend.validation.related_aggregate_relation_required")
		return
	}
	target, targetOK := state.objects[strings.TrimSpace(relation.Validation.Target)]
	if !targetOK {
		state.add(path+".config.relation_field", "backend.validation.related_aggregate_target_unknown: %s", relation.Validation.Target)
		return
	}
	aggregate := cleanManifestReference(validation.Config["aggregate"])
	if aggregate != "sum" && aggregate != "count" {
		state.add(path+".config.aggregate", "backend.validation.related_aggregate_operation_invalid: %s", aggregate)
	}
	valueField := cleanManifestReference(validation.Config["value_field"])
	valueSchema, valueOK := fields[valueField]
	if aggregate == "sum" {
		if !valueOK {
			state.add(path+".config.value_field", "backend.validation.related_aggregate_value_field_required")
		} else if !relatedAggregateNumericType(valueSchema.Type) {
			state.add(path+".config.value_field", "backend.validation.related_aggregate_value_type_invalid: %s", valueSchema.Type)
		}
	} else if valueField != "" {
		state.add(path+".config.value_field", "backend.validation.related_aggregate_count_value_forbidden")
	}
	limitField := cleanManifestReference(validation.Config["limit_field"])
	var limitSchema definitionmodel.FieldSchema
	for _, field := range target.Fields {
		if strings.TrimSpace(field.Key) == limitField {
			limitSchema = field
			break
		}
	}
	if strings.TrimSpace(limitSchema.Key) == "" {
		state.add(path+".config.limit_field", "backend.validation.related_aggregate_limit_field_required: %s", limitField)
	} else if !relatedAggregateNumericType(limitSchema.Type) {
		state.add(path+".config.limit_field", "backend.validation.related_aggregate_limit_type_invalid: %s", limitSchema.Type)
	} else if aggregate == "count" && limitSchema.Type == "currency" {
		state.add(path+".config.limit_field", "backend.validation.related_aggregate_count_limit_type_invalid")
	} else if aggregate == "sum" && valueOK && !relatedAggregateTypesCompatible(valueSchema.Type, limitSchema.Type) {
		state.add(path+".config", "backend.validation.related_aggregate_type_mismatch: %s/%s", valueSchema.Type, limitSchema.Type)
	}
	switch cleanManifestReference(validation.Config["operator"]) {
	case "lt", "lte", "eq", "gte", "gt":
	default:
		state.add(path+".config.operator", "backend.validation.related_aggregate_operator_invalid")
	}
	statusField := cleanManifestReference(validation.Config["status_field"])
	if len(validationStringList(validation.Config["included_statuses"])) > 0 {
		if _, ok := fields[statusField]; !ok || statusField == "" {
			state.add(path+".config.status_field", "backend.validation.related_aggregate_status_field_required")
		}
	}
}

func relatedAggregateNumericType(fieldType string) bool {
	switch strings.TrimSpace(fieldType) {
	case "number", "percent", "currency":
		return true
	default:
		return false
	}
}

func relatedAggregateTypesCompatible(left, right string) bool {
	left, right = strings.TrimSpace(left), strings.TrimSpace(right)
	if left == right {
		return true
	}
	return (left == "number" || left == "percent") && (right == "number" || right == "percent")
}
