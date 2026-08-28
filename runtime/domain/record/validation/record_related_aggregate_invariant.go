package validation

import (
	"fmt"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type RecordRelatedAggregateInvariant struct {
	Key              string
	RelationField    string
	TargetObjectKey  string
	Aggregate        string
	ValueField       string
	LimitField       string
	Operator         string
	StatusField      string
	IncludedStatuses []string
	ErrorCode        string
	Definition       definitionmodel.ValidationSchema
}

func RecordRelatedAggregateInvariants(object definitionmodel.ObjectSchema) ([]RecordRelatedAggregateInvariant, error) {
	fields := map[string]definitionmodel.FieldSchema{}
	for _, field := range object.Fields {
		fields[strings.TrimSpace(field.Key)] = field
	}
	result := []RecordRelatedAggregateInvariant{}
	for _, validation := range object.Validations {
		if strings.TrimSpace(validation.Type) != "related_aggregate_invariant" {
			continue
		}
		relationField := RecordConfigString(validation.Config["relation_field"])
		relation, relationOK := fields[relationField]
		aggregate := RecordConfigString(validation.Config["aggregate"])
		valueField := RecordConfigString(validation.Config["value_field"])
		limitField := RecordConfigString(validation.Config["limit_field"])
		operator := RecordConfigString(validation.Config["operator"])
		if !relationOK || strings.TrimSpace(relation.Validation.Target) == "" {
			return nil, validationError("backend.validation.related_aggregate_relation_required", "validation", validation.Key, "field", relationField)
		}
		if aggregate != "sum" && aggregate != "count" {
			return nil, validationError("backend.validation.related_aggregate_operation_invalid", "validation", validation.Key, "aggregate", aggregate)
		}
		if aggregate == "sum" {
			if _, ok := fields[valueField]; !ok || valueField == "" {
				return nil, validationError("backend.validation.related_aggregate_value_field_required", "validation", validation.Key, "field", valueField)
			}
		}
		if limitField == "" {
			return nil, validationError("backend.validation.related_aggregate_limit_field_required", "validation", validation.Key)
		}
		switch operator {
		case "lt", "lte", "eq", "gte", "gt":
		default:
			return nil, validationError("backend.validation.related_aggregate_operator_invalid", "validation", validation.Key, "operator", operator)
		}
		statusField := RecordConfigString(validation.Config["status_field"])
		included := recordStringList(validation.Config["included_statuses"])
		if len(included) > 0 {
			if _, ok := fields[statusField]; !ok || statusField == "" {
				return nil, validationError("backend.validation.related_aggregate_status_field_required", "validation", validation.Key)
			}
		}
		result = append(result, RecordRelatedAggregateInvariant{
			Key: strings.TrimSpace(validation.Key), RelationField: relationField, TargetObjectKey: strings.TrimSpace(relation.Validation.Target),
			Aggregate: aggregate, ValueField: valueField, LimitField: limitField, Operator: operator,
			StatusField: statusField, IncludedStatuses: included,
			ErrorCode: RecordPolicyMessageCode(validation.Message, "backend.policy.related_aggregate_invariant"), Definition: validation,
		})
	}
	return result, nil
}

func RecordRelatedAggregateCandidate(policy RecordRelatedAggregateInvariant, data map[string]any, operation string) (bool, error) {
	if !RecordValidationBlocks(policy.Definition) || !RecordPolicyAppliesToOperation(policy.Definition, operation) || !RecordPolicyConditionMatches(policy.Definition, data) {
		return false, nil
	}
	if len(policy.IncludedStatuses) > 0 && !RecordContainsText(policy.IncludedStatuses, strings.TrimSpace(fmt.Sprint(data[policy.StatusField]))) {
		return false, nil
	}
	if RecordIsEmptyValue(data[policy.RelationField]) {
		return false, validationError("backend.validation.related_aggregate_relation_required", "validation", policy.Key, "field", policy.RelationField)
	}
	if policy.Aggregate == "sum" && RecordIsEmptyValue(data[policy.ValueField]) {
		return false, validationError("backend.validation.related_aggregate_value_required", "validation", policy.Key, "field", policy.ValueField)
	}
	return true, nil
}
