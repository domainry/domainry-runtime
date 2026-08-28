package validation

import (
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func RecordPolicyConditionMatches(validation definitionmodel.ValidationSchema, data map[string]any) bool {
	fieldKey := policyConfigString(validation.Config["when_field"])
	if fieldKey != "" && !policyConditionFieldMatches(fieldKey, validation.Config["when_in"], validation.Config["values"], validation.Config["value"], data) {
		return false
	}
	for _, condition := range policyMapSlice(validation.Config["conditions"]) {
		if !policyConditionItemMatches(condition, data) {
			return false
		}
	}
	for _, condition := range policyMapSlice(validation.Config["when"]) {
		if !policyConditionItemMatches(condition, data) {
			return false
		}
	}
	return true
}

func policyConditionItemMatches(condition map[string]any, data map[string]any) bool {
	fieldKey := policyConfigString(condition["field"])
	if fieldKey == "" {
		fieldKey = policyConfigString(condition["field_key"])
	}
	if fieldKey == "" {
		return false
	}
	return policyConditionFieldMatches(fieldKey, condition["values"], condition["in"], condition["value"], data)
}

func policyConditionFieldMatches(fieldKey string, valuesValue any, alternateValuesValue any, expectedValue any, data map[string]any) bool {
	actual := strings.TrimSpace(fmt.Sprint(data[fieldKey]))
	if actual == "" || actual == "<nil>" {
		return false
	}
	values := stringListAny(valuesValue)
	if len(values) == 0 {
		values = stringListAny(alternateValuesValue)
	}
	if len(values) > 0 {
		return policyContainsText(values, actual)
	}
	if expected := policyConfigString(expectedValue); expected != "" {
		return actual == expected
	}
	return true
}

func RecordPolicyFilterExpectedValue(filter map[string]any, data map[string]any) (any, bool) {
	if value, ok := filter["value"]; ok && value != nil {
		return value, true
	}
	sourceField := policyConfigString(filter["source_field"])
	if sourceField == "" {
		sourceField = policyConfigString(filter["equals_field"])
	}
	if sourceField == "" {
		return nil, false
	}
	value := data[sourceField]
	if RecordIsEmptyValue(value) {
		return nil, false
	}
	return value, true
}

func RecordMatchesPolicyFilters(record recordmodel.Record, validation definitionmodel.ValidationSchema, data map[string]any) bool {
	for _, filter := range policyMapSlice(validation.Config["filters"]) {
		fieldKey := policyConfigString(filter["field"])
		if fieldKey == "" {
			continue
		}
		actual := record.Data[fieldKey]
		if expected, ok := RecordPolicyFilterExpectedValue(filter, data); ok {
			if strings.TrimSpace(fmt.Sprint(actual)) != strings.TrimSpace(fmt.Sprint(expected)) {
				return false
			}
		}
		if values := stringListAny(filter["values"]); len(values) > 0 && !policyContainsText(values, strings.TrimSpace(fmt.Sprint(actual))) {
			return false
		}
		if minField := policyConfigString(filter["gte_field"]); minField != "" {
			if !RecordPolicyValueOnOrAfter(actual, data[minField]) {
				return false
			}
		}
		if maxField := policyConfigString(filter["lte_field"]); maxField != "" {
			if !RecordPolicyValueOnOrBefore(actual, data[maxField]) {
				return false
			}
		}
	}
	return true
}

func RecordPolicyValueOnOrAfter(actual any, minimum any) bool {
	actualDate, actualDateOK := RecordDateOnly(actual)
	minDate, minDateOK := RecordDateOnly(minimum)
	if actualDateOK && minDateOK {
		return !actualDate.Before(minDate)
	}
	actualNumber, actualNumberOK := policyNumericAny(actual)
	minNumber, minNumberOK := policyNumericAny(minimum)
	return actualNumberOK && minNumberOK && actualNumber >= minNumber
}

func RecordPolicyValueOnOrBefore(actual any, maximum any) bool {
	actualDate, actualDateOK := RecordDateOnly(actual)
	maxDate, maxDateOK := RecordDateOnly(maximum)
	if actualDateOK && maxDateOK {
		return !actualDate.After(maxDate)
	}
	actualNumber, actualNumberOK := policyNumericAny(actual)
	maxNumber, maxNumberOK := policyNumericAny(maximum)
	return actualNumberOK && maxNumberOK && actualNumber <= maxNumber
}

func RecordThresholdPermissionKey(validation definitionmodel.ValidationSchema) string {
	for _, key := range []string{"permission", "required_permission", "requires_permission"} {
		if permission := policyConfigString(validation.Config[key]); permission != "" {
			return permission
		}
	}
	return ""
}

func RecordThresholdPermissionChangedOnly(validation definitionmodel.ValidationSchema) bool {
	for _, key := range []string{"changed_only", "only_when_changed"} {
		if value, ok := policyBoolAny(validation.Config[key]); ok {
			return value
		}
	}
	return false
}

func RecordThresholdPermissionExceeded(validation definitionmodel.ValidationSchema, value float64, data map[string]any) bool {
	matchMode := strings.ToLower(recordValueOrDefault(policyConfigString(validation.Config["match"]), "any"))
	matched := false
	checked := false
	for _, key := range []string{"min", "threshold", "gte", "min_value"} {
		if threshold, ok := policyNumericAny(validation.Config[key]); ok {
			checked = true
			if value >= threshold {
				matched = true
			}
		}
	}
	for _, key := range []string{"exclusive_min", "gt"} {
		if threshold, ok := policyNumericAny(validation.Config[key]); ok {
			checked = true
			if value > threshold {
				matched = true
			}
		}
	}
	ratioField := policyConfigString(validation.Config["ratio_field"])
	if ratioField == "" {
		ratioField = policyConfigString(validation.Config["base_field"])
	}
	if ratioField != "" {
		if ratio, ok := RecordThresholdPermissionRatio(value, data[ratioField]); ok {
			for _, key := range []string{"ratio_min", "ratio_threshold", "min_ratio"} {
				if threshold, thresholdOK := policyNumericAny(validation.Config[key]); thresholdOK {
					checked = true
					if ratio >= threshold {
						matched = true
					}
				}
			}
			for _, key := range []string{"exclusive_ratio_min", "ratio_gt"} {
				if threshold, thresholdOK := policyNumericAny(validation.Config[key]); thresholdOK {
					checked = true
					if ratio > threshold {
						matched = true
					}
				}
			}
		}
	}
	if !checked {
		return false
	}
	if matchMode == "all" {
		return RecordThresholdPermissionAllExceeded(validation, value, data)
	}
	return matched
}

func RecordThresholdPermissionAllExceeded(validation definitionmodel.ValidationSchema, value float64, data map[string]any) bool {
	hasCheck := false
	for _, key := range []string{"min", "threshold", "gte", "min_value"} {
		if threshold, ok := policyNumericAny(validation.Config[key]); ok {
			hasCheck = true
			if value < threshold {
				return false
			}
		}
	}
	for _, key := range []string{"exclusive_min", "gt"} {
		if threshold, ok := policyNumericAny(validation.Config[key]); ok {
			hasCheck = true
			if value <= threshold {
				return false
			}
		}
	}
	ratioField := policyConfigString(validation.Config["ratio_field"])
	if ratioField == "" {
		ratioField = policyConfigString(validation.Config["base_field"])
	}
	ratio, ratioOK := RecordThresholdPermissionRatio(value, data[ratioField])
	for _, key := range []string{"ratio_min", "ratio_threshold", "min_ratio"} {
		if threshold, ok := policyNumericAny(validation.Config[key]); ok {
			hasCheck = true
			if !ratioOK || ratio < threshold {
				return false
			}
		}
	}
	for _, key := range []string{"exclusive_ratio_min", "ratio_gt"} {
		if threshold, ok := policyNumericAny(validation.Config[key]); ok {
			hasCheck = true
			if !ratioOK || ratio <= threshold {
				return false
			}
		}
	}
	return hasCheck
}

func RecordThresholdPermissionRatio(value float64, base any) (float64, bool) {
	baseNumber, ok := policyNumericAny(base)
	if !ok || baseNumber == 0 {
		return 0, false
	}
	return value / baseNumber, true
}

func policyMapSlice(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return append([]map[string]any(nil), typed...)
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if mapped, ok := item.(map[string]any); ok {
				out = append(out, mapped)
			}
		}
		return out
	default:
		return nil
	}
}

func RecordMapSlice(value any) []map[string]any {
	return policyMapSlice(value)
}

func policyConfigString(value any) string {
	if value == nil {
		return ""
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "<nil>" {
		return ""
	}
	return text
}

func policyContainsText(values []string, expected string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(expected)) {
			return true
		}
	}
	return false
}

func policyNumericAny(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func RecordNumericValue(value any) (float64, bool) {
	return policyNumericAny(value)
}

func policyBoolAny(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes":
			return true, true
		case "false", "0", "no":
			return false, true
		}
	}
	return false, false
}

func RecordBoolValue(value any) (bool, bool) {
	return policyBoolAny(value)
}
