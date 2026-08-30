package validation

import (
	"fmt"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func (state *validationState) validateRequiredIndexes() {
	for objectIndex, object := range state.manifest.Objects {
		for fieldIndex, field := range object.Fields {
			if strings.TrimSpace(field.Type) == "relation" && !manifestFieldIndexed(field) {
				state.add(fmt.Sprintf("objects[%d].fields[%d].config.indexed", objectIndex, fieldIndex), "backend.definition.required_index_missing: relation %s.%s", object.Key, field.Key)
			}
		}
	}
}

func (state *validationState) requireManifestFieldIndex(objectKey, fieldKey, path, usage string) {
	objectKey = strings.TrimSpace(objectKey)
	fieldKey = strings.TrimSpace(fieldKey)
	if objectKey == "" || fieldKey == "" {
		return
	}
	field, exists := state.fields[objectKey][fieldKey]
	if !exists || strings.TrimSpace(field.Key) == "" {
		return // Reference validation owns unknown object/field diagnostics.
	}
	if !manifestFieldIndexed(field) {
		state.add(path, "backend.definition.required_index_missing: %s %s.%s", usage, objectKey, fieldKey)
	}
}

func manifestFieldIndexed(field definitionmodel.FieldSchema) bool {
	if field.Unique {
		return true
	}
	raw, present := field.Config["indexed"]
	if !present {
		return strings.TrimSpace(field.Type) == "relation"
	}
	switch value := raw.(type) {
	case bool:
		return value
	case string:
		value = strings.TrimSpace(value)
		return strings.EqualFold(value, "true") || value == "1"
	default:
		return false
	}
}

func manifestMapSlice(value any) []map[string]any {
	result := []map[string]any{}
	switch values := value.(type) {
	case []map[string]any:
		return values
	case []any:
		for _, value := range values {
			if item, ok := value.(map[string]any); ok {
				result = append(result, item)
			}
		}
	}
	return result
}
