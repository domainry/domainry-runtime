package openapi

import (
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func addObjectOpenAPIPaths(paths map[string]any, object definitionmodel.ObjectSchema) {
	objectKey := strings.TrimSpace(object.Key)
	if objectKey == "" {
		return
	}
	tag := valueOrDefault(object.Name, objectKey)
	recordSchema := openAPIRef(openAPIObjectSchemaName(objectKey) + "Record")
	createRequest := openAPIRef(openAPIObjectSchemaName(objectKey) + "CreateRequest")
	updateRequest := openAPIRef(openAPIObjectSchemaName(objectKey) + "UpdateRequest")
	paths["/records/"+objectKey+""] = map[string]any{
		"get":  openAPIOperation("list"+openAPIOperationName(objectKey)+"Records", tag, "List "+tag+" records", openAPIEndpointAccess(), openAPIAdminSecurity(), openAPIQueryParameter("locale", "Resolve and search localized record fields in this BCP 47 locale.", map[string]any{"type": "string"}), openAPIJSONResponse("Record page", openAPIRef("PageResult"))),
		"post": openAPIOperation("create"+openAPIOperationName(objectKey)+"Record", tag, "Create "+tag+" record", openAPIEndpointAccess(), openAPIAdminSecurity(), openAPIJSONRequest(createRequest), openAPIJSONResponse("Created record", recordSchema)),
	}
	paths["/records/"+objectKey+"/import/preview"] = map[string]any{
		"post": openAPIOperation("preview"+openAPIOperationName(objectKey)+"Import", tag, "Preview CSV import for "+tag, openAPIEndpointAccess(), openAPIAdminSecurity(), openAPIJSONResponse("Import preview", openAPIObject(nil))),
	}
	paths["/records/"+objectKey+"/import/apply"] = map[string]any{
		"post": openAPIOperation("apply"+openAPIOperationName(objectKey)+"Import", tag, "Apply CSV import for "+tag, openAPIEndpointAccess(), openAPIAdminSecurity(), openAPIJSONResponse("Import result", openAPIObject(nil))),
	}
	exportDispatch := recordExportDispatchOpenAPIResponses(openAPIOperation("dispatch"+openAPIOperationName(objectKey)+"RecordExport", tag, "Export "+tag+" records with automatic direct or background delivery", openAPIAdminSecurity(), openAPIParameter{Value: openAPIHeaderParameter("Idempotency-Key", "Stable caller operation key", true)}, openAPIQueryParameter("locale", "Export resolved localized record fields in this BCP 47 locale.", map[string]any{"type": "string"})))
	paths["/records/"+objectKey+"/export"] = map[string]any{
		"post": exportDispatch,
	}
	paths["/records/"+objectKey+"/items/{recordID}"] = map[string]any{
		"get":    openAPIOperation("get"+openAPIOperationName(objectKey)+"Record", tag, "Get "+tag+" record", openAPIAdminSecurity(), openAPIPathParameter("recordID", "Record ID"), openAPIQueryParameter("locale", "Resolve localized record fields in this BCP 47 locale.", map[string]any{"type": "string"}), openAPIJSONResponse("Record", recordSchema)),
		"patch":  openAPIOperation("update"+openAPIOperationName(objectKey)+"Record", tag, "Update "+tag+" record", openAPIAdminSecurity(), openAPIPathParameter("recordID", "Record ID"), openAPIJSONRequest(updateRequest), openAPIJSONResponse("Updated record", recordSchema)),
		"delete": openAPIOperation("delete"+openAPIOperationName(objectKey)+"Record", tag, "Delete "+tag+" record", openAPIAdminSecurity(), openAPIPathParameter("recordID", "Record ID"), openAPIResponse("Deleted", "application/json", openAPIObject(nil))),
	}
	paths["/records/"+objectKey+"/actions"] = map[string]any{
		"get": openAPIOperation("list"+openAPIOperationName(objectKey)+"Actions", tag, "List available actions for "+tag, openAPIEndpointAccess(), openAPIAdminSecurity(), openAPIJSONResponse("Actions", openAPIArray(openAPIRef("Action")))),
	}
}

func addRuntimeContractOpenAPIPaths(paths map[string]any) {
	paths["/discovery/i18n/locales"] = map[string]any{
		"get": openAPIOperation("listI18nLocales", "I18n", "Available runtime locales", openAPIPublicSecurity(), openAPIJSONResponse("Locales", openAPIArray(openAPIObject(nil)))),
	}
	paths["/discovery/i18n/resources"] = map[string]any{
		"get": openAPIOperation("getI18nResources", "I18n", "Localized runtime resources", openAPIPublicSecurity(), openAPIJSONResponse("Resources", openAPIObject(nil))),
	}
	paths["/discovery/references/{kind}"] = map[string]any{
		"get": openAPIOperation("resolveRuntimeReferenceValues", "Runtime Discovery", "Resolve Runtime-instance values for one Plane-published reference kind", openAPIAdminSecurity(), openAPIPathParameter("kind", "Reference kind"), openAPIQueryParameter("scope", "Optional object, connector, or target scope", map[string]any{"type": "string"}), openAPIJSONResponse("Reference values", openAPIObject(nil))),
	}
	actionAssuranceBinding := openAPIRequiredObject([]string{"action_key", "object_key"}, map[string]any{
		"action_key": map[string]any{"type": "string"},
		"object_key": map[string]any{"type": "string"},
		"record_id":  map[string]any{"type": "string"},
		"payload":    openAPIObject(nil),
	})
	paths["/records/action-assurance/challenges"] = map[string]any{
		"post": openAPIOperation("beginRecordActionAssurance", "Actions", "Begin an Identity-backed dynamic-PIN challenge for an exact Action invocation", openAPIAdminSecurity(), openAPIJSONRequest(actionAssuranceBinding), openAPIJSONResponse("Action assurance challenge", openAPIObject(nil))),
	}
	actionAssuranceVerification := openAPIRequiredObject([]string{"action_key", "object_key", "provider", "state", "code"}, map[string]any{
		"action_key": map[string]any{"type": "string"},
		"object_key": map[string]any{"type": "string"},
		"record_id":  map[string]any{"type": "string"},
		"payload":    openAPIObject(nil),
		"provider":   map[string]any{"type": "string"},
		"state":      map[string]any{"type": "string"},
		"code":       map[string]any{"type": "string", "writeOnly": true},
	})
	paths["/records/action-assurance/challenges/verify"] = map[string]any{
		"post": openAPIOperation("verifyRecordActionAssurance", "Actions", "Verify the dynamic PIN and mint a short-lived, one-time, invocation-bound Action grant", openAPIAdminSecurity(), openAPIJSONRequest(actionAssuranceVerification), openAPIJSONResponse("Action assurance grant", openAPIObject(nil))),
	}
	addUploadOpenAPIPaths(paths)
	paths["/records/{objectKey}"] = map[string]any{
		"get":  openAPIOperation("listObjectRecords", "Objects", "List object records", openAPIAdminSecurity(), openAPIPathParameter("objectKey", "Object key"), openAPIJSONResponse("Record page", openAPIRef("PageResult"))),
		"post": openAPIOperation("createObjectRecord", "Objects", "Create an object record", openAPIAdminSecurity(), openAPIPathParameter("objectKey", "Object key"), openAPIJSONRequest(openAPIObject(nil)), openAPIJSONResponse("Created record", openAPIRef("Record"))),
	}
	paths["/records/{objectKey}/fields/{fieldKey}/options"] = map[string]any{
		"get": openAPIOperation(
			"listObjectFieldReferenceOptions", "Objects", "Resolve an authorized relation input to a bounded id/name option page; query is raw text and Runtime builds the contains predicate",
			openAPIAdminSecurity(), openAPIPathParameter("objectKey", "Source object key"), openAPIPathParameter("fieldKey", "Source relation field key"),
			openAPIQueryParameter("query", "Raw name search text; SQL wildcard syntax is not accepted", map[string]any{"type": "string"}),
			openAPIQueryParameter("page", "One-based page", map[string]any{"type": "integer", "minimum": 1}),
			openAPIQueryParameter("limit", "Bounded page size", map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "default": 20}),
			openAPIQueryParameter("locale", "Resolve localized display fields in this BCP 47 locale", map[string]any{"type": "string"}),
			openAPIJSONResponse("Reference options", openAPIRef("ReferenceOptionPage")),
		),
	}
	paths["/records/{objectKey}/items/{recordID}"] = map[string]any{
		"get":    openAPIOperation("getObjectRecord", "Objects", "Get an object record", openAPIAdminSecurity(), openAPIPathParameter("objectKey", "Object key"), openAPIPathParameter("recordID", "Record ID"), openAPIJSONResponse("Record", openAPIRef("Record"))),
		"patch":  openAPIOperation("updateObjectRecord", "Objects", "Update an object record", openAPIAdminSecurity(), openAPIPathParameter("objectKey", "Object key"), openAPIPathParameter("recordID", "Record ID"), openAPIJSONRequest(openAPIObject(nil)), openAPIJSONResponse("Updated record", openAPIRef("Record"))),
		"delete": openAPIOperation("deleteObjectRecord", "Objects", "Delete an object record", openAPIAdminSecurity(), openAPIPathParameter("objectKey", "Object key"), openAPIPathParameter("recordID", "Record ID"), openAPIJSONResponse("Delete result", openAPIObject(nil))),
	}
	paths["/records/{objectKey}/items/{recordID}/profile/deactivate"] = map[string]any{
		"post": openAPIOperation("deactivateBusinessProfile", "Objects", "Deactivate one business profile without disabling its Identity login", openAPIAdminSecurity(), openAPIPathParameter("objectKey", "Profile object key"), openAPIPathParameter("recordID", "Profile record ID"), openAPIJSONRequest(openAPIRequiredObject([]string{"inactive_status", "reason"}, map[string]any{"inactive_status": map[string]any{"type": "string"}, "expected_updated_at": map[string]any{"type": "string"}, "reason": map[string]any{"type": "string"}})), openAPIJSONResponse("Deactivated profile", openAPIObject(nil))),
	}
	paths["/records/{objectKey}/items/{recordID}/profile/reactivate"] = map[string]any{
		"post": openAPIOperation("reactivateBusinessProfile", "Objects", "Reactivate one business profile and only its still-valid binding entitlements", openAPIAdminSecurity(), openAPIPathParameter("objectKey", "Profile object key"), openAPIPathParameter("recordID", "Profile record ID"), openAPIJSONRequest(openAPIRequiredObject([]string{"active_status", "reason"}, map[string]any{"active_status": map[string]any{"type": "string"}, "expected_updated_at": map[string]any{"type": "string"}, "reason": map[string]any{"type": "string"}})), openAPIJSONResponse("Reactivated profile", openAPIObject(nil))),
	}
	paths["/records/{objectKey}/items/{recordID}/references"] = map[string]any{
		"get": openAPIOperation("getObjectRecordReferences", "Objects", "Reference summary for a record", openAPIAdminSecurity(), openAPIPathParameter("objectKey", "Object key"), openAPIPathParameter("recordID", "Record ID"), openAPIJSONResponse("Record references", openAPIObject(nil))),
	}
	paths["/records/{objectKey}/items/{recordID}/related/{relatedObjectKey}"] = map[string]any{
		"get": openAPIOperation("listRelatedObjectRecords", "Objects", "Related records for a workbench tab", openAPIAdminSecurity(), openAPIPathParameter("objectKey", "Object key"), openAPIPathParameter("recordID", "Record ID"), openAPIPathParameter("relatedObjectKey", "Related object key"), openAPIJSONResponse("Related record page", openAPIRef("PageResult"))),
	}
	addRecordBatchOpenAPIPaths(paths)
	paths["/records/{objectKey}/actions"] = map[string]any{
		"get": openAPIOperation("listObjectActions", "Actions", "List available object actions", openAPIAdminSecurity(), openAPIPathParameter("objectKey", "Object key"), openAPIJSONResponse("Actions", openAPIArray(openAPIRef("Action")))),
	}
	paths["/records/{objectKey}/actions/{actionKey}"] = map[string]any{
		"post": openAPIOperation("executeObjectAction", "Actions", "Execute an object-scoped action", openAPIAdminSecurity(), openAPIPathParameter("objectKey", "Object key"), openAPIPathParameter("actionKey", "Action key"), openAPIJSONRequest(openAPIRef("ObjectActionRequest")), openAPIJSONResponse("Object action result", openAPIRef("ObjectActionResult"))),
	}
	paths["/records/{objectKey}/items/{recordID}/actions/{actionKey}"] = map[string]any{
		"post": openAPIOperation("executeRecordAction", "Actions", "Execute a record-scoped action", openAPIAdminSecurity(), openAPIPathParameter("objectKey", "Object key"), openAPIPathParameter("recordID", "Record ID"), openAPIPathParameter("actionKey", "Action key"), openAPIJSONRequest(openAPIRef("ActionRequest")), openAPIJSONResponse("Action result", openAPIRef("ActionResult"))),
	}
	paths["/references"] = map[string]any{
		"get": openAPIOperation("getBusinessReferenceGraph", "Business maintenance", "List domain configuration dependency nodes and edges", openAPIAdminSecurity(), openAPIJSONResponse("Reference graph", openAPIObject(nil))),
	}
	paths["/references/{resourceType}/{resourceKey}/impact"] = map[string]any{
		"get": openAPIOperation("getBusinessReferenceImpact", "Business maintenance", "List direct and indirect consumers before changing a resource", openAPIAdminSecurity(), openAPIPathParameter("resourceType", "Resource type"), openAPIPathParameter("resourceKey", "Resource key"), openAPIJSONResponse("Reference impact", openAPIObject(nil))),
	}
	paths["/authoring/snapshot"] = map[string]any{
		"get": openAPIOperation("getRuntimeAuthoringSnapshot", "Runtime authoring", "Read a bounded Runtime-native authoring index by default, page one resource category, read one exact resource, inspect runtime-state counts on demand, or explicitly request the legacy full snapshot.", openAPIPublicSecurity(), openAPIQueryParameter("projection", "Response projection.", map[string]any{"type": "string", "enum": []string{"index", "resources", "resource", "runtime-index", "full"}, "default": "index"}), openAPIQueryParameter("resource_type", "Resource type required by resources and resource projections.", map[string]any{"type": "string"}), openAPIQueryParameter("resource_key", "Resource key required by the resource projection.", map[string]any{"type": "string"}), openAPIQueryParameter("cursor", "Opaque cursor returned by the resources projection.", map[string]any{"type": "string"}), openAPIQueryParameter("limit", "Bounded resources page size.", map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "default": 25}), openAPIJSONResponse("Runtime authoring snapshot projection", openAPIObject(nil))),
	}
	paths["/authoring/validate"] = map[string]any{
		"post": openAPIOperation("validateRuntimeAuthoringState", "Runtime authoring", "Validate current Runtime configuration and coverage. An optional evidence plan is registered once and returns signed per-step tokens, replacing repeated scenario metadata on observed requests.", openAPIAdminSecurity(), openAPIJSONRequest(runtimeAuthoringCoverageRequestSchema()), openAPIJSONResponse("Runtime authoring validation report", openAPIObject(nil))),
	}
	paths["/authoring/verify-delivery"] = map[string]any{
		"post": openAPIOperation("verifyRuntimeAuthoringDelivery", "Runtime authoring", "Revalidate before ready. The compact form submits coverage plus flat Runtime receipts; Runtime reconstructs binding, scenarios, categories, states, and outcomes from signed observations.", openAPIAdminSecurity(), openAPIJSONRequest(runtimeAuthoringDeliveryEvidenceSchema()), openAPIJSONResponse("Runtime authoring delivery report", openAPIObject(nil))),
	}
	paths["/automation/rules"] = map[string]any{
		"get": openAPIOperation("listAutomationRules", "Automation", "List object lifecycle automation rules", openAPIAdminSecurity(), openAPIJSONResponse("Automation rules", openAPIArray(openAPIRef("AutomationRule")))),
	}
	paths["/automation/execution-catalog"] = map[string]any{
		"get": openAPIOperation("getAutomationExecutionCatalog", "Automation", "Inspect Runtime-instance lifecycle phases, operations, Connectors, and ready Connections. Plane owns static capability aggregation.", openAPIAdminSecurity(), openAPIJSONResponse("Automation execution catalog", openAPIRef("AutomationExecutionCatalog"))),
	}
	paths["/automation/executions"] = map[string]any{
		"get": openAPIOperation("listAutomationExecutions", "Automation", "Automation execution history and metrics", openAPIAdminSecurity(), openAPIJSONResponse("Execution history", openAPIObject(nil))),
	}
	paths["/automation/validate"] = map[string]any{
		"post": openAPIOperation("validateAutomationRule", "Automation", "Validate a draft lifecycle automation rule", openAPIAdminSecurity(), openAPIJSONRequest(openAPIRef("AutomationRule")), openAPIJSONResponse("Validation result", openAPIObject(nil))),
	}
	paths["/automation/simulate"] = map[string]any{
		"post": openAPIOperation("simulateAutomationRuleDraft", "Automation", "Simulate a draft lifecycle automation rule without a domain write", openAPIAdminSecurity(), openAPIJSONRequest(openAPIRef("AutomationSimulationRequest")), openAPIJSONResponse("Simulation result", openAPIRef("AutomationSimulationResult"))),
	}
	paths["/automation/rules/{ruleKey}"] = map[string]any{
		"get": openAPIOperation("getAutomationRule", "Automation", "Get a lifecycle automation rule", openAPIAdminSecurity(), openAPIPathParameter("ruleKey", "Automation rule key"), openAPIJSONResponse("Automation rule", openAPIRef("AutomationRule"))),
	}
	paths["/automation/rules/{ruleKey}/simulate"] = map[string]any{"post": openAPIOperation("simulateAutomationRule", "Automation", "Simulate a saved or edited lifecycle automation rule", openAPIAdminSecurity(), openAPIPathParameter("ruleKey", "Automation rule key"), openAPIJSONRequest(openAPIRef("AutomationSimulationRequest")), openAPIJSONResponse("Simulation result", openAPIRef("AutomationSimulationResult")))}
}

func openAPIRequiredObject(required []string, properties map[string]any) map[string]any {
	schema := openAPIObject(properties)
	schema["required"] = required
	return schema
}
