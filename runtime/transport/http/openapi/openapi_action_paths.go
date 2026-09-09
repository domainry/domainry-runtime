package openapi

import (
	"sort"
	"strings"

	actionvalidation "github.com/domainry/domainry-runtime/runtime/domain/action/validation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func addActionOpenAPIPath(paths map[string]any, action definitionmodel.ActionSchema, objectSets ...[]definitionmodel.ObjectSchema) {
	objectKey, actionKey := strings.TrimSpace(action.ObjectKey), strings.TrimSpace(action.Key)
	if objectKey == "" || actionKey == "" {
		return
	}
	if openAPIObjectActionKind(action.Kind) {
		paths["/records/"+objectKey+"/actions/"+actionKey] = map[string]any{"post": openAPIOperation("execute"+openAPIOperationName(objectKey)+openAPIOperationName(actionKey), "Actions", "Execute "+valueOrDefault(action.Label, actionKey), openAPIAdminSecurity(), openAPIJSONRequest(openAPIActionRequestSchema(action, "ObjectActionRequest")), openAPIJSONResponse("Object action result", openAPIActionResultSchema(action, objectSets)))}
	}
	if !openAPIObjectOnlyActionKind(action.Kind) {
		paths["/records/"+objectKey+"/items/{recordID}/actions/"+actionKey] = map[string]any{"post": openAPIOperation("execute"+openAPIOperationName(objectKey)+openAPIOperationName(actionKey), "Actions", "Execute "+valueOrDefault(action.Label, actionKey), openAPIAdminSecurity(), openAPIPathParameter("recordID", "Record ID"), openAPIJSONRequest(openAPIActionRequestSchema(action, "ActionRequest")), openAPIJSONResponse("Action result", openAPIRef("ActionResult")))}
	}
	paths["/records/"+objectKey+"/actions/"+actionKey+"/bulk"] = map[string]any{"post": openAPIOperation("executeBulk"+openAPIOperationName(objectKey)+openAPIOperationName(actionKey), "Actions", "Execute "+valueOrDefault(action.Label, actionKey)+" for multiple records", openAPIAdminSecurity(), openAPIJSONRequest(openAPIBulkActionRequestSchema(action)), openAPIJSONResponse("Bulk action result", openAPIObject(nil)))}
}

// openAPIActionRequestSchema returns the per-Action request body. Actions
// without a published payload contract keep the untyped shared reference;
// Actions with payload_fields publish a closed data schema derived from the
// field tree while keeping the Runtime invocation extras optional.
func openAPIActionRequestSchema(action definitionmodel.ActionSchema, fallback string) map[string]any {
	if action.PayloadFields == nil {
		return openAPIRef(fallback)
	}
	return map[string]any{"type": "object", "properties": map[string]any{
		"data": openAPIActionInputSchema(action), "target_organization_id": map[string]any{"type": "string"},
	}}
}

func openAPIBulkActionRequestSchema(action definitionmodel.ActionSchema) map[string]any {
	if action.PayloadFields == nil {
		return openAPIRef("BulkActionRequest")
	}
	return map[string]any{
		"type":     "object",
		"required": []string{"record_ids"},
		"properties": map[string]any{
			"record_ids": openAPIArray(map[string]any{"type": "string"}), "data": openAPIActionInputSchema(action), "expected_versions": openAPIObject(map[string]any{"type": "integer"}),
		},
	}
}

// openAPIActionInputSchema builds the closed data schema of one Action from its
// payload_fields, recursing through nested objects and repeated fields.
// Runtime-owned invocation extras remain optional top-level properties.
func openAPIActionInputSchema(action definitionmodel.ActionSchema) map[string]any {
	schema := openAPIActionPayloadObjectSchema(action.PayloadFields)
	properties := schema["properties"].(map[string]any)
	for key, extra := range openAPIActionInvocationExtraProperties() {
		if _, declared := properties[key]; !declared {
			properties[key] = extra
		}
	}
	return schema
}

func openAPIActionInvocationExtraProperties() map[string]any {
	return map[string]any{
		"expected_version":    map[string]any{"type": "integer"},
		"expected_updated_at": map[string]any{"type": "string"},
		"record_id":           map[string]any{"type": "string"},
		"request_ref":         map[string]any{"type": "string"},
		"approved":            map[string]any{"type": "boolean"},
		"approval_id":         map[string]any{"type": "string"},
		"approval_token":      map[string]any{"type": "string"},
	}
}

func openAPIActionPayloadObjectSchema(fields []definitionmodel.ActionPayloadField) map[string]any {
	properties := map[string]any{}
	required := []string{}
	for _, field := range fields {
		key := strings.TrimSpace(field.Key)
		if key == "" {
			continue
		}
		properties[key] = openAPIActionPayloadFieldSchema(field)
		if field.Required {
			required = append(required, key)
		}
	}
	sort.Strings(required)
	return strictOpenAPIObject(required, properties)
}

func openAPIActionPayloadFieldSchema(field definitionmodel.ActionPayloadField) map[string]any {
	var item map[string]any
	if field.IsObject() {
		item = openAPIActionPayloadObjectSchema(field.Fields)
		openAPISetConstraint(item, "title", field.Name, strings.TrimSpace(field.Name) != "")
	} else {
		leaf := field
		leaf.Repeated, leaf.Fields, leaf.MinItems, leaf.MaxItems = false, nil, nil, nil
		item = openAPIFieldSchema(actionvalidation.ActionTypedPayloadField(leaf))
	}
	if !field.Repeated {
		openAPISetConstraint(item, "description", field.Description, strings.TrimSpace(field.Description) != "")
		return item
	}
	array := openAPIArray(item)
	if title, ok := item["title"]; ok {
		delete(item, "title")
		array["title"] = title
	}
	openAPISetConstraint(array, "description", field.Description, strings.TrimSpace(field.Description) != "")
	minItems := 0
	if field.MinItems != nil {
		minItems = *field.MinItems
	}
	if field.Required && minItems < 1 {
		minItems = 1
	}
	maxItems := definitionmodel.ActionPayloadMaxItems
	if field.MaxItems != nil && *field.MaxItems < maxItems {
		maxItems = *field.MaxItems
	}
	openAPISetConstraint(array, "minItems", minItems, minItems > 0)
	array["maxItems"] = maxItems
	return array
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
