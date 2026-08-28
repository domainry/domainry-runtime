package validation

import definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
)

func findFieldSchema(object definitionmodel.ObjectSchema, fieldKey string) (definitionmodel.FieldSchema, bool) {
	for _, field := range object.Fields {
		if field.Key == fieldKey {
			return field, true
		}
	}
	return definitionmodel.FieldSchema{}, false
}

// stateMachineAllowsTransition checks whether the transition from `from` to `to` is
// permitted by the transitions config (array of {from, to} objects or map[from -> []to]).
func stateMachineAllowsTransition(transitionsRaw any, from string, to string) bool {
	if transitionsRaw == nil {
		return true // no transitions configured: allow all
	}
	items, _ := jsonArrayAny(transitionsRaw)
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		fromState := strings.TrimSpace(fmt.Sprint(m["from"]))
		if fromState != from {
			continue
		}
		toStates := stringListAny(m["to"])
		if containsString(toStates, to) {
			return true
		}
	}
	return false
}

func isBlockingValidation(validation definitionmodel.ValidationSchema) bool {
	severity := strings.ToLower(strings.TrimSpace(validation.Severity))
	return severity != "warning" && severity != "info"
}

func validationConditionApplies(validation definitionmodel.ValidationSchema, data map[string]any) bool {
	fieldKey, ok := stringConfig(validation.Config, "when_field")
	if !ok {
		return true
	}
	actual := strings.TrimSpace(fmt.Sprint(data[fieldKey]))
	if actual == "" || actual == "<nil>" {
		return false
	}
	values := stringListAny(validation.Config["when_in"])
	if len(values) == 0 {
		values = stringListAny(validation.Config["values"])
	}
	if len(values) > 0 {
		return containsString(values, actual)
	}
	if expected, ok := stringConfig(validation.Config, "value"); ok {
		return actual == expected
	}
	return true
}

func validationValueMatches(value any, config map[string]any) bool {
	if config == nil {
		return false
	}
	if enabled, ok := boolAny(config["nonzero"]); ok && enabled {
		number, ok := numericAny(value)
		return ok && math.Abs(number) > 0.0000001
	}
	actual := strings.TrimSpace(fmt.Sprint(value))
	if rawExpected, ok := config["value"]; ok && rawExpected != nil {
		expected := strings.TrimSpace(fmt.Sprint(rawExpected))
		if expected == "" {
			return false
		}
		return actual == expected
	}
	for _, expected := range stringListAny(config["values"]) {
		if actual == expected {
			return true
		}
	}
	return false
}

func stringListAny(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text != "" {
				out = append(out, text)
			}
		}
		return out
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []string{strings.TrimSpace(typed)}
	default:
		return nil
	}
}

func jsonStringArray(value any) ([]string, error) {
	items, err := jsonArrayAny(value)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, validationError("backend.validation.array_item_string")
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return nil, validationError("backend.validation.array_item_required")
		}
		out = append(out, text)
	}
	return out, nil
}

func validateSpecialDatesScheduleValue(value any) error {
	items, err := jsonArrayAny(value)
	if err != nil {
		return err
	}
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			return validationError("backend.validation.special_date_object")
		}
		dateText := strings.TrimSpace(fmt.Sprint(record["date"]))
		if dateText == "" || dateText == "<nil>" {
			return validationError("backend.validation.special_date_required")
		}
		if _, err := time.Parse("2006-01-02", dateText); err != nil {
			return validationError("backend.validation.special_date_format")
		}
		if segments, ok := record["segments"]; ok && !RecordIsEmptyValue(segments) {
			if err := validateBusinessHourSegmentsValue(segments); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateBusinessHourSegmentsValue(value any) error {
	items, err := jsonArrayAny(value)
	if err != nil {
		return err
	}
	intervals := make([][2]int, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			return validationError("backend.validation.business_hours_object")
		}
		opensAt := strings.TrimSpace(fmt.Sprint(record["opens_at"]))
		closesAt := strings.TrimSpace(fmt.Sprint(record["closes_at"]))
		if opensAt == "" || opensAt == "<nil>" || closesAt == "" || closesAt == "<nil>" {
			return validationError("backend.validation.business_hours_required")
		}
		opens, ok := parseHHMMMinutes(opensAt)
		if !ok {
			return validationError("backend.validation.business_hours_open_format")
		}
		closes, ok := parseHHMMMinutes(closesAt)
		if !ok {
			return validationError("backend.validation.business_hours_close_format")
		}
		if closes <= opens {
			return validationError("backend.validation.business_hours_close_after_open")
		}
		for _, existing := range intervals {
			if opens < existing[1] && existing[0] < closes {
				return validationError("backend.validation.business_hours_overlap")
			}
		}
		intervals = append(intervals, [2]int{opens, closes})
	}
	return nil
}

func jsonArrayAny(value any) ([]any, error) {
	if RecordIsEmptyValue(value) {
		return nil, nil
	}
	switch typed := value.(type) {
	case []any:
		return typed, nil
	case []map[string]any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, item)
		}
		return out, nil
	case []string:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, item)
		}
		return out, nil
	case string:
		text := strings.TrimSpace(typed)
		var out []any
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			return nil, err
		}
		return out, nil
	default:
		return nil, validationError("backend.validation.json_array")
	}
}

func stateMachineValue(data map[string]any, fieldKey string) string {
	if data == nil {
		return ""
	}
	value, ok := data[fieldKey]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func parseHHMMMinutes(value string) (int, bool) {
	if !regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`).MatchString(value) {
		return 0, false
	}
	parts := strings.Split(value, ":")
	hour := int(parts[0][0]-'0')*10 + int(parts[0][1]-'0')
	minute := int(parts[1][0]-'0')*10 + int(parts[1][1]-'0')
	return hour*60 + minute, true
}

func validationDateOnly(value any) (time.Time, bool) {
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" || text == "<nil>" {
		return time.Time{}, false
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339} {
		parsed, err := time.Parse(layout, text)
		if err == nil {
			return parsed.UTC().Truncate(24 * time.Hour), true
		}
	}
	return time.Time{}, false
}
