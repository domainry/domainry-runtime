package openapi

import (
	"strings"

	"github.com/domainry/domainry-foundation/modulehttp"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

func runtimeAuthoringCoverageRequestSchema() map[string]any {
	return openAPIObject(map[string]any{
		"coverage":      runtimeAuthoringCoverageLedgerSchema(),
		"evidence_plan": runtimeAuthoringEvidencePlanSchema(),
	})
}

func runtimeAuthoringEvidencePlanSchema() map[string]any {
	step := openAPIRequiredObject([]string{"step_id", "label", "method", "path", "expected_status"}, map[string]any{
		"step_id": map[string]any{"type": "string"}, "label": map[string]any{"type": "string"},
		"observation": map[string]any{"type": "string", "enum": []string{"before_state", "after_state"}},
		"method":      map[string]any{"type": "string"}, "path": map[string]any{"type": "string"},
		"expected_status": openAPIArray(map[string]any{"type": "integer", "minimum": 100, "maximum": 599}),
	})
	scenario := openAPIRequiredObject([]string{"scenario_id", "categories", "steps"}, map[string]any{
		"scenario_id": map[string]any{"type": "string"},
		"categories":  openAPIArray(map[string]any{"type": "string", "enum": changeplanmodel.RuntimeAuthoringRequiredScenarioCategories}),
		"steps":       openAPIArray(step),
	})
	return openAPIRequiredObject([]string{"version", "scenarios"}, map[string]any{
		"version":   map[string]any{"type": "string", "const": changeplanmodel.RuntimeAuthoringEvidencePlanVersion},
		"scenarios": openAPIArray(scenario),
	})
}

func runtimeAuthoringCoverageLedgerSchema() map[string]any {
	return openAPIObject(map[string]any{
		"version": map[string]any{"type": "string", "const": changeplanmodel.RuntimeAuthoringCoverageLedgerVersion},
		"requirements": openAPIArray(openAPIObject(map[string]any{
			"requirement_id": map[string]any{"type": "string"}, "capability_keys": openAPIArray(map[string]any{"type": "string"}),
			"resources": openAPIArray(openAPIObject(map[string]any{
				"resource_type": map[string]any{"type": "string"}, "resource_key": map[string]any{"type": "string"},
			})),
			"scenario_ids": openAPIArray(map[string]any{"type": "string"}),
		})),
	})
}

func runtimeAuthoringDeliveryEvidenceSchema() map[string]any {
	binding := openAPIRequiredObject([]string{"runtime_version", "contract_hash", "instance_hash", "snapshot_hash", "coverage_hash", "resource_hashes"}, map[string]any{
		"runtime_version": map[string]any{"type": "string"}, "contract_hash": map[string]any{"type": "string"},
		"instance_hash": map[string]any{"type": "string"}, "snapshot_hash": map[string]any{"type": "string"},
		"coverage_hash": map[string]any{"type": "string"}, "resource_hashes": openAPIObject(nil),
	})
	step := openAPIRequiredObject([]string{"runtime_receipt"}, map[string]any{
		"runtime_receipt": map[string]any{"type": "string", "description": "Opaque Runtime-issued HMAC receipt returned by the observed HTTP call."},
	})
	scenario := openAPIRequiredObject([]string{"version", "scenario_id", "steps"}, map[string]any{
		"version":     map[string]any{"type": "string", "const": changeplanmodel.RuntimeAuthoringScenarioEvidenceVersion},
		"scenario_id": map[string]any{"type": "string"}, "steps": openAPIArray(step),
	})
	return openAPIRequiredObject([]string{"coverage"}, map[string]any{
		"version": map[string]any{"type": "string", "const": changeplanmodel.RuntimeAuthoringDeliveryEvidenceVersion},
		"binding": binding, "coverage": runtimeAuthoringCoverageLedgerSchema(),
		"scenarios": openAPIArray(scenario),
		"receipts":  openAPIArray(map[string]any{"type": "string", "description": "Runtime-issued receipts. When present, Runtime reconstructs scenarios and binding without duplicated caller-authored wrappers."}),
	})
}

func runtimeAuthoringEvidenceOpenAPIExtension() map[string]any {
	return map[string]any{
		"version": changeplanmodel.RuntimeAuthoringEvidenceCollectionVersion, "trust_policy": changeplanmodel.RuntimeAuthoringEvidenceTrustPolicy,
		"step_receipt_version": changeplanmodel.RuntimeAuthoringStepReceiptVersion,
		"request_headers": map[string]any{
			"evidence_step_token": changeplanmodel.RuntimeAuthoringEvidenceStepTokenHeader,
			"builder_task":        changeplanmodel.RuntimeAuthoringBuilderTaskHeader, "scenario_id": changeplanmodel.RuntimeAuthoringScenarioIDHeader,
			"scenario_categories": changeplanmodel.RuntimeAuthoringScenarioCategoriesHeader, "step_label": changeplanmodel.RuntimeAuthoringStepLabelHeader,
			"step_observation": changeplanmodel.RuntimeAuthoringStepObservationHeader, "expected_status": changeplanmodel.RuntimeAuthoringExpectedStatusHeader,
			"snapshot_hash": changeplanmodel.RuntimeAuthoringSnapshotHashHeader, "coverage_hash": changeplanmodel.RuntimeAuthoringCoverageHashHeader,
		},
		"response_headers":           map[string]any{"step_receipt": changeplanmodel.RuntimeAuthoringStepReceiptHeader, "evidence_error": changeplanmodel.RuntimeAuthoringEvidenceErrorHeader},
		"streaming_path_restriction": changeplanmodel.RuntimeAuthoringEvidenceStreamingPathRestriction,
	}
}

// addBusinessBuilderOpenAPIPaths publishes domain-system authoring and
// administrator recovery APIs that are not schema-derived object CRUD paths.
func addBusinessBuilderOpenAPIPaths(paths map[string]any) {
	addBuilderPath(paths, "/business/runtime-schema", "Business Runtime Schema", "get")
	addBuilderPath(paths, "/portal/runtime-schema", "Portal Runtime Schema", "get")
	for _, prefix := range []string{"/business", "/portal"} {
		addBuilderPath(paths, prefix+"/notifications", "Notification Inbox", "get")
		addBuilderPath(paths, prefix+"/notifications/facets", "Notification Inbox", "get")
		addBuilderPath(paths, prefix+"/notifications/unread-count", "Notification Inbox", "get")
		addBuilderPath(paths, prefix+"/notifications/stream", "Notification Inbox", "get")
		addBuilderPath(paths, prefix+"/notifications/read-all", "Notification Inbox", "post")
		addBuilderPath(paths, prefix+"/notifications/saved-views", "Notification Inbox", "get")
		addBuilderPath(paths, prefix+"/notifications/saved-views/{viewKey}", "Notification Inbox", "put", "delete")
		addBuilderPath(paths, prefix+"/notifications/delegations", "Notification Inbox", "get")
		addBuilderPath(paths, prefix+"/notifications/delegations/{delegationID}", "Notification Inbox", "put", "delete")
		addBuilderPath(paths, prefix+"/notifications/delegated-owners", "Notification Inbox", "get")
		addBuilderPath(paths, prefix+"/notifications/{notificationID}", "Notification Inbox", "get")
		addBuilderPath(paths, prefix+"/notifications/{notificationID}/actions/{actionKey}/resolve", "Notification Inbox", "get")
		addBuilderPath(paths, prefix+"/notifications/{notificationID}/acknowledge", "Notification Inbox", "post")
		for _, mutation := range []string{"read", "unread", "archive", "restore"} {
			addBuilderPath(paths, prefix+"/notifications/{notificationID}/"+mutation, "Notification Inbox", "post")
		}
		addBuilderPath(paths, prefix+"/notification-preferences", "Notification Inbox", "get", "put")
	}
	addBuilderPath(paths, "/notifications/templates", "Notification Governance", "get")
	addBuilderPath(paths, "/notifications/publications", "Notification Governance", "get")
	addBuilderPath(paths, "/notifications/publications/{publicationID}/approve", "Notification Governance", "post")
	addBuilderPath(paths, "/notifications/publications/{publicationID}/reject", "Notification Governance", "post")
	addBuilderPath(paths, "/notifications/publications/{publicationID}/cancel", "Notification Governance", "post")
	addBuilderPath(paths, "/notifications/capabilities", "Notification Governance", "get")
	addBuilderPath(paths, "/notifications/policy", "Notification Governance", "get", "put")
	addBuilderPath(paths, "/notifications/preferences", "Notification Governance", "get")
	addBuilderPath(paths, "/notifications/preferences/{recipientKey}", "Notification Governance", "put")
	addBuilderPath(paths, "/notifications/metrics", "Notification Governance", "get")
	addBuilderPath(paths, "/notifications/deliveries", "Notification Governance", "get")
	addBuilderPath(paths, "/notifications/governance/catalog", "Notification Governance", "get")
	addBuilderPath(paths, "/notifications/governance/inbox-metrics", "Notification Governance", "get")
	addBuilderPath(paths, "/notifications/templates/preview", "Notification Governance", "post")
	addBuilderPath(paths, "/notifications/templates/{templateKey}", "Notification Governance", "get")
	addBuilderPath(paths, "/notifications/templates/{templateKey}/draft", "Notification Governance", "put")
	addBuilderPath(paths, "/notifications/templates/{templateKey}/publish", "Notification Governance", "post")
	addBuilderPath(paths, "/notifications/templates/{templateKey}/publication-requests", "Notification Governance", "post")
	addBuilderPath(paths, "/notifications/templates/{templateKey}/disable", "Notification Governance", "post")
	addBuilderPath(paths, "/notifications/templates/{templateKey}/preview", "Notification Governance", "post")
	addBuilderPath(paths, "/notifications/templates/{templateKey}/versions", "Notification Governance", "get")
	addBuilderPath(paths, "/notifications/templates/{templateKey}/versions/{version}/restore-draft", "Notification Governance", "post")
	addBuilderPath(paths, "/tenant-admin/runtime-schema", "Metadata Administration", "get")
	addBuilderPath(paths, "/tenant-admin/metadata/definitions/{resourceType}/{resourceKey}/validate", "Metadata Administration", "post")
	addBuilderPath(paths, "/tenant-admin/metadata/migration-plan", "Metadata Administration", "get")
	addBuilderPath(paths, "/tenant-admin/metadata/objects/{objectKey}/record-count", "Metadata Administration", "get")
	addBuilderPath(paths, "/tenant-admin/metadata/capabilities", "Metadata Administration", "get")
	addBuilderPath(paths, "/operations/metadata/diagnostics", "Metadata Operations", "get")
	addBuilderPath(paths, "/business/records/stream", "Business Records", "get")
	addBuilderPath(paths, "/business/workflow/processes", "Workflow Business", "get")
	addBuilderPath(paths, "/business/workflow/processes/{processID}", "Workflow Business", "get")
	addBuilderPath(paths, "/business/workflow/team-tasks", "Workflow Business", "get")
	addBuilderPath(paths, "/business/workflow/tasks", "Workflow Business", "get")
	addBuilderPath(paths, "/business/workflow/processes/{processID}/withdraw", "Workflow Business", "post")
	addBuilderPath(paths, "/business/workflow/processes/{processID}/retry", "Workflow Business", "post")
	addBuilderPath(paths, "/business/workflow/tasks/{taskID}/approve", "Workflow Business", "post")
	addBuilderPath(paths, "/business/workflow/tasks/{taskID}/reject", "Workflow Business", "post")
	addBuilderPath(paths, "/business/workflow/tasks/{taskID}/return", "Workflow Business", "post")
	addBuilderPath(paths, "/business/workflows/{workflowKey}/run", "Workflow Business", "post")
	addBuilderPath(paths, "/portal/workflows/{workflowKey}/run", "Workflow Portal", "post")
	addBusinessStreamOpenAPIPaths(paths)
	annotateNotificationRuntimeClient(paths)
	addBuilderPath(paths, "/operations/workflow/executions", "Workflow Operations", "get")
	addBuilderPath(paths, "/operations/workflow/executions/process", "Workflow Operations", "post")
	addBuilderPath(paths, "/operations/workflow/executions/{executionID}/retry", "Workflow Operations", "post")
	addBuilderPath(paths, "/operations/workflow/executions/{executionID}/resolve", "Workflow Operations", "post")
	addBuilderPath(paths, "/operations/workflow/processes", "Workflow Operations", "get")
	addBuilderPath(paths, "/operations/workflow/processes/{processID}", "Workflow Operations", "get")
	addBuilderPath(paths, "/operations/workflow/processes/{processID}/retry", "Workflow Operations", "post")
	addBuilderPath(paths, "/operations/workflow/processes/{processID}/resolve", "Workflow Operations", "post")
	addBuilderPath(paths, "/tenant-admin/workflows/authoring-fragments/{capabilityKey}/validate", "Workflow Administration", "post")
	addBuilderPath(paths, "/tenant-admin/workflows/{workflowKey}/validate", "Workflow Administration", "post")
	addBuilderPath(paths, "/tenant-admin/workflows/{workflowKey}/simulate", "Workflow Administration", "post")
	addSchedulerOpenAPIPaths(paths)
	addBuilderPath(paths, "/automation-rules/authoring-fragments/{capabilityKey}/validate", "Automation", "post")

}

func addSchedulerOpenAPIPaths(paths map[string]any) {
	contract, err := schedulersdk.SchedulerHTTPSurfaceContract()
	if err != nil {
		panic("compile Scheduler OpenAPI surface: " + err.Error())
	}
	routes := make(map[string]modulehttp.Route, len(contract.Routes))
	for _, source := range contract.Routes {
		route, routeErr := modulehttp.RouteFromAction(source.Action)
		if routeErr != nil {
			panic("project Scheduler OpenAPI route: " + routeErr.Error())
		}
		routes[route.Pattern()] = route
	}
	for pattern, sourceOperation := range contract.OpenAPI {
		method, path, found := strings.Cut(strings.TrimSpace(pattern), " ")
		if !found || strings.TrimSpace(method) == "" || strings.TrimSpace(path) == "" {
			panic("Scheduler OpenAPI operation has invalid route pattern: " + pattern)
		}
		route, found := routes[pattern]
		if !found {
			panic("Scheduler OpenAPI operation has no source Action: " + pattern)
		}
		operation := cloneOpenAPIOperation(sourceOperation)
		responses, _ := operation["responses"].(map[string]any)
		if responses == nil {
			responses = map[string]any{}
		}
		responses["default"] = openAPIJSONResponse("Error", openAPIRef("Error")).Value
		operation["responses"] = responses
		applyModuleHTTPRouteMetadata(operation, path, contract.Owner, contract.Name, contract.ContractVersion, route)
		pathItem, _ := paths[path].(map[string]any)
		if pathItem == nil {
			pathItem = map[string]any{}
		}
		pathItem[strings.ToLower(method)] = operation
		paths[path] = pathItem
		delete(routes, pattern)
	}
	if len(routes) != 0 {
		panic("Scheduler source Action has no OpenAPI operation")
	}
}

func addBuilderPath(paths map[string]any, path, tag string, methods ...string) {
	pathSpec, _ := paths[path].(map[string]any)
	if pathSpec == nil {
		pathSpec = map[string]any{}
	}
	for _, method := range methods {
		if _, exists := pathSpec[method]; exists {
			continue
		}
		operationID := openAPIOperationName(method + " " + strings.ReplaceAll(path, "{", " "))
		args := []any{openAPIAdminSecurity()}
		for _, segment := range strings.Split(path, "/") {
			if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
				name := strings.TrimSuffix(strings.TrimPrefix(segment, "{"), "}")
				args = append(args, openAPIPathParameter(name, "Business Runtime resource identifier"))
			}
		}
		if method == "post" || method == "put" || method == "patch" {
			args = append(args, openAPIJSONRequest(openAPIObject(nil)))
		}
		args = append(args, openAPIJSONResponse("Business Runtime response", openAPIObject(nil)))
		pathSpec[method] = openAPIOperation(operationID, tag, "Business Runtime "+tag+" operation", args...)
	}
	paths[path] = pathSpec
}

func addBusinessStreamOpenAPIPaths(paths map[string]any) {
	streamResponse := func(description string) openAPIResponseArg {
		return openAPIResponse(description, "text/event-stream", map[string]any{"type": "string"})
	}
	resume := openAPIParameter{Value: openAPIHeaderParameter("Last-Event-ID", "Opaque cursor from the last processed event", false)}
	for _, prefix := range []string{"/business", "/portal"} {
		paths[prefix+"/notifications/stream"] = map[string]any{"get": openAPIOperation(
			openAPIOperationName("get "+prefix+" notifications stream"), "Notification Inbox", "Resume the authenticated principal Inbox synchronization stream; refetch durable state after each signal",
			openAPIAdminSecurity(), resume, openAPIQueryParameter("cursor", "Signed Inbox synchronization cursor", map[string]any{"type": "string"}), streamResponse("Inbox synchronization SSE stream"),
		)}
	}
	paths["/business/records/stream"] = map[string]any{"get": openAPIOperation(
		"streamBusinessRecords", "Business Records", "Resume the workspace-scoped business record synchronization stream; refetch durable records after each signal",
		openAPIAdminSecurity(), resume, openAPIQueryParameter("cursor", "Signed business record synchronization cursor", map[string]any{"type": "string"}), streamResponse("Business record synchronization SSE stream"),
	)}
}
