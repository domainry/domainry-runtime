package policy

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func containsText(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func pipelineValuesEqual(left any, right any) bool {
	return reflect.DeepEqual(left, right)
}

func recordUpdatedTriggers(objectKey string, before map[string]any, after map[string]any) []string {
	triggers := []string{"record_updated:" + objectKey}
	for _, fieldKey := range WorkflowChangedRecordFieldKeys(before, after) {
		triggers = append(triggers, "record_updated:"+objectKey+"."+fieldKey)
	}
	return triggers
}

func WorkflowRecordUpdatedTriggers(objectKey string, before map[string]any, after map[string]any) []string {
	return recordUpdatedTriggers(objectKey, before, after)
}

func WorkflowChangedRecordFieldKeys(before map[string]any, after map[string]any) []string {
	seen := map[string]struct{}{}
	for key := range before {
		seen[key] = struct{}{}
	}
	for key := range after {
		seen[key] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		if !reflect.DeepEqual(before[key], after[key]) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func WorkflowMatchesRecordEvent(ctx context.Context, workflow definitionmodel.WorkflowSchema, objectKey string, data, before map[string]any, trigger string) bool {
	if workflow.TriggerContract == nil || strings.TrimSpace(workflow.TriggerContract.Type) == "" {
		return false
	}
	return workflowTriggerContractMatches(*workflow.TriggerContract, objectKey, trigger) && workflowConditionMatchesChange(ctx, workflow, data, before)
}

func WorkflowTriggerContractTypeIsValid(triggerType string) bool {
	switch strings.TrimSpace(triggerType) {
	case "manual", "record_created", "record_updated", "field_changed", "action_completed", "scheduled", "integration_event":
		return true
	default:
		return false
	}
}

func workflowTriggerContractMatches(contract definitionmodel.WorkflowTriggerContract, objectKey, trigger string) bool {
	if contract.ObjectKey != "" && contract.ObjectKey != objectKey {
		return false
	}
	switch strings.TrimSpace(contract.Type) {
	case "record_created":
		return trigger == "record_created:"+objectKey
	case "record_updated":
		return trigger == "record_updated:"+objectKey
	case "field_changed":
		return strings.TrimSpace(contract.FieldKey) != "" && trigger == "record_updated:"+objectKey+"."+strings.TrimSpace(contract.FieldKey)
	case "action_completed":
		return strings.TrimSpace(contract.Event) != "" && trigger == "action_executed:"+strings.TrimSpace(contract.Event)
	default:
		return false
	}
}

func WorkflowTriggerObjectKeys(workflow definitionmodel.WorkflowSchema) []string {
	out := []string{}
	if workflow.TriggerContract == nil {
		return out
	}
	for _, value := range append([]string{workflow.TriggerContract.ObjectKey}, workflow.TriggerContract.ObjectKeys...) {
		if value = strings.TrimSpace(value); value != "" && !containsText(out, value) {
			out = append(out, value)
		}
	}
	return out
}

func WorkflowConditionMatches(ctx context.Context, workflow definitionmodel.WorkflowSchema, data map[string]any) bool {
	return workflowConditionMatchesChange(ctx, workflow, data, nil)
}

func workflowConditionMatchesChange(ctx context.Context, workflow definitionmodel.WorkflowSchema, data, before map[string]any) bool {
	if workflow.ConditionContract == nil || strings.TrimSpace(workflow.ConditionContract.Type) == "" {
		return true
	}
	return WorkflowConditionContractMatches(ctx, *workflow.ConditionContract, data, before)
}

func WorkflowConditionContractMatches(ctx context.Context, condition definitionmodel.WorkflowConditionContract, data, before map[string]any) bool {
	switch workflowValueOrDefault(strings.TrimSpace(condition.Type), "always") {
	case "always":
		return true
	case "field_equals":
		return pipelineValuesEqual(resolvePayloadPath(data, condition.Field), condition.Value)
	case "field_changed":
		return !pipelineValuesEqual(resolvePayloadPath(before, condition.Field), resolvePayloadPath(data, condition.Field))
	case "expression":
		return WorkflowConditionExpressionMatches(ctx, condition.Expression, data)
	case "all", "and":
		for _, child := range condition.Conditions {
			if !WorkflowConditionContractMatches(ctx, child, data, before) {
				return false
			}
		}
		return true
	case "any", "or":
		for _, child := range condition.Conditions {
			if WorkflowConditionContractMatches(ctx, child, data, before) {
				return true
			}
		}
		return false
	case "not":
		return condition.Condition != nil && !WorkflowConditionContractMatches(ctx, *condition.Condition, data, before)
	default:
		return false
	}
}

func workflowValueOrDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}

func resolvePayloadPath(data map[string]any, field string) any {
	field = strings.TrimPrefix(strings.TrimSpace(field), "$")
	field = strings.TrimPrefix(field, "record.")
	field = strings.TrimPrefix(field, "after.")
	if field == "" {
		return nil
	}
	return data[field]
}

func structuredWorkflowConditionMatches(condition map[string]any, data map[string]any) bool {
	if len(condition) == 0 {
		return true
	}
	field := strings.TrimSpace(fmt.Sprint(condition["field"]))
	if field == "" || field == "<nil>" {
		return true
	}
	actual := strings.TrimSpace(fmt.Sprint(data[field]))
	if expected, ok := condition["equals"]; ok {
		return strings.EqualFold(actual, strings.TrimSpace(fmt.Sprint(expected)))
	}
	if expected, ok := condition["not_equals"]; ok {
		return !strings.EqualFold(actual, strings.TrimSpace(fmt.Sprint(expected)))
	}
	values := stringListFromAny(condition["in"])
	if len(values) == 0 {
		values = stringListFromAny(condition["values"])
	}
	if len(values) > 0 {
		for _, value := range values {
			if strings.EqualFold(actual, strings.TrimSpace(value)) {
				return true
			}
		}
		return false
	}
	return true
}

func WorkflowConditionExpressionMatches(ctx context.Context, expression string, data map[string]any) bool {
	text := strings.TrimSpace(strings.ToLower(expression))
	switch {
	case strings.Contains(text, " and "):
		for _, part := range strings.Split(text, " and ") {
			if !WorkflowConditionExpressionMatches(ctx, part, data) {
				return false
			}
		}
		return true
	case strings.Contains(text, " or "):
		for _, part := range strings.Split(text, " or ") {
			if WorkflowConditionExpressionMatches(ctx, part, data) {
				return true
			}
		}
		return false
	case strings.Contains(text, " <= days_ago:"):
		parts := strings.SplitN(text, " <= days_ago:", 2)
		value, valueOK := workflowRecordDateOnly(data[strings.TrimSpace(parts[0])])
		cutoff, cutoffOK := daysAgoCutoff(ctx, parts[1])
		return valueOK && cutoffOK && !value.After(cutoff)
	case strings.Contains(text, " < days_ago:"):
		parts := strings.SplitN(text, " < days_ago:", 2)
		value, valueOK := workflowRecordDateOnly(data[strings.TrimSpace(parts[0])])
		cutoff, cutoffOK := daysAgoCutoff(ctx, parts[1])
		return valueOK && cutoffOK && value.Before(cutoff)
	case strings.Contains(text, " <= days_ahead:"):
		parts := strings.SplitN(text, " <= days_ahead:", 2)
		value, valueOK := workflowRecordDateOnly(data[strings.TrimSpace(parts[0])])
		cutoff, cutoffOK := daysAheadCutoff(ctx, parts[1])
		return valueOK && cutoffOK && !value.After(cutoff)
	case strings.Contains(text, " < days_ahead:"):
		parts := strings.SplitN(text, " < days_ahead:", 2)
		value, valueOK := workflowRecordDateOnly(data[strings.TrimSpace(parts[0])])
		cutoff, cutoffOK := daysAheadCutoff(ctx, parts[1])
		return valueOK && cutoffOK && value.Before(cutoff)
	case strings.HasSuffix(text, " <= today"):
		field := strings.TrimSpace(strings.TrimSuffix(text, " <= today"))
		value, ok := workflowRecordDateOnly(data[field])
		return ok && !value.After(time.Now().UTC().Truncate(24*time.Hour))
	case strings.HasSuffix(text, " < today"):
		field := strings.TrimSpace(strings.TrimSuffix(text, " < today"))
		value, ok := workflowRecordDateOnly(data[field])
		return ok && value.Before(time.Now().UTC().Truncate(24*time.Hour))
	case strings.HasSuffix(text, " <= now"):
		field := strings.TrimSpace(strings.TrimSuffix(text, " <= now"))
		value, ok := workflowRecordTime(data[field])
		return ok && !value.After(time.Now().UTC())
	case strings.HasSuffix(text, " < now"):
		field := strings.TrimSpace(strings.TrimSuffix(text, " < now"))
		value, ok := workflowRecordTime(data[field])
		return ok && value.Before(time.Now().UTC())
	case strings.HasSuffix(text, " >= now"):
		field := strings.TrimSpace(strings.TrimSuffix(text, " >= now"))
		value, ok := workflowRecordTime(data[field])
		return ok && !value.Before(time.Now().UTC())
	case strings.HasSuffix(text, " > now"):
		field := strings.TrimSpace(strings.TrimSuffix(text, " > now"))
		value, ok := workflowRecordTime(data[field])
		return ok && value.After(time.Now().UTC())
	case strings.HasSuffix(text, " is empty"):
		field := strings.TrimSpace(strings.TrimSuffix(text, " is empty"))
		return workflowRecordIsEmptyValue(data[field])
	case strings.HasSuffix(text, " is present"):
		field := strings.TrimSpace(strings.TrimSuffix(text, " is present"))
		return !workflowRecordIsEmptyValue(data[field])
	case strings.Contains(text, " == "):
		parts := strings.SplitN(text, " == ", 2)
		return strings.EqualFold(strings.TrimSpace(fmt.Sprint(data[strings.TrimSpace(parts[0])])), strings.TrimSpace(parts[1]))
	case strings.Contains(text, " != "):
		parts := strings.SplitN(text, " != ", 2)
		return !strings.EqualFold(strings.TrimSpace(fmt.Sprint(data[strings.TrimSpace(parts[0])])), strings.TrimSpace(parts[1]))
	case strings.Contains(text, " not in "):
		parts := strings.SplitN(text, " not in ", 2)
		value := strings.TrimSpace(fmt.Sprint(data[strings.TrimSpace(parts[0])]))
		return !stringInCSV(value, parts[1])
	case strings.Contains(text, " in "):
		parts := strings.SplitN(text, " in ", 2)
		value := strings.TrimSpace(fmt.Sprint(data[strings.TrimSpace(parts[0])]))
		return stringInCSV(value, parts[1])
	case strings.Contains(text, " >= "):
		parts := strings.SplitN(text, " >= ", 2)
		left, leftOK := numericExpressionAny(parts[0], data)
		right, rightOK := numericExpressionAny(parts[1], data)
		return leftOK && rightOK && left >= right
	case strings.Contains(text, " > "):
		parts := strings.SplitN(text, " > ", 2)
		left, leftOK := numericExpressionAny(parts[0], data)
		right, rightOK := numericExpressionAny(parts[1], data)
		return leftOK && rightOK && left > right
	case strings.Contains(text, " <= "):
		parts := strings.SplitN(text, " <= ", 2)
		left, leftOK := numericExpressionAny(parts[0], data)
		right, rightOK := numericExpressionAny(parts[1], data)
		return leftOK && rightOK && left <= right
	case strings.Contains(text, " < "):
		parts := strings.SplitN(text, " < ", 2)
		left, leftOK := numericExpressionAny(parts[0], data)
		right, rightOK := numericExpressionAny(parts[1], data)
		return leftOK && rightOK && left < right
	default:
		return false
	}
}

func stringInCSV(value string, csv string) bool {
	for _, candidate := range strings.Split(csv, ",") {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(candidate)) {
			return true
		}
	}
	return false
}

func daysAgoCutoff(ctx context.Context, value string) (time.Time, bool) {
	_ = ctx
	var days int
	if _, err := fmt.Sscan(strings.TrimSpace(value), &days); err != nil || days < 0 {
		return time.Time{}, false
	}
	return time.Now().UTC().AddDate(0, 0, -days).Truncate(24 * time.Hour), true
}

func daysAheadCutoff(ctx context.Context, value string) (time.Time, bool) {
	_ = ctx
	var days int
	if _, err := fmt.Sscan(strings.TrimSpace(value), &days); err != nil || days < 0 {
		return time.Time{}, false
	}
	return time.Now().UTC().AddDate(0, 0, days).Truncate(24 * time.Hour), true
}

func numericExpressionAny(expression string, data map[string]any) (float64, bool) {
	text := strings.TrimSpace(expression)
	if text == "" {
		return 0, false
	}
	parts := strings.Split(text, "-")
	var total float64
	for idx, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return 0, false
		}
		value, ok := numericAny(data[part])
		if !ok {
			value, ok = numericAny(part)
		}
		if !ok {
			return 0, false
		}
		if idx == 0 {
			total = value
		} else {
			total -= value
		}
	}
	return total, true
}

func stringListFromAny(value any) []string {
	out := []string{}
	switch typed := value.(type) {
	case []string:
		return append(out, typed...)
	case []any:
		for _, item := range typed {
			out = append(out, fmt.Sprint(item))
		}
	}
	return out
}

func numericAny(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case string:
		var out float64
		if _, err := fmt.Sscan(typed, &out); err == nil {
			return out, true
		}
	}
	return 0, false
}

func boolAny(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		text := strings.ToLower(strings.TrimSpace(typed))
		if text == "true" || text == "1" || text == "yes" {
			return true, true
		}
		if text == "false" || text == "0" || text == "no" {
			return false, true
		}
	}
	return false, false
}

func workflowRecordDateOnly(value any) (time.Time, bool) {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" || text == "<nil>" {
		return time.Time{}, false
	}
	if len(text) >= 10 {
		text = text[:10]
	}
	parsed, err := time.Parse("2006-01-02", text)
	return parsed, err == nil
}

func workflowRecordTime(value any) (time.Time, bool) {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" || text == "<nil>" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05Z07:00", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func workflowRecordIsEmptyValue(value any) bool {
	if value == nil {
		return true
	}
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) == ""
}
