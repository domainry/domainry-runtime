package validation

import (
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func RecordValidateRetentionPolicies(object definitionmodel.ObjectSchema, before, next map[string]any, operation string) error {
	return validateRetentionPoliciesAt(object, before, next, operation, time.Now().UTC())
}

func validateRetentionPoliciesAt(object definitionmodel.ObjectSchema, before, next map[string]any, operation string, now time.Time) error {
	for _, validation := range object.Validations {
		validationType := strings.TrimSpace(validation.Type)
		if validationType != "retention_guard" && validationType != "retention_policy" && validationType != "legal_hold" {
			continue
		}
		if !validationBlocks(validation) || !policyAppliesToOperation(validation, operation) || !RecordPolicyConditionMatches(validation, next) || !RecordRetentionPolicyDeleteIntent(validation, before, next, operation) {
			continue
		}
		legalHoldField := recordValueOrDefault(policyConfigString(validation.Config["legal_hold_field"]), "legal_hold")
		if held, ok := policyBoolAny(next[legalHoldField]); ok && held {
			return stateMachineError(apperror.KindForbidden, policyMessageCode(validation.Message, "backend.policy.retention_guard"), "policy", validation.Key, "field", legalHoldField, "reason", "legal_hold")
		}
		retainUntilField := recordValueOrDefault(policyConfigString(validation.Config["retain_until_field"]), "retain_until")
		retainUntil, ok, err := RecordRetentionPolicyDateValue(next[retainUntilField])
		if err != nil {
			return stateMachineError(apperror.KindBadRequest, "backend.validation.date_format", "field", retainUntilField)
		}
		if ok {
			today := now.UTC().Truncate(24 * time.Hour)
			if !retainUntil.Before(today) {
				return stateMachineError(apperror.KindForbidden, policyMessageCode(validation.Message, "backend.policy.retention_guard"), "policy", validation.Key, "field", retainUntilField, "retain_until", retainUntil.Format("2006-01-02"))
			}
		}
	}
	return nil
}

func RecordValidateStaleRecordPolicies(object definitionmodel.ObjectSchema, data map[string]any, operation string) error {
	return validateStaleRecordPoliciesAt(object, data, operation, time.Now().UTC())
}

func validateStaleRecordPoliciesAt(object definitionmodel.ObjectSchema, data map[string]any, operation string, now time.Time) error {
	for _, validation := range object.Validations {
		validationType := strings.TrimSpace(validation.Type)
		if validationType != "stale_record_guard" && validationType != "stale_activity_guard" {
			continue
		}
		if !validationBlocks(validation) || !policyAppliesToOperation(validation, operation) || !RecordPolicyConditionMatches(validation, data) {
			continue
		}
		dateField := recordValueOrDefault(policyConfigString(validation.Config["date_field"]), "last_activity_at")
		lastActivity, ok, err := RecordStalePolicyDateValue(data[dateField])
		if err != nil {
			return stateMachineError(apperror.KindBadRequest, "backend.validation.date_format", "field", dateField)
		}
		maxAgeDays := RecordStalePolicyMaxAgeDays(validation)
		if !ok || maxAgeDays <= 0 || lastActivity.After(now.UTC().AddDate(0, 0, -maxAgeDays)) {
			continue
		}
		requiredField := recordValueOrDefault(policyConfigString(validation.Config["required_field"]), "stalled_reason")
		if RecordIsEmptyValue(data[requiredField]) {
			return stateMachineError(apperror.KindBadRequest, policyMessageCode(validation.Message, "backend.policy.stale_record_required_field"), "policy", validation.Key, "field", requiredField, "date_field", dateField)
		}
	}
	return nil
}

func policyMessageCode(message, fallback string) string {
	message = strings.TrimSpace(message)
	if !strings.Contains(message, ".") || strings.ContainsAny(message, " \t\n\r") {
		return fallback
	}
	for _, char := range message {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.' {
			continue
		}
		return fallback
	}
	return message
}

func RecordPolicyMessageCode(message, fallback string) string {
	return policyMessageCode(message, fallback)
}

func RecordStalePolicyMaxAgeDays(validation definitionmodel.ValidationSchema) int {
	for _, key := range []string{"max_idle_days", "max_age_days", "stale_after_days"} {
		if value, ok := policyNumericAny(validation.Config[key]); ok {
			return int(value)
		}
	}
	return 0
}

func RecordStalePolicyDateValue(value any) (time.Time, bool, error) {
	if RecordIsEmptyValue(value) {
		return time.Time{}, false, nil
	}
	text := policyConfigString(value)
	if text == "" {
		return time.Time{}, false, nil
	}
	if parsed, err := time.Parse("2006-01-02", text); err == nil {
		return parsed.UTC(), true, nil
	}
	if parsed, err := time.Parse(time.RFC3339, text); err == nil {
		return parsed.UTC(), true, nil
	}
	return time.Time{}, false, fmt.Errorf("invalid stale policy date")
}

func RecordRetentionPolicyDeleteIntent(validation definitionmodel.ValidationSchema, before, next map[string]any, operation string) bool {
	if strings.EqualFold(strings.TrimSpace(operation), "delete") {
		return true
	}
	statusField := recordValueOrDefault(policyConfigString(validation.Config["delete_status_field"]), "status")
	deleteStatuses := stringListAny(validation.Config["delete_status_values"])
	if len(deleteStatuses) == 0 {
		deleteStatuses = stringListAny(validation.Config["deleted_statuses"])
	}
	if len(deleteStatuses) == 0 {
		deleteStatuses = []string{"deleted"}
	}
	nextStatus := policyConfigString(next[statusField])
	if nextStatus == "" || !policyContainsText(deleteStatuses, nextStatus) {
		return false
	}
	if before == nil {
		return true
	}
	return policyConfigString(before[statusField]) != nextStatus
}

func RecordRetentionPolicyDateValue(value any) (time.Time, bool, error) {
	if RecordIsEmptyValue(value) {
		return time.Time{}, false, nil
	}
	text := policyConfigString(value)
	if text == "" {
		return time.Time{}, false, nil
	}
	if parsed, err := time.Parse("2006-01-02", text); err == nil {
		return parsed.UTC(), true, nil
	}
	if parsed, err := time.Parse(time.RFC3339, text); err == nil {
		return parsed.UTC().Truncate(24 * time.Hour), true, nil
	}
	return time.Time{}, false, fmt.Errorf("invalid retention date")
}

func RecordTimeOverlapRangeFields(validation definitionmodel.ValidationSchema) (string, string) {
	startField := policyConfigString(validation.Config["start_field"])
	if startField == "" && len(validation.Fields) > 0 {
		startField = strings.TrimSpace(validation.Fields[0])
	}
	if startField == "" {
		startField = "starts_at"
	}
	endField := policyConfigString(validation.Config["end_field"])
	if endField == "" && len(validation.Fields) > 1 {
		endField = strings.TrimSpace(validation.Fields[1])
	}
	if endField == "" {
		endField = "ends_at"
	}
	return startField, endField
}

func RecordTimeOverlapRange(data map[string]any, startField, endField string) (time.Time, time.Time, bool, error) {
	startText := policyConfigString(data[startField])
	endText := policyConfigString(data[endField])
	if startText == "" || endText == "" {
		return time.Time{}, time.Time{}, false, nil
	}
	start, startDateOnly, err := parseOverlapBoundary(startText)
	if err != nil {
		return time.Time{}, time.Time{}, false, validationTimeError(apperror.KindBadRequest, "backend.validation.datetime_format", "field", startField)
	}
	end, endDateOnly, err := parseOverlapBoundary(endText)
	if err != nil {
		return time.Time{}, time.Time{}, false, validationTimeError(apperror.KindBadRequest, "backend.validation.datetime_format", "field", endField)
	}
	if startDateOnly && endDateOnly {
		end = end.AddDate(0, 0, 1)
	}
	if !end.After(start) {
		return time.Time{}, time.Time{}, false, validationTimeError(apperror.KindBadRequest, "backend.policy.invalid_time_range", "start_field", startField, "end_field", endField)
	}
	return start, end, true, nil
}

func RecordTimeOverlapScopeMatches(validation definitionmodel.ValidationSchema, left, right map[string]any) bool {
	for _, group := range timeOverlapResourceGroups(validation) {
		if timeOverlapResourceGroupMatches(group, left, right) {
			return true
		}
	}
	return false
}

func parseOverlapBoundary(value string) (time.Time, bool, error) {
	if parsed, err := time.Parse("2006-01-02", value); err == nil {
		return parsed.UTC(), true, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	return parsed, false, err
}

func timeOverlapResourceGroups(validation definitionmodel.ValidationSchema) [][]string {
	for _, key := range []string{"resource_groups", "scope_groups"} {
		if groups := stringGroupsFromAny(validation.Config[key]); len(groups) > 0 {
			return groups
		}
	}
	var fields []string
	for _, key := range []string{"scope_fields", "match_fields", "resource_fields"} {
		if fields = stringListAny(validation.Config[key]); len(fields) > 0 {
			break
		}
	}
	if len(fields) == 0 {
		fields = []string{"owner"}
	}
	groups := make([][]string, 0, len(fields))
	for _, field := range fields {
		if field = strings.TrimSpace(field); field != "" {
			groups = append(groups, []string{field})
		}
	}
	return groups
}

func timeOverlapResourceGroupMatches(group []string, left, right map[string]any) bool {
	matched := false
	for _, field := range group {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		leftValue := policyConfigString(left[field])
		rightValue := policyConfigString(right[field])
		if leftValue == "" || rightValue == "" || leftValue != rightValue {
			return false
		}
		matched = true
	}
	return matched
}

func stringGroupsFromAny(value any) [][]string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	groups := make([][]string, 0, len(items))
	for _, item := range items {
		fields := stringListAny(item)
		if len(fields) == 0 {
			if text := policyConfigString(item); text != "" {
				fields = []string{text}
			}
		}
		cleaned := make([]string, 0, len(fields))
		for _, field := range fields {
			if field = strings.TrimSpace(field); field != "" {
				cleaned = append(cleaned, field)
			}
		}
		if len(cleaned) > 0 {
			groups = append(groups, cleaned)
		}
	}
	return groups
}

func validationTimeError(kind apperror.ErrorKind, code string, params ...string) error {
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		if key := strings.TrimSpace(params[index]); key != "" {
			values[key] = params[index+1]
		}
	}
	if len(values) == 0 {
		values = nil
	}
	return &apperror.AppError{Kind: kind, Code: code, Params: values}
}
