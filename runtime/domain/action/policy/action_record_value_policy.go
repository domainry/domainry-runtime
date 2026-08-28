package policy

import (
	"fmt"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

// ActionCloneRecordData copies record-shaped input without depending on Record behavior packages.
func ActionCloneRecordData(data map[string]any) map[string]any {
	out := make(map[string]any, len(data))
	for key, value := range data {
		out[key] = value
	}
	return out
}

func ActionRecordValueEmpty(value any) bool {
	if value == nil {
		return true
	}
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) == ""
}

func ActionObjectFieldExists(object definitionmodel.ObjectSchema, fieldKey string) bool {
	for _, field := range object.Fields {
		if field.Key == fieldKey {
			return true
		}
	}
	return false
}

func ActionRelationField(object definitionmodel.ObjectSchema, fieldKey string) (definitionmodel.FieldSchema, bool) {
	for _, field := range object.Fields {
		if field.Key == fieldKey && field.Type == "relation" {
			return field, true
		}
	}
	return definitionmodel.FieldSchema{}, false
}

func ActionRelationTarget(field definitionmodel.FieldSchema) string {
	target := strings.TrimSpace(fmt.Sprint(field.Config["object_key"]))
	if target == "" || target == "<nil>" {
		target = strings.TrimSpace(fmt.Sprint(field.Config["target"]))
	}
	if target == "" || target == "<nil>" {
		target = strings.TrimSpace(field.Validation.Target)
	}
	return target
}

func ActionTransitionEffects(transition map[string]any) []any {
	if raw, ok := transition["effects"].([]any); ok {
		return append([]any(nil), raw...)
	}
	if raw, ok := transition["effects"].([]string); ok {
		out := make([]any, 0, len(raw))
		for _, item := range raw {
			out = append(out, item)
		}
		return out
	}
	if raw, ok := transition["effect"].(map[string]any); ok {
		return []any{raw}
	}
	return nil
}
