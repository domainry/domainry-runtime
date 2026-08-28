package service

import "strings"

func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}

func mapValue(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return nil
}

func boolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func relatedMap(value any) map[string]any {
	mapped, _ := value.(map[string]any)
	return mapped
}

func relatedMapSlice(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return append([]map[string]any(nil), typed...)
	case []any:
		result := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if mapped := relatedMap(item); len(mapped) > 0 {
				result = append(result, mapped)
			}
		}
		return result
	default:
		return nil
	}
}
