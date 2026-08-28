package integrationcontract

import (
	"math"
	"strings"
)

func IntegrationProtocolValueMatchesType(value any, fieldType string) bool {
	if value == nil {
		return true
	}
	switch strings.TrimSpace(fieldType) {
	case "text", "long_text", "date", "datetime", "file":
		_, ok := value.(string)
		return ok
	case "integer":
		switch typed := value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			return true
		case float64:
			return typed == float64(int64(typed))
		default:
			return false
		}
	case "decimal":
		switch value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
			return true
		default:
			return false
		}
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "json":
		return true
	default:
		return false
	}
}

func IntegrationProtocolValueType(value any) (string, bool) {
	switch typed := value.(type) {
	case bool:
		return "boolean", true
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return "integer", true
	case float32:
		number := float64(typed)
		if !math.IsNaN(number) && !math.IsInf(number, 0) && math.Trunc(number) == number {
			return "integer", true
		}
		return "decimal", true
	case float64:
		if !math.IsNaN(typed) && !math.IsInf(typed, 0) && math.Trunc(typed) == typed {
			return "integer", true
		}
		return "decimal", true
	case map[string]any, []any:
		return "json", true
	case string:
		return "text", true
	case nil:
		return "", false
	default:
		return "json", true
	}
}

func IntegrationProtocolTypesCompatible(sourceType, targetType string) bool {
	source := strings.ToLower(strings.TrimSpace(sourceType))
	target := strings.ToLower(strings.TrimSpace(targetType))
	if target == "json" || source == target {
		return true
	}
	if target == "decimal" && (source == "integer" || source == "number") {
		return true
	}
	if target == "number" && (source == "integer" || source == "decimal" || source == "currency") {
		return true
	}
	textTypes := map[string]bool{"text": true, "long_text": true, "string": true, "email": true, "phone": true, "url": true, "select": true, "relation": true, "user": true, "file": true}
	return textTypes[target] && textTypes[source]
}
