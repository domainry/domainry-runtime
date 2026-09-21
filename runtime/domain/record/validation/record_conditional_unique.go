package validation

import (
	"fmt"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

const ConditionalUniqueValidationType = "conditional_unique"

// RecordConditionalUniquePolicy describes a database-backed uniqueness rule
// that applies only while one discriminator field has a configured value.
type RecordConditionalUniquePolicy struct {
	Key             string
	Message         string
	Fields          []string
	ConditionField  string
	ConditionValues []string
}

// RecordConditionalUniquePolicies parses every conditional_unique validation
// without guessing field roles or active states from names.
func RecordConditionalUniquePolicies(object definitionmodel.ObjectSchema) ([]RecordConditionalUniquePolicy, error) {
	fields := make(map[string]definitionmodel.FieldSchema, len(object.Fields))
	for _, field := range object.Fields {
		fields[strings.TrimSpace(field.Key)] = field
	}
	policies := []RecordConditionalUniquePolicy{}
	for _, rule := range object.Validations {
		if strings.TrimSpace(rule.Type) != ConditionalUniqueValidationType {
			continue
		}
		policy := RecordConditionalUniquePolicy{
			Key:            strings.TrimSpace(rule.Key),
			Message:        strings.TrimSpace(rule.Message),
			ConditionField: RecordConfigString(rule.Config["condition_field"]),
		}
		seenFields := map[string]bool{}
		for _, raw := range rule.Fields {
			field := strings.TrimSpace(raw)
			if field == "" || fields[field].Key == "" || strings.TrimSpace(fields[field].DisabledAt) != "" {
				return nil, fmt.Errorf("conditional unique %s.%s has invalid field %q", object.Key, policy.Key, raw)
			}
			if recordmodel.RecordIsStructuredFieldType(fields[field].Type) {
				return nil, fmt.Errorf("conditional unique %s.%s cannot index structured field %q", object.Key, policy.Key, field)
			}
			if seenFields[field] {
				return nil, fmt.Errorf("conditional unique %s.%s repeats field %q", object.Key, policy.Key, field)
			}
			seenFields[field] = true
			policy.Fields = append(policy.Fields, field)
		}
		if len(policy.Fields) == 0 {
			return nil, fmt.Errorf("conditional unique %s.%s has no unique fields", object.Key, policy.Key)
		}
		condition, ok := fields[policy.ConditionField]
		if policy.ConditionField == "" || !ok || strings.TrimSpace(condition.DisabledAt) != "" {
			return nil, fmt.Errorf("conditional unique %s.%s has invalid condition field %q", object.Key, policy.Key, policy.ConditionField)
		}
		if recordmodel.RecordIsStructuredFieldType(condition.Type) {
			return nil, fmt.Errorf("conditional unique %s.%s cannot index structured condition field %q", object.Key, policy.Key, policy.ConditionField)
		}
		seenValues := map[string]bool{}
		for _, value := range recordConfigStrings(rule.Config["condition_values"]) {
			if value == "" || seenValues[value] {
				return nil, fmt.Errorf("conditional unique %s.%s has invalid condition value %q", object.Key, policy.Key, value)
			}
			seenValues[value] = true
			policy.ConditionValues = append(policy.ConditionValues, value)
		}
		if len(policy.ConditionValues) == 0 {
			return nil, fmt.Errorf("conditional unique %s.%s has no condition values", object.Key, policy.Key)
		}
		policies = append(policies, policy)
	}
	return policies, nil
}

// RecordConditionalUniqueApplies reports whether the candidate is in the
// database-constrained state set published by the policy.
func RecordConditionalUniqueApplies(policy RecordConditionalUniquePolicy, data map[string]any) bool {
	value, ok := data[policy.ConditionField]
	if !ok || RecordIsEmptyValue(value) {
		return false
	}
	actual := strings.TrimSpace(fmt.Sprint(value))
	for _, allowed := range policy.ConditionValues {
		if actual == allowed {
			return true
		}
	}
	return false
}

func recordConfigStrings(value any) []string {
	result := []string{}
	switch typed := value.(type) {
	case []string:
		for _, item := range typed {
			result = append(result, strings.TrimSpace(item))
		}
	case []any:
		for _, item := range typed {
			if item == nil {
				result = append(result, "")
				continue
			}
			result = append(result, strings.TrimSpace(fmt.Sprint(item)))
		}
	}
	return result
}
