package validation

import (
	"fmt"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func RecordDuplicateIdentityFields(object definitionmodel.ObjectSchema) []definitionmodel.FieldSchema {
	fields := []definitionmodel.FieldSchema{}
	for _, field := range object.Fields {
		if field.Unique || field.Key == "email" || field.Key == "phone" {
			fields = append(fields, field)
		}
	}
	// Display names and titles may repeat, including recurring course sessions.
	// Their uniqueness must be declared by the field contract above.
	return fields
}

func RecordPolicyAllowsMissingRelatedLookup(validation definitionmodel.ValidationSchema) bool {
	for _, key := range []string{"allow_missing", "missing_allowed", "optional"} {
		if value, ok := policyBoolAny(validation.Config[key]); ok {
			return value
		}
	}
	return false
}

func RecordPolicyValuesEqual(left, right any) bool {
	if leftNumber, ok := policyNumericAny(left); ok {
		if rightNumber, rightOK := policyNumericAny(right); rightOK {
			return leftNumber == rightNumber
		}
	}
	return strings.TrimSpace(fmt.Sprint(left)) == strings.TrimSpace(fmt.Sprint(right))
}

func RecordRelatedNumericValueField(validation definitionmodel.ValidationSchema) string {
	for _, key := range []string{"value_field", "required_field", "amount_field", "field"} {
		if field := policyConfigString(validation.Config[key]); field != "" {
			return field
		}
	}
	if field := strings.TrimSpace(validation.FieldKey); field != "" {
		return field
	}
	if len(validation.Fields) > 0 {
		return strings.TrimSpace(validation.Fields[0])
	}
	return ""
}

func RecordRelatedNumericTargetField(validation definitionmodel.ValidationSchema) string {
	for _, key := range []string{"target_field", "numeric_field", "related_field"} {
		if field := policyConfigString(validation.Config[key]); field != "" {
			return field
		}
	}
	return ""
}

func RecordRelatedNumericLimitValueInScope(validation definitionmodel.ValidationSchema, value float64) bool {
	for _, key := range []string{"value_exclusive_min", "exclusive_min", "gt"} {
		if threshold, ok := policyNumericAny(validation.Config[key]); ok && value <= threshold {
			return false
		}
	}
	for _, key := range []string{"value_min", "min", "gte"} {
		if threshold, ok := policyNumericAny(validation.Config[key]); ok && value < threshold {
			return false
		}
	}
	return true
}

func RecordRelatedNumericLimitSatisfied(targetValue, requiredValue float64, operator string) bool {
	switch operator {
	case "gt", ">":
		return targetValue > requiredValue
	case "lte", "le", "<=":
		return targetValue <= requiredValue
	case "lt", "<":
		return targetValue < requiredValue
	case "eq", "equal", "==":
		return targetValue == requiredValue
	default:
		return targetValue >= requiredValue
	}
}
