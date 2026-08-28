package validation

import (
	"fmt"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type RecordTemporalExclusionPolicy struct {
	Key              string
	StartField       string
	EndField         string
	ScopeFields      []string
	StatusField      string
	ExcludedStatuses []string
	ErrorCode        string
	Definition       definitionmodel.ValidationSchema
}

func RecordTemporalExclusionPolicies(object definitionmodel.ObjectSchema) ([]RecordTemporalExclusionPolicy, error) {
	policies := []RecordTemporalExclusionPolicy{}
	for _, validation := range object.Validations {
		if strings.TrimSpace(validation.Type) != "temporal_exclusion" {
			continue
		}
		startField := RecordConfigString(validation.Config["start_field"])
		endField := RecordConfigString(validation.Config["end_field"])
		scopeFields := recordStringList(validation.Config["scope_fields"])
		if startField == "" || endField == "" {
			return nil, validationError("backend.validation.temporal_exclusion_range_fields_required", "validation", validation.Key)
		}
		if len(scopeFields) == 0 {
			return nil, validationError("backend.validation.temporal_exclusion_scope_fields_required", "validation", validation.Key)
		}
		statusField := RecordConfigString(validation.Config["status_field"])
		excludedStatuses := recordStringList(validation.Config["excluded_statuses"])
		policies = append(policies, RecordTemporalExclusionPolicy{
			Key: strings.TrimSpace(validation.Key), StartField: startField, EndField: endField,
			ScopeFields: scopeFields, StatusField: statusField, ExcludedStatuses: excludedStatuses,
			ErrorCode: RecordPolicyMessageCode(validation.Message, "backend.policy.temporal_exclusion"), Definition: validation,
		})
	}
	return policies, nil
}

func RecordTemporalExclusionCandidate(policy RecordTemporalExclusionPolicy, data map[string]any, operation string) (bool, error) {
	if !RecordValidationBlocks(policy.Definition) || !RecordPolicyAppliesToOperation(policy.Definition, operation) || !RecordPolicyConditionMatches(policy.Definition, data) {
		return false, nil
	}
	if policy.StatusField != "" && RecordContainsText(policy.ExcludedStatuses, strings.TrimSpace(fmt.Sprint(data[policy.StatusField]))) {
		return false, nil
	}
	_, _, ok, err := RecordTimeOverlapRange(data, policy.StartField, policy.EndField)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	for _, field := range policy.ScopeFields {
		if RecordIsEmptyValue(data[field]) {
			return false, validationError("backend.validation.temporal_exclusion_scope_required", "validation", policy.Key, "field", field)
		}
	}
	return true, nil
}

func recordStringList(value any) []string {
	result := []string{}
	switch values := value.(type) {
	case []string:
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				result = append(result, value)
			}
		}
	case []any:
		for _, value := range values {
			if text := strings.TrimSpace(fmt.Sprint(value)); text != "" && text != "<nil>" {
				result = append(result, text)
			}
		}
	}
	return result
}
