package openapi

import (
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func addActionOpenAPIPath(paths map[string]any, action definitionmodel.ActionSchema, objectSets ...[]definitionmodel.ObjectSchema) {
	objectKey, actionKey := strings.TrimSpace(action.ObjectKey), strings.TrimSpace(action.Key)
	if objectKey == "" || actionKey == "" {
		return
	}
	if openAPIObjectActionKind(action.Kind) {
		paths["/records/"+objectKey+"/actions/"+actionKey] = map[string]any{"post": openAPIOperation("execute"+openAPIOperationName(objectKey)+openAPIOperationName(actionKey), "Actions", "Execute "+valueOrDefault(action.Label, actionKey), openAPIAdminSecurity(), openAPIJSONRequest(openAPIRef("ObjectActionRequest")), openAPIJSONResponse("Object action result", openAPIActionResultSchema(action, objectSets)))}
	}
	if !openAPIObjectOnlyActionKind(action.Kind) {
		paths["/records/"+objectKey+"/items/{recordID}/actions/"+actionKey] = map[string]any{"post": openAPIOperation("execute"+openAPIOperationName(objectKey)+openAPIOperationName(actionKey), "Actions", "Execute "+valueOrDefault(action.Label, actionKey), openAPIAdminSecurity(), openAPIPathParameter("recordID", "Record ID"), openAPIJSONRequest(openAPIRef("ActionRequest")), openAPIJSONResponse("Action result", openAPIRef("ActionResult")))}
	}
	paths["/records/"+objectKey+"/actions/"+actionKey+"/bulk"] = map[string]any{"post": openAPIOperation("executeBulk"+openAPIOperationName(objectKey)+openAPIOperationName(actionKey), "Actions", "Execute "+valueOrDefault(action.Label, actionKey)+" for multiple records", openAPIAdminSecurity(), openAPIJSONRequest(openAPIRef("BulkActionRequest")), openAPIJSONResponse("Bulk action result", openAPIObject(nil)))}
}

func openAPIActionResultSchema(action definitionmodel.ActionSchema, objectSets [][]definitionmodel.ObjectSchema) map[string]any {
	objectCatalog := map[string]definitionmodel.ObjectSchema{}
	for _, objects := range objectSets {
		for _, object := range objects {
			objectCatalog[strings.TrimSpace(object.Key)] = object
		}
	}
	properties := map[string]any{}
	required := []string{}
	hasStructuredOutput := false
	for _, field := range action.OutputFields {
		fieldKey := strings.TrimSpace(field.Key)
		if fieldKey == "" {
			continue
		}
		if strings.TrimSpace(field.Type) == "store_organization_snapshot" {
			properties[fieldKey] = openAPIStoreOrganizationSnapshotSchema(field, objectCatalog)
			hasStructuredOutput = true
		} else {
			properties[fieldKey] = openAPIActionOutputFieldSchema(field, objectCatalog)
		}
		if field.Required {
			required = append(required, fieldKey)
		}
	}
	if !hasStructuredOutput {
		return openAPIRef("ObjectActionResult")
	}
	output := map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
	if len(required) > 0 {
		output["required"] = required
	}
	return map[string]any{"allOf": []any{
		openAPIRef("ObjectActionResult"),
		map[string]any{"type": "object", "properties": map[string]any{"output": output}, "required": []string{"output"}},
	}}
}

func openAPIActionOutputFieldSchema(field definitionmodel.ActionOutputField, objects map[string]definitionmodel.ObjectSchema) map[string]any {
	if object, exists := objects[strings.TrimSpace(field.SourceObjectKey)]; exists {
		for _, objectField := range object.Fields {
			if strings.TrimSpace(objectField.Key) == strings.TrimSpace(field.SourceFieldKey) {
				return openAPIFieldSchema(objectField)
			}
		}
	}
	var schema map[string]any
	switch strings.TrimSpace(field.Type) {
	case "integer":
		schema = map[string]any{"type": "integer"}
	case "number":
		schema = map[string]any{"type": "number"}
	case "percent":
		schema = map[string]any{"type": "string", "format": "decimal", "pattern": `^-?[0-9]+(?:\.[0-9]+)?$`, "x-lossless-decimal": true}
	case "boolean":
		schema = map[string]any{"type": "boolean"}
	case "date", "datetime":
		schema = map[string]any{"type": "string", "format": "date-time"}
	case "object":
		schema = map[string]any{"type": "object"}
	default:
		schema = map[string]any{"type": "string"}
	}
	if field.Repeated {
		return map[string]any{"type": "array", "items": schema}
	}
	return schema
}

func openAPIStoreOrganizationSnapshotSchema(field definitionmodel.ActionOutputField, objects map[string]definitionmodel.ObjectSchema) map[string]any {
	organization := strictOpenAPIObject([]string{"reference", "code", "name", "status", "sort_order", "version"}, map[string]any{
		"reference":  map[string]any{"type": "string", "minLength": 1, "x-domainry-opaque-reference": "identity.organization_unit"},
		"code":       map[string]any{"type": "string", "minLength": 1},
		"name":       map[string]any{"type": "string", "minLength": 1},
		"status":     map[string]any{"type": "string", "minLength": 1},
		"sort_order": map[string]any{"type": "integer"},
		"version":    map[string]any{"type": "integer", "minimum": 1},
	})
	itemProperties := map[string]any{"organization": organization}
	itemRequired := []string{"organization"}
	for _, raw := range field.StoreOrganizationSnapshotObjectKeys {
		objectKey := strings.TrimSpace(raw)
		data := openAPIObjectDataSchema(objects[objectKey])
		data["additionalProperties"] = false
		itemProperties[objectKey] = strictOpenAPIObject([]string{"record_id", "revision", "data"}, map[string]any{
			"record_id": map[string]any{"type": "string", "minLength": 1},
			"revision":  map[string]any{"type": "string", "minLength": 1},
			"data":      data,
		})
		itemRequired = append(itemRequired, objectKey)
	}
	item := strictOpenAPIObject(itemRequired, itemProperties)
	return strictOpenAPIObject([]string{"items"}, map[string]any{
		"items":       map[string]any{"type": "array", "items": item},
		"next_cursor": map[string]any{"type": "string", "maxLength": 2048},
	})
}

func strictOpenAPIObject(required []string, properties map[string]any) map[string]any {
	result := map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
	if len(required) > 0 {
		result["required"] = required
	}
	return result
}

func openAPIObjectActionKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "object_create", "object_operation", "bulk_operation":
		return true
	default:
		return false
	}
}

func openAPIObjectOnlyActionKind(kind string) bool {
	switch strings.TrimSpace(kind) {
	case "object_create", "object_operation":
		return true
	default:
		return false
	}
}
