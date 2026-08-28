package validation

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
)

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
		if out, err := strconv.ParseFloat(strings.TrimSpace(typed), 64); err == nil {
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

func numericValue(value any) (float64, bool) {
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

func integerValue(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed || typed < -9223372036854775808 || typed >= 9223372036854775808 {
			return 0, false
		}
		return int64(typed), true
	case float32:
		return integerValue(float64(typed))
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func boolValue(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case int:
		return typed != 0, true
	case int64:
		return typed != 0, true
	case float64:
		return typed != 0, true
	case json.Number:
		parsed, err := typed.Int64()
		return parsed != 0, err == nil
	case string:
		text := strings.TrimSpace(strings.ToLower(typed))
		switch text {
		case "true", "1", "yes", "y", "on":
			return true, true
		case "false", "0", "no", "n", "off":
			return false, true
		default:
			return false, false
		}
	default:
		return false, false
	}
}

func stringValue(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed), true
	case fmt.Stringer:
		return strings.TrimSpace(typed.String()), true
	default:
		return "", false
	}
}

func containsOption(options []string, value string) bool {
	for _, option := range options {
		if option == value {
			return true
		}
	}
	return false
}

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func RecordIsEmptyValue(value any) bool {
	return recordcontract.RecordIsEmptyValue(value)
}

func RecordCloneData(data map[string]any) map[string]any {
	return recordcontract.RecordCloneData(data)
}

func recordValueOrDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}

func recordSplitPermission(value string) (string, string) {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) < 2 {
		return "", strings.TrimSpace(value)
	}
	return strings.TrimSpace(parts[len(parts)-2]), strings.TrimSpace(parts[len(parts)-1])
}
