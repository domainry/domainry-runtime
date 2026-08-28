package validation

import (
	"encoding/json"
	"fmt"
	"strings"
)

func importMapFromAny(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	if typed, ok := value.(map[string]any); ok {
		return RecordCloneData(typed)
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	result := map[string]any{}
	if json.Unmarshal(payload, &result) != nil {
		return map[string]any{}
	}
	return result
}

func importStringListFromAny(value any) []string {
	result := []string{}
	switch typed := value.(type) {
	case []string:
		result = append(result, typed...)
	case []any:
		for _, item := range typed {
			if text := strings.TrimSpace(fmt.Sprint(item)); text != "" && text != "<nil>" {
				result = append(result, text)
			}
		}
	case string:
		if text := strings.TrimSpace(typed); text != "" {
			result = append(result, text)
		}
	}
	return result
}
