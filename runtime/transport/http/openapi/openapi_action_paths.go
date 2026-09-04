package openapi

import (
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func addActionOpenAPIPath(paths map[string]any, action definitionmodel.ActionSchema) {
	objectKey, actionKey := strings.TrimSpace(action.ObjectKey), strings.TrimSpace(action.Key)
	if objectKey == "" || actionKey == "" {
		return
	}
	if openAPIObjectActionKind(action.Kind) {
		paths["/records/objects/"+objectKey+"/actions/"+actionKey+"/run"] = map[string]any{"post": openAPIOperation("execute"+openAPIOperationName(objectKey)+openAPIOperationName(actionKey), "Actions", "Execute "+valueOrDefault(action.Label, actionKey), openAPIAdminSecurity(), openAPIJSONRequest(openAPIRef("ObjectActionRequest")), openAPIJSONResponse("Object action result", openAPIRef("ObjectActionResult")))}
	}
	if !openAPIObjectOnlyActionKind(action.Kind) {
		paths["/records/objects/"+objectKey+"/records/{recordID}/actions/"+actionKey] = map[string]any{"post": openAPIOperation("execute"+openAPIOperationName(objectKey)+openAPIOperationName(actionKey), "Actions", "Execute "+valueOrDefault(action.Label, actionKey), openAPIAdminSecurity(), openAPIPathParameter("recordID", "Record ID"), openAPIJSONRequest(openAPIRef("ActionRequest")), openAPIJSONResponse("Action result", openAPIRef("ActionResult")))}
	}
	paths["/records/objects/"+objectKey+"/actions/"+actionKey+"/bulk"] = map[string]any{"post": openAPIOperation("executeBulk"+openAPIOperationName(objectKey)+openAPIOperationName(actionKey), "Actions", "Execute "+valueOrDefault(action.Label, actionKey)+" for multiple records", openAPIAdminSecurity(), openAPIJSONRequest(openAPIRef("BulkActionRequest")), openAPIJSONResponse("Bulk action result", openAPIObject(nil)))}
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
