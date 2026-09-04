package openapi

import (
	"strings"

	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
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
	return openAPIRequiredObject([]string{"scenarios"}, map[string]any{
		"scenarios": openAPIArray(scenario),
	})
}

func runtimeAuthoringCoverageLedgerSchema() map[string]any {
	return openAPIObject(map[string]any{
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
	return openAPIRequiredObject([]string{"coverage", "receipts"}, map[string]any{
		"coverage": runtimeAuthoringCoverageLedgerSchema(),
		"receipts": openAPIArray(map[string]any{"type": "string", "description": "Opaque Runtime-issued HMAC receipts. Runtime reconstructs binding, scenarios, and steps from these receipts."}),
	})
}

func runtimeAuthoringEvidenceOpenAPIExtension() map[string]any {
	return map[string]any{
		"version": changeplanmodel.RuntimeAuthoringEvidenceCollectionVersion, "trust_policy": changeplanmodel.RuntimeAuthoringEvidenceTrustPolicy,
		"step_receipt_version": changeplanmodel.RuntimeAuthoringStepReceiptVersion,
		"request_headers": map[string]any{
			"evidence_step_token": changeplanmodel.RuntimeAuthoringEvidenceStepTokenHeader,
			"builder_task":        changeplanmodel.RuntimeAuthoringBuilderTaskHeader,
		},
		"response_headers":           map[string]any{"step_receipt": changeplanmodel.RuntimeAuthoringStepReceiptHeader, "evidence_error": changeplanmodel.RuntimeAuthoringEvidenceErrorHeader},
		"streaming_path_restriction": changeplanmodel.RuntimeAuthoringEvidenceStreamingPathRestriction,
	}
}

// addBusinessBuilderOpenAPIPaths publishes domain-system authoring and
// administrator recovery APIs that are not schema-derived object CRUD paths.
func addBusinessBuilderOpenAPIPaths(paths map[string]any) {
	addBuilderPath(paths, "/discovery/schema", "Runtime Schema", "get")
	addBuilderPath(paths, "/notification/inbox", "Notification Inbox", "get")
	addBuilderPath(paths, "/notification/inbox/facets", "Notification Inbox", "get")
	addBuilderPath(paths, "/notification/inbox/unread-count", "Notification Inbox", "get")
	addBuilderPath(paths, "/notification/inbox/stream", "Notification Inbox", "get")
	addBuilderPath(paths, "/notification/inbox/read-all", "Notification Inbox", "post")
	addBuilderPath(paths, "/notification/inbox/saved-views", "Notification Inbox", "get")
	addBuilderPath(paths, "/notification/inbox/saved-views/{viewKey}", "Notification Inbox", "put", "delete")
	addBuilderPath(paths, "/notification/inbox/delegations", "Notification Inbox", "get")
	addBuilderPath(paths, "/notification/inbox/delegations/{delegationID}", "Notification Inbox", "put", "delete")
	addBuilderPath(paths, "/notification/inbox/delegated-owners", "Notification Inbox", "get")
	addBuilderPath(paths, "/notification/inbox/{notificationID}", "Notification Inbox", "get")
	addBuilderPath(paths, "/notification/inbox/{notificationID}/actions/{actionKey}/resolve", "Notification Inbox", "get")
	addBuilderPath(paths, "/notification/inbox/{notificationID}/acknowledge", "Notification Inbox", "post")
	for _, mutation := range []string{"read", "unread", "archive", "restore"} {
		addBuilderPath(paths, "/notification/inbox/{notificationID}/"+mutation, "Notification Inbox", "post")
	}
	addBuilderPath(paths, "/notification/inbox/preference", "Notification Inbox", "get", "put")
	addBuilderPath(paths, "/notification/templates", "Notification Governance", "get")
	addBuilderPath(paths, "/notification/publications", "Notification Governance", "get")
	addBuilderPath(paths, "/notification/publications/{publicationID}/approve", "Notification Governance", "post")
	addBuilderPath(paths, "/notification/publications/{publicationID}/reject", "Notification Governance", "post")
	addBuilderPath(paths, "/notification/publications/{publicationID}/cancel", "Notification Governance", "post")
	addBuilderPath(paths, "/notification/capabilities", "Notification Governance", "get")
	addBuilderPath(paths, "/notification/policy", "Notification Governance", "get", "put")
	addBuilderPath(paths, "/notification/preferences", "Notification Governance", "get")
	addBuilderPath(paths, "/notification/preferences/{recipientKey}", "Notification Governance", "put")
	addBuilderPath(paths, "/notification/metrics", "Notification Governance", "get")
	addNotificationDeliveryOpenAPIPath(paths)
	addBuilderPath(paths, "/notification/governance/catalog", "Notification Governance", "get")
	addBuilderPath(paths, "/notification/governance/inbox-metrics", "Notification Governance", "get")
	addBuilderPath(paths, "/notification/templates/preview", "Notification Governance", "post")
	addBuilderPath(paths, "/notification/templates/{templateKey}", "Notification Governance", "get")
	addBuilderPath(paths, "/notification/templates/{templateKey}/draft", "Notification Governance", "put")
	addBuilderPath(paths, "/notification/templates/{templateKey}/publish", "Notification Governance", "post")
	addBuilderPath(paths, "/notification/templates/{templateKey}/publication-requests", "Notification Governance", "post")
	addBuilderPath(paths, "/notification/templates/{templateKey}/disable", "Notification Governance", "post")
	addBuilderPath(paths, "/notification/templates/{templateKey}/preview", "Notification Governance", "post")
	addBuilderPath(paths, "/notification/templates/{templateKey}/versions", "Notification Governance", "get")
	addBuilderPath(paths, "/notification/templates/{templateKey}/versions/{version}/restore-draft", "Notification Governance", "post")
	addBuilderPath(paths, "/discovery/schema/administration", "Metadata Administration", "get")
	addBuilderPath(paths, "/application-schema/definitions/{resourceType}/{resourceKey}/validate", "Application Schema", "post")
	addBuilderPath(paths, "/application-schema/migration-plan", "Application Schema", "get")
	addBuilderPath(paths, "/application-schema/objects/{objectKey}/record-count", "Application Schema", "get")
	addBuilderPath(paths, "/application-schema/diagnostics", "Application Schema Operations", "get")
	addBuilderPath(paths, "/records/stream", "Business Records", "get")
	addBuilderPath(paths, "/workflow/processes", "Workflow Business", "get")
	addBuilderPath(paths, "/workflow/processes/{processID}", "Workflow Business", "get")
	addBuilderPath(paths, "/workflow/team-tasks", "Workflow Business", "get")
	addBuilderPath(paths, "/workflow/tasks", "Workflow Business", "get")
	addBuilderPath(paths, "/workflow/processes/{processID}/withdraw", "Workflow Business", "post")
	addBuilderPath(paths, "/workflow/processes/{processID}/retry", "Workflow Business", "post")
	addBuilderPath(paths, "/workflow/tasks/{taskID}/approve", "Workflow Business", "post")
	addBuilderPath(paths, "/workflow/tasks/{taskID}/reject", "Workflow Business", "post")
	addBuilderPath(paths, "/workflow/tasks/{taskID}/return", "Workflow Business", "post")
	addBuilderPath(paths, "/workflow/definitions/{workflowKey}/run", "Workflow Business", "post")
	addBusinessStreamOpenAPIPaths(paths)
	annotateNotificationRuntimeClient(paths)
	addBuilderPath(paths, "/workflow/recovery/executions", "Workflow Operations", "get")
	addBuilderPath(paths, "/workflow/recovery/executions/process", "Workflow Operations", "post")
	addBuilderPath(paths, "/workflow/recovery/executions/{executionID}/retry", "Workflow Operations", "post")
	addBuilderPath(paths, "/workflow/recovery/executions/{executionID}/resolve", "Workflow Operations", "post")
	addBuilderPath(paths, "/workflow/recovery/processes", "Workflow Operations", "get")
	addBuilderPath(paths, "/workflow/recovery/processes/{processID}", "Workflow Operations", "get")
	addBuilderPath(paths, "/workflow/recovery/processes/{processID}/retry", "Workflow Operations", "post")
	addBuilderPath(paths, "/workflow/recovery/processes/{processID}/resolve", "Workflow Operations", "post")
	addBuilderPath(paths, "/workflow/authoring-fragments/{capabilityKey}/validate", "Workflow Administration", "post")
	addBuilderPath(paths, "/workflow/definitions/{workflowKey}/validate", "Workflow Administration", "post")
	addBuilderPath(paths, "/workflow/definitions/{workflowKey}/simulate", "Workflow Administration", "post")
	addBuilderPath(paths, "/automation/fragments/{capabilityKey}/validate", "Automation", "post")

}

func addNotificationDeliveryOpenAPIPath(paths map[string]any) {
	delivery := openAPIRequiredObject(
		[]string{"id", "connector_key", "operation", "status", "attempt_count"},
		map[string]any{
			"id":              map[string]any{"type": "string"},
			"connector_key":   map[string]any{"type": "string"},
			"connection_key":  map[string]any{"type": "string"},
			"operation":       map[string]any{"type": "string"},
			"status":          map[string]any{"type": "string"},
			"payload":         openAPIObject(nil),
			"response_ref":    map[string]any{"type": "string"},
			"error":           map[string]any{"type": "string"},
			"attempt_count":   map[string]any{"type": "integer", "minimum": 0},
			"next_attempt_at": map[string]any{"type": "string"},
			"created_at":      map[string]any{"type": "string"},
			"updated_at":      map[string]any{"type": "string"},
		},
	)
	response := openAPIRequiredObject(
		[]string{"deliveries", "count"},
		map[string]any{
			"deliveries": openAPIArray(delivery),
			"count":      map[string]any{"type": "integer", "minimum": 0},
		},
	)
	paths["/notification/deliveries"] = map[string]any{
		"get": openAPIOperation(
			"GetNotificationDeliveries",
			"Notification Governance",
			"List bounded notification delivery ledger entries",
			openAPIAdminSecurity(),
			openAPIQueryParameter("status", "Optional delivery status filter", map[string]any{"type": "string"}),
			openAPIQueryParameter("limit", "Bounded delivery result size", map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "default": 100}),
			openAPIJSONResponse("Notification delivery ledger", response),
		),
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
	paths["/notification/inbox/stream"] = map[string]any{"get": openAPIOperation(
		"streamNotificationInbox", "Notification Inbox", "Resume the authenticated principal Inbox synchronization stream; refetch durable state after each signal",
		openAPIAdminSecurity(), resume, openAPIQueryParameter("cursor", "Signed Inbox synchronization cursor", map[string]any{"type": "string"}), streamResponse("Inbox synchronization SSE stream"),
	)}
	paths["/records/stream"] = map[string]any{"get": openAPIOperation(
		"streamBusinessRecords", "Business Records", "Resume the workspace-scoped business record synchronization stream; refetch durable records after each signal",
		openAPIAdminSecurity(), resume, openAPIQueryParameter("cursor", "Signed business record synchronization cursor", map[string]any{"type": "string"}), streamResponse("Business record synchronization SSE stream"),
	)}
}
