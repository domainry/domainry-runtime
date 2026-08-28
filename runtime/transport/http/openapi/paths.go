package openapi

import (
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	surfacemodel "github.com/domainry/domainry-runtime/runtime/domain/surface/model"
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
	paths["/objects/"+objectKey+"/records"] = map[string]any{
		"get":  openAPIOperation("list"+openAPIOperationName(objectKey)+"Records", tag, "List "+tag+" records", openAPIProductSurfaces(surfacemodel.ProductSurfaceBusinessWorkspace, surfacemodel.ProductSurfaceConsumerPortal), openAPIAdminSecurity(), openAPIQueryParameter("locale", "Resolve and search localized record fields in this BCP 47 locale.", map[string]any{"type": "string"}), openAPIJSONResponse("Record page", openAPIRef("PageResult"))),
		"post": openAPIOperation("create"+openAPIOperationName(objectKey)+"Record", tag, "Create "+tag+" record", openAPIProductSurfaces(surfacemodel.ProductSurfaceBusinessWorkspace, surfacemodel.ProductSurfaceConsumerPortal), openAPIAdminSecurity(), openAPIJSONRequest(createRequest), openAPIJSONResponse("Created record", recordSchema)),
	}
	paths["/objects/"+objectKey+"/records/import/preview"] = map[string]any{
		"post": openAPIOperation("preview"+openAPIOperationName(objectKey)+"Import", tag, "Preview CSV import for "+tag, openAPIProductSurfaces(surfacemodel.ProductSurfaceBusinessWorkspace), openAPIAdminSecurity(), openAPIJSONResponse("Import preview", openAPIObject(nil))),
	}
	paths["/objects/"+objectKey+"/records/import/apply"] = map[string]any{
		"post": openAPIOperation("apply"+openAPIOperationName(objectKey)+"Import", tag, "Apply CSV import for "+tag, openAPIProductSurfaces(surfacemodel.ProductSurfaceBusinessWorkspace), openAPIAdminSecurity(), openAPIJSONResponse("Import result", openAPIObject(nil))),
	}
	paths["/objects/"+objectKey+"/records/export"] = map[string]any{
		"get": openAPIOperation("export"+openAPIOperationName(objectKey)+"Records", tag, "Export "+tag+" records as CSV", openAPIAdminSecurity(), openAPIQueryParameter("locale", "Export resolved localized record fields in this BCP 47 locale.", map[string]any{"type": "string"}), openAPIResponse("CSV export", "text/csv", map[string]any{"type": "string"})),
	}
	paths["/objects/"+objectKey+"/records/{recordID}"] = map[string]any{
		"get":    openAPIOperation("get"+openAPIOperationName(objectKey)+"Record", tag, "Get "+tag+" record", openAPIAdminSecurity(), openAPIPathParameter("recordID", "Record ID"), openAPIQueryParameter("locale", "Resolve localized record fields in this BCP 47 locale.", map[string]any{"type": "string"}), openAPIJSONResponse("Record", recordSchema)),
		"patch":  openAPIOperation("update"+openAPIOperationName(objectKey)+"Record", tag, "Update "+tag+" record", openAPIAdminSecurity(), openAPIPathParameter("recordID", "Record ID"), openAPIJSONRequest(updateRequest), openAPIJSONResponse("Updated record", recordSchema)),
		"delete": openAPIOperation("delete"+openAPIOperationName(objectKey)+"Record", tag, "Delete "+tag+" record", openAPIAdminSecurity(), openAPIPathParameter("recordID", "Record ID"), openAPIResponse("Deleted", "application/json", openAPIObject(nil))),
	}
	paths["/objects/"+objectKey+"/actions"] = map[string]any{
		"get": openAPIOperation("list"+openAPIOperationName(objectKey)+"Actions", tag, "List available actions for "+tag, openAPIProductSurfaces(surfacemodel.ProductSurfaceBusinessWorkspace, surfacemodel.ProductSurfaceConsumerPortal), openAPIAdminSecurity(), openAPIJSONResponse("Actions", openAPIArray(openAPIRef("Action")))),
	}
}

func addRuntimeContractOpenAPIPaths(paths map[string]any) {
	addPartyOpenAPIPaths(paths)
	addReportQueryOpenAPIPaths(paths)
	paths["/i18n/locales"] = map[string]any{
		"get": openAPIOperation("listI18nLocales", "I18n", "Available runtime locales", openAPIPublicSecurity(), openAPIJSONResponse("Locales", openAPIArray(openAPIObject(nil)))),
	}
	paths["/i18n/resources"] = map[string]any{
		"get": openAPIOperation("getI18nResources", "I18n", "Localized runtime resources", openAPIPublicSecurity(), openAPIJSONResponse("Resources", openAPIObject(nil))),
	}
	paths["/dictionaries/{dictionaryKey}/items"] = map[string]any{
		"get": openAPIOperation("listDictionaryItems", "Dictionaries", "List dictionary items", openAPIAdminSecurity(), openAPIPathParameter("dictionaryKey", "Dictionary key"), openAPIJSONResponse("Dictionary items", openAPIArray(openAPIObject(nil)))),
	}
	paths["/reports/{reportKey}/snapshots/refresh"] = map[string]any{
		"post": openAPIOperation("refreshReportSnapshot", "Reports", "Refresh an authorization-scoped report snapshot", openAPIAdminSecurity(), openAPIPathParameter("reportKey", "Report key"), openAPIJSONResponse("Report snapshot", openAPIObject(nil))),
	}
	prepareReportExport := openAPIOperation("prepareReportExport", "Reports", "Count the same authorized Report and return a synchronous governed CSV through 1000 rows or accept an asynchronous job above 1000. The server atomically replays the same active job or unexpired intact artifact for the canonical report, parameters, source, requester, authorization/data scope, format, and result version.", openAPIAdminSecurity(), openAPIPathParameter("reportKey", "Report key"), openAPIPathParameter("objectKey", "Object key"), openAPIParameter{Value: openAPIHeaderParameter("Idempotency-Key", "Required transport idempotency key; canonical server fingerprint dedupe also applies across different caller keys", true)}, openAPIJSONRequest(openAPIRequiredObject([]string{"audit_id"}, map[string]any{"audit_id": map[string]any{"type": "string"}, "scope": openAPIReportExportScopeSchema()})), openAPIJSONResponse("Accepted report export job", reportExportJobSchema()))
	synchronousReportExport := openAPIResponseValue("Governed synchronous Report CSV (exact authorized total <= 1000)", "text/csv", map[string]any{"type": "string", "format": "binary"})
	synchronousReportExport["headers"] = map[string]any{
		"X-Report-Export-Artifact-ID":    map[string]any{"schema": map[string]any{"type": "string"}},
		"X-Content-SHA256":               map[string]any{"schema": map[string]any{"type": "string"}},
		"X-Report-Export-Download-Token": map[string]any{"schema": map[string]any{"type": "string"}},
		"X-Report-Export-Row-Count":      map[string]any{"schema": map[string]any{"type": "integer"}},
		"X-Report-Export-Expires-At":     map[string]any{"schema": map[string]any{"type": "string", "format": "date-time"}},
	}
	prepareReportExport["responses"] = map[string]any{"200": synchronousReportExport, "202": openAPIResponseValue("Accepted asynchronous Report export job (exact authorized total > 1000)", "application/json", reportExportJobSchema()), "default": openAPIJSONResponse("Error", openAPIRef("Error")).Value}
	paths["/reports/{reportKey}/exports/{objectKey}/prepare"] = map[string]any{"post": prepareReportExport}
	paths["/report-exports/{jobID}"] = map[string]any{"get": openAPIOperation("getReportExportJob", "Reports", "Get requester-owned report export status and progress", openAPIAdminSecurity(), openAPIPathParameter("jobID", "Report export job ID"), openAPIJSONResponse("Report export job", reportExportJobSchema()))}
	paths["/report-exports/{jobID}/cancel"] = map[string]any{"post": openAPIOperation("cancelReportExport", "Reports", "Cancel a requester-owned report export job", openAPIAdminSecurity(), openAPIPathParameter("jobID", "Report export job ID"), openAPIJSONResponse("Cancelled report export job", reportExportJobSchema()))}
	paths["/report-exports/downloads/{token}"] = map[string]any{
		"get": openAPIOperation("downloadReportExport", "Reports", "Download an approved report export before expiry", openAPIAdminSecurity(), openAPIPathParameter("token", "Download token"), openAPIResponse("Report export", "text/csv", map[string]any{"type": "string", "format": "binary"})),
	}
	paths["/business/audit-event-exports"] = map[string]any{
		"post": openAPIOperation("prepareBusinessAuditEventExport", "Audit", "Prepare immutable server-rendered business audit-event CSV bytes under current workspace and data scope", openAPIProductSurfaces(surfacemodel.ProductSurfaceBusinessWorkspace), openAPIAdminSecurity(), openAPIParameter{Value: openAPIHeaderParameter("Idempotency-Key", "Stable caller key for immutable export preparation", true)}, openAPIJSONRequest(openAPIBusinessAuditExportRequestSchema()), openAPIJSONResponse("Prepared business audit-event export", openAPIBusinessAuditExportPreparedSchema())),
	}
	paths["/business/audit-event-exports/downloads/{token}"] = map[string]any{
		"get": openAPIOperation("downloadBusinessAuditEventExport", "Audit", "Download a short-lived business audit-event export after current permission and data-scope revalidation", openAPIProductSurfaces(surfacemodel.ProductSurfaceBusinessWorkspace), openAPIAdminSecurity(), openAPIPathParameter("token", "Short-lived download token"), openAPIResponse("Business audit-event CSV", "text/csv", map[string]any{"type": "string", "format": "binary"})),
	}
	addUploadOpenAPIPaths(paths)
	paths["/objects/{objectKey}/records"] = map[string]any{
		"get":  openAPIOperation("listObjectRecords", "Objects", "List object records", openAPIAdminSecurity(), openAPIPathParameter("objectKey", "Object key"), openAPIJSONResponse("Record page", openAPIRef("PageResult"))),
		"post": openAPIOperation("createObjectRecord", "Objects", "Create an object record", openAPIAdminSecurity(), openAPIPathParameter("objectKey", "Object key"), openAPIJSONRequest(openAPIObject(nil)), openAPIJSONResponse("Created record", openAPIRef("Record"))),
	}
	paths["/objects/{objectKey}/records/{recordID}"] = map[string]any{
		"get":    openAPIOperation("getObjectRecord", "Objects", "Get an object record", openAPIAdminSecurity(), openAPIPathParameter("objectKey", "Object key"), openAPIPathParameter("recordID", "Record ID"), openAPIJSONResponse("Record", openAPIRef("Record"))),
		"patch":  openAPIOperation("updateObjectRecord", "Objects", "Update an object record", openAPIAdminSecurity(), openAPIPathParameter("objectKey", "Object key"), openAPIPathParameter("recordID", "Record ID"), openAPIJSONRequest(openAPIObject(nil)), openAPIJSONResponse("Updated record", openAPIRef("Record"))),
		"delete": openAPIOperation("deleteObjectRecord", "Objects", "Delete an object record", openAPIAdminSecurity(), openAPIPathParameter("objectKey", "Object key"), openAPIPathParameter("recordID", "Record ID"), openAPIJSONResponse("Delete result", openAPIObject(nil))),
	}
	paths["/objects/{objectKey}/records/{recordID}/deactivate-profile"] = map[string]any{
		"post": openAPIOperation("deactivateBusinessProfile", "Objects", "Deactivate one business profile without terminating Workforce or disabling its login", openAPIAdminSecurity(), openAPIPathParameter("objectKey", "Profile object key"), openAPIPathParameter("recordID", "Profile record ID"), openAPIJSONRequest(openAPIRequiredObject([]string{"inactive_status", "reason"}, map[string]any{"inactive_status": map[string]any{"type": "string"}, "expected_updated_at": map[string]any{"type": "string"}, "reason": map[string]any{"type": "string"}})), openAPIJSONResponse("Deactivated profile", openAPIObject(nil))),
	}
	paths["/objects/{objectKey}/records/{recordID}/reactivate-profile"] = map[string]any{
		"post": openAPIOperation("reactivateBusinessProfile", "Objects", "Reactivate one business profile and only its still-valid binding entitlements", openAPIAdminSecurity(), openAPIPathParameter("objectKey", "Profile object key"), openAPIPathParameter("recordID", "Profile record ID"), openAPIJSONRequest(openAPIRequiredObject([]string{"active_status", "reason"}, map[string]any{"active_status": map[string]any{"type": "string"}, "expected_updated_at": map[string]any{"type": "string"}, "reason": map[string]any{"type": "string"}})), openAPIJSONResponse("Reactivated profile", openAPIObject(nil))),
	}
	paths["/objects/{objectKey}/records/{recordID}/references"] = map[string]any{
		"get": openAPIOperation("getObjectRecordReferences", "Objects", "Reference summary for a record", openAPIAdminSecurity(), openAPIPathParameter("objectKey", "Object key"), openAPIPathParameter("recordID", "Record ID"), openAPIJSONResponse("Record references", openAPIObject(nil))),
	}
	paths["/objects/{objectKey}/records/{recordID}/related/{relatedObjectKey}"] = map[string]any{
		"get": openAPIOperation("listRelatedObjectRecords", "Objects", "Related records for a workbench tab", openAPIAdminSecurity(), openAPIPathParameter("objectKey", "Object key"), openAPIPathParameter("recordID", "Record ID"), openAPIPathParameter("relatedObjectKey", "Related object key"), openAPIJSONResponse("Related record page", openAPIRef("PageResult"))),
	}
	addRecordBatchOpenAPIPaths(paths)
	paths["/objects/{objectKey}/actions"] = map[string]any{
		"get": openAPIOperation("listObjectActions", "Actions", "List available object actions", openAPIAdminSecurity(), openAPIPathParameter("objectKey", "Object key"), openAPIJSONResponse("Actions", openAPIArray(openAPIRef("Action")))),
	}
	paths["/objects/{objectKey}/actions/{actionKey}/run"] = map[string]any{
		"post": openAPIOperation("executeObjectAction", "Actions", "Execute an object-scoped action", openAPIAdminSecurity(), openAPIPathParameter("objectKey", "Object key"), openAPIPathParameter("actionKey", "Action key"), openAPIJSONRequest(openAPIRef("ObjectActionRequest")), openAPIJSONResponse("Object action result", openAPIRef("ObjectActionResult"))),
	}
	paths["/objects/{objectKey}/records/{recordID}/actions/{actionKey}"] = map[string]any{
		"post": openAPIOperation("executeRecordAction", "Actions", "Execute a record-scoped action", openAPIAdminSecurity(), openAPIPathParameter("objectKey", "Object key"), openAPIPathParameter("recordID", "Record ID"), openAPIPathParameter("actionKey", "Action key"), openAPIJSONRequest(openAPIRef("ActionRequest")), openAPIJSONResponse("Action result", openAPIRef("ActionResult"))),
	}
	paths["/tenant-admin/execution-capabilities"] = map[string]any{
		"get": openAPIOperation("getExecutionCapabilities", "Metadata", "List the active Business Action, Automation Rule, and Workflow execution contracts", openAPIAdminSecurity(), openAPIJSONResponse("Execution capability catalog", openAPIObject(nil))),
	}
	paths["/domain-reference-graph"] = map[string]any{
		"get": openAPIOperation("getBusinessReferenceGraph", "Business maintenance", "List domain configuration dependency nodes and edges", openAPIAdminSecurity(), openAPIJSONResponse("Reference graph", openAPIObject(nil))),
	}
	paths["/domain-references/{resourceType}/{resourceKey}"] = map[string]any{
		"get": openAPIOperation("getBusinessReferenceImpact", "Business maintenance", "List direct and indirect consumers before changing a resource", openAPIAdminSecurity(), openAPIPathParameter("resourceType", "Resource type"), openAPIPathParameter("resourceKey", "Resource key"), openAPIJSONResponse("Reference impact", openAPIObject(nil))),
	}
	paths["/tenant-admin/change-plans/validate"] = map[string]any{
		"post": openAPIOperation("validateBusinessChangePlan", "Business maintenance", "Validate an incremental domain-system change plan against current Snapshot, contract, ownership, references, and review policy without applying it", openAPIAdminSecurity(), openAPIJSONRequest(openAPIObject(nil)), openAPIJSONResponse("Change plan validation", openAPIObject(nil))),
	}
	paths["/tenant-admin/change-plans/{planID}"] = map[string]any{
		"get": openAPIOperation("getBusinessChangePlanDraft", "Business maintenance", "Read the current high-risk configuration plan draft or frozen published plan", openAPIAdminSecurity(), openAPIPathParameter("planID", "Change plan ID"), openAPIJSONResponse("Change plan draft", openAPIObject(nil))),
		"put": openAPIOperation("saveBusinessChangePlanDraft", "Business maintenance", "Create or optimistically update a high-risk configuration plan draft", openAPIAdminSecurity(), openAPIPathParameter("planID", "Change plan ID"), openAPIJSONRequest(openAPIObject(nil)), openAPIJSONResponse("Change plan draft", openAPIObject(nil))),
	}
	paths["/tenant-admin/change-plans/{planID}/review"] = map[string]any{
		"post": openAPIOperation("submitBusinessChangePlanForReview", "Business maintenance", "Validate and freeze a draft revision for independent approval", openAPIAdminSecurity(), openAPIPathParameter("planID", "Change plan ID"), openAPIJSONRequest(openAPIObject(nil)), openAPIJSONResponse("Change plan review state", openAPIObject(nil))),
	}
	paths["/tenant-admin/change-plans/{planID}/clone-current"] = map[string]any{
		"post": openAPIOperation("cloneCurrentBusinessSystemDraft", "Business maintenance", "Clone all active Metadata definitions and pinned resource hashes into a workspace-scoped editable system draft", openAPIAdminSecurity(), openAPIPathParameter("planID", "Change plan ID"), openAPIJSONRequest(openAPIObject(nil)), openAPIJSONResponse("Cloned change plan draft", openAPIObject(nil))),
	}
	paths["/tenant-admin/change-plans/{planID}/simulate"] = map[string]any{
		"post": openAPIOperation("simulateBusinessChangePlanScenarios", "Business maintenance", "Run typed acceptance scenarios against the fully composed Metadata candidate without publishing or executing side effects", openAPIAdminSecurity(), openAPIPathParameter("planID", "Change plan ID"), openAPIJSONRequest(openAPIObject(nil)), openAPIJSONResponse("Acceptance scenario simulation", openAPIObject(nil))),
	}
	paths["/tenant-admin/change-plans/{planID}/approve"] = map[string]any{
		"post": openAPIOperation("approveBusinessChangePlan", "Business maintenance", "Independently approve the exact reviewed draft revision after revalidation", openAPIAdminSecurity(), openAPIPathParameter("planID", "Change plan ID"), openAPIJSONRequest(openAPIObject(nil)), openAPIJSONResponse("Approved change plan state", openAPIObject(nil))),
	}
	paths["/tenant-admin/change-plans/{planID}/export"] = map[string]any{
		"get": openAPIOperation("exportBusinessSystemPackage", "Business maintenance", "Export an approved or published revision as a deterministic system package with resource hashes, dependencies, empty-workspace apply plan, and acceptance scenarios", openAPIAdminSecurity(), openAPIPathParameter("planID", "Change plan ID"), openAPIJSONResponse("System package", openAPIObject(nil))),
	}
	paths["/tenant-admin/change-plans/{planID}/package"] = map[string]any{
		"put": openAPIOperation("importBusinessSystemPackage", "Business maintenance", "Verify a current-contract system package and bind it to an empty workspace as a normal editable draft; review, approval, and atomic publish remain mandatory", openAPIAdminSecurity(), openAPIPathParameter("planID", "Change plan ID"), openAPIJSONRequest(openAPIObject(nil)), openAPIJSONResponse("Imported change plan draft", openAPIObject(nil))),
	}
	paths["/domain-maintenance/rollback-policy"] = map[string]any{
		"get": openAPIOperation("getBusinessRollbackPolicy", "Business maintenance", "Describe automatic, compensating, and manual rollback behavior without mutating execution evidence", openAPIAdminSecurity(), openAPIJSONResponse("Rollback policy", openAPIObject(nil))),
	}
	paths["/tenant-admin/platform-capabilities"] = map[string]any{
		"get": openAPIOperation("getPlatformCapabilities", "Capabilities", "Temporarily unauthenticated builder discovery of the Runtime authoring contract and target-instance capability bindings", openAPIPublicSecurity(), openAPIJSONResponse("Platform capabilities", openAPIObject(nil))),
	}
	paths["/tenant-admin/platform-capabilities/index"] = map[string]any{
		"get": openAPIOperation("getPlatformCapabilityIndex", "Capabilities", "Read the compact Runtime authoring capability domain index", openAPIPublicSecurity(), openAPIJSONResponse("Platform capability index", openAPIObject(nil))),
	}
	paths["/tenant-admin/platform-capabilities/domains/{domainKey}"] = map[string]any{
		"get": openAPIOperation("getPlatformCapabilityDomain", "Capabilities", "Read summaries for one Runtime authoring capability domain", openAPIPublicSecurity(), openAPIPathParameter("domainKey", "Capability domain key"), openAPIJSONResponse("Platform capability domain", openAPIObject(nil))),
	}
	paths["/tenant-admin/platform-capabilities/capabilities/{capabilityKey}"] = map[string]any{
		"get": openAPIOperation("getPlatformCapabilityDetail", "Capabilities", "Read one Runtime authoring capability contract and its target-instance bindings", openAPIPublicSecurity(), openAPIPathParameter("capabilityKey", "Capability key"), openAPIJSONResponse("Platform capability detail", openAPIObject(nil))),
	}
	paths["/tenant-admin/platform-capabilities/references/{kind}"] = map[string]any{
		"get": openAPIOperation("getPlatformCapabilityReferences", "Capabilities", "Read target-instance reference values for one authoring parameter kind", openAPIPublicSecurity(), openAPIPathParameter("kind", "Reference kind"), openAPIJSONResponse("Platform capability references", openAPIObject(nil))),
	}
	paths["/domain-system-snapshot"] = map[string]any{
		"get": openAPIOperation("getBusinessSystemSnapshot", "Capabilities", "Temporarily unauthenticated Runtime-native metadata, governance, workflow lifecycle and resource ownership snapshot for builder Maintain mode", openAPIPublicSecurity(), openAPIJSONResponse("Business system snapshot", openAPIObject(nil))),
	}
	paths["/domain-system-validation"] = map[string]any{
		"post": openAPIOperation("validateRuntimeAuthoringState", "Capabilities", "Validate the current mutable Runtime and its requirement-to-capability/resource/scenario coverage before lifecycle verification", openAPIAdminSecurity(), openAPIJSONRequest(runtimeAuthoringCoverageRequestSchema()), openAPIJSONResponse("Runtime authoring validation report", openAPIObject(nil))),
	}
	paths["/domain-system-delivery-verification"] = map[string]any{
		"post": openAPIOperation("verifyRuntimeAuthoringDelivery", "Capabilities", "Revalidate the current Runtime and bound scenario evidence before transitioning an owned Runtime to ready", openAPIAdminSecurity(), openAPIJSONRequest(openAPIObject(nil)), openAPIJSONResponse("Runtime authoring delivery report", openAPIObject(nil))),
	}
	paths["/automation-rules"] = map[string]any{
		"get": openAPIOperation("listAutomationRules", "Automation", "List object lifecycle automation rules", openAPIAdminSecurity(), openAPIJSONResponse("Automation rules", openAPIArray(openAPIRef("AutomationRule")))),
	}
	paths["/automation-rules/capabilities"] = map[string]any{
		"get": openAPIOperation("getAutomationCapabilities", "Automation", "Catalog lifecycle phases, operations, Action types, Connectors, operations, and ready Connections", openAPIAdminSecurity(), openAPIJSONResponse("Automation capabilities", openAPIRef("AutomationCapabilities"))),
	}
	paths["/automation-rules/executions"] = map[string]any{
		"get": openAPIOperation("listAutomationExecutions", "Automation", "Automation execution history and metrics", openAPIAdminSecurity(), openAPIJSONResponse("Execution history", openAPIObject(nil))),
	}
	paths["/automation-rules/validate"] = map[string]any{
		"post": openAPIOperation("validateAutomationRule", "Automation", "Validate a draft lifecycle automation rule", openAPIAdminSecurity(), openAPIJSONRequest(openAPIRef("AutomationRule")), openAPIJSONResponse("Validation result", openAPIObject(nil))),
	}
	paths["/automation-rules/simulate"] = map[string]any{
		"post": openAPIOperation("simulateAutomationRuleDraft", "Automation", "Simulate a draft lifecycle automation rule without a domain write", openAPIAdminSecurity(), openAPIJSONRequest(openAPIRef("AutomationSimulationRequest")), openAPIJSONResponse("Simulation result", openAPIRef("AutomationSimulationResult"))),
	}
	paths["/automation-rules/{ruleKey}"] = map[string]any{
		"get": openAPIOperation("getAutomationRule", "Automation", "Get a lifecycle automation rule", openAPIAdminSecurity(), openAPIPathParameter("ruleKey", "Automation rule key"), openAPIJSONResponse("Automation rule", openAPIRef("AutomationRule"))),
	}
	paths["/automation-rules/{ruleKey}/simulate"] = map[string]any{"post": openAPIOperation("simulateAutomationRule", "Automation", "Simulate a saved or edited lifecycle automation rule", openAPIAdminSecurity(), openAPIPathParameter("ruleKey", "Automation rule key"), openAPIJSONRequest(openAPIRef("AutomationSimulationRequest")), openAPIJSONResponse("Simulation result", openAPIRef("AutomationSimulationResult")))}
}

func openAPIRequiredObject(required []string, properties map[string]any) map[string]any {
	schema := openAPIObject(properties)
	schema["required"] = required
	return schema
}
