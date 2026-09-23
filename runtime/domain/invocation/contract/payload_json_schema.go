package invocation

import (
	"sort"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

// PayloadJSONSchema describes canonical business input, excluding Runtime-owned
// invocation metadata. Both discovery and HTTP documentation use this projection.
// Callers must distinguish an absent published contract from an empty contract.
// Validation and normalization at invocation remain authoritative.
func PayloadJSONSchema(fields []definitionmodel.ActionPayloadField, defaults map[string]any) map[string]any {
	properties := map[string]any{}
	required := []string{}
	for _, field := range fields {
		key := strings.TrimSpace(field.Key)
		if key == "" {
			continue
		}
		value := payloadFieldJSONSchema(field)
		fallback := field.DefaultValue
		if defaults[key] != nil {
			fallback = defaults[key]
		}
		if fallback != nil {
			value["default"] = fallback
		}
		properties[key] = value
		if field.Required && fallback == nil {
			required = append(required, key)
		}
	}
	sort.Strings(required)
	schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func payloadFieldJSONSchema(field definitionmodel.ActionPayloadField) map[string]any {
	var item map[string]any
	if field.IsObject() {
		item = PayloadJSONSchema(field.Fields, nil)
	} else {
		item = map[string]any{}
		switch strings.TrimSpace(field.Type) {
		case "integer":
			item["type"] = "integer"
		case "number":
			item["type"] = "number"
		case "boolean":
			item["type"] = "boolean"
		case "currency", "percent":
			item["type"], item["format"] = "string", "decimal"
			item["pattern"], item["x-lossless-decimal"] = `^-?[0-9]+(?:\.[0-9]+)?$`, true
		case "date":
			item["type"], item["format"] = "string", "date"
		case "datetime":
			item["type"], item["format"] = "string", "date-time"
		default:
			// The Runtime scalar normalizer treats empty type as text. Source
			// lineage is not type inheritance and is not disclosed here.
			item["type"] = "string"
		}
		if len(field.Options) > 0 {
			item["enum"] = append([]string(nil), field.Options...)
		}
	}
	if field.Repeated {
		minItems, maxItems := 0, definitionmodel.ActionPayloadMaxItems
		if field.MinItems != nil {
			minItems = *field.MinItems
		}
		if field.Required && minItems < 1 {
			minItems = 1
		}
		if field.MaxItems != nil && *field.MaxItems < maxItems {
			maxItems = *field.MaxItems
		}
		item = map[string]any{"type": "array", "items": item, "maxItems": maxItems}
		if minItems > 0 {
			item["minItems"] = minItems
		}
	}
	if strings.TrimSpace(field.Name) != "" {
		item["title"] = field.Name
	}
	if strings.TrimSpace(field.Description) != "" {
		item["description"] = field.Description
	}
	return item
}

func WorkflowPayloadJSONSchema(workflow definitionmodel.WorkflowSchema) map[string]any {
	fields := workflowInvocationFields(workflow.InputFields)
	for i := range fields {
		fields[i].Description = workflow.InputFields[i].Description
	}
	return PayloadJSONSchema(fields, nil)
}
