package projection

import (
	"encoding/json"
	"fmt"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type PipelineFieldChange struct {
	Field  string `json:"field"`
	Before any    `json:"before,omitempty"`
	After  any    `json:"after,omitempty"`
}

func PipelineTransitionFieldChangesJSON(object definitionmodel.ObjectSchema, before, after map[string]any) string {
	changes := []PipelineFieldChange{}
	for _, field := range object.Fields {
		if !PipelineTransitionValuesEqual(before[field.Key], after[field.Key]) {
			changes = append(changes, PipelineFieldChange{Field: field.Key, Before: before[field.Key], After: after[field.Key]})
		}
	}
	if len(changes) == 0 {
		return ""
	}
	encoded, err := json.Marshal(changes)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func PipelineTransitionChangedPatch(object definitionmodel.ObjectSchema, before, after map[string]any) map[string]any {
	patch := map[string]any{}
	for _, field := range object.Fields {
		if !PipelineTransitionValuesEqual(before[field.Key], after[field.Key]) {
			patch[field.Key] = after[field.Key]
		}
	}
	return patch
}

func PipelineTransitionValuesEqual(left, right any) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	if leftErr == nil && rightErr == nil {
		return string(leftJSON) == string(rightJSON)
	}
	return fmt.Sprint(left) == fmt.Sprint(right)
}
