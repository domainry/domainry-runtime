package contract

import "strings"

// RecordIsEmptyValue reports whether a Runtime value is absent for validation.
func RecordIsEmptyValue(value any) bool {
	if value == nil {
		return true
	}
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) == ""
}

// RecordCloneData returns a shallow copy suitable for candidate mutations.
func RecordCloneData(data map[string]any) map[string]any {
	out := make(map[string]any, len(data))
	for key, value := range data {
		out[key] = value
	}
	return out
}
