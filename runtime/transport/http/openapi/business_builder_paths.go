package openapi

import "strings"

func runtimeAuthoringCoverageRequestSchema() map[string]any {
	return openAPIObject(map[string]any{"coverage": openAPIObject(map[string]any{
		"version": map[string]any{"type": "string", "const": "runtime-authoring-coverage-v1"},
		"requirements": openAPIArray(openAPIObject(map[string]any{
			"requirement_id": map[string]any{"type": "string"}, "capability_keys": openAPIArray(map[string]any{"type": "string"}),
			"resources": openAPIArray(openAPIObject(map[string]any{
				"resource_type": map[string]any{"type": "string"}, "resource_key": map[string]any{"type": "string"},
			})),
			"scenario_ids": openAPIArray(map[string]any{"type": "string"}),
		})),
	})})
}

// addBusinessBuilderOpenAPIPaths publishes domain-system authoring and
// administrator recovery APIs that are not schema-derived object CRUD paths.
func addBusinessBuilderOpenAPIPaths(paths map[string]any) {
	addBuilderPath(paths, "/business/runtime-schema", "Business Runtime Schema", "get")
	addBuilderPath(paths, "/business/surface-context", "Business Runtime Schema", "get")
	addBuilderPath(paths, "/portal/runtime-schema", "Portal Runtime Schema", "get")
	addBuilderPath(paths, "/portal/surface-context", "Portal Runtime Schema", "get")
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
	addBuilderPath(paths, "/tenant-admin/metadata/definitions/{resourceType}", "Metadata Administration", "get")
	addBuilderPath(paths, "/tenant-admin/metadata/definitions/{resourceType}/{resourceKey}", "Metadata Administration", "get")
	addBuilderPath(paths, "/tenant-admin/metadata/definitions/{resourceType}/{resourceKey}/validate", "Metadata Administration", "post")
	addBuilderPath(paths, "/tenant-admin/metadata/migration-plan", "Metadata Administration", "get")
	addBuilderPath(paths, "/tenant-admin/metadata/objects/{objectKey}/record-count", "Metadata Administration", "get")
	addBuilderPath(paths, "/tenant-admin/metadata/capabilities", "Metadata Administration", "get")
	addBuilderPath(paths, "/operations/metadata/diagnostics", "Metadata Operations", "get")
	addBuilderPath(paths, "/business/audit-events", "Audit Business", "get")
	addBusinessAuditOpenAPIPath(paths)
	addBuilderPath(paths, "/business/records/stream", "Business Records", "get")
	addBuilderPath(paths, "/tenant-admin/audit-events", "Audit Administration", "get")
	addBuilderPath(paths, "/tenant-admin/audit-events/export", "Audit Administration", "get")
	addBuilderPath(paths, "/operations/audit-events", "Audit Operations", "get")
	addBuilderPath(paths, "/operations/audit-events/export", "Audit Operations", "get")
	addBuilderPath(paths, "/frontend-capability-manifest/validate", "Capabilities", "post")
	addBuilderPath(paths, "/frontend-capability-manifest", "Capabilities", "get")
	paths["/frontend-capability-manifest"] = map[string]any{
		"get": openAPIOperation("getFrontendCapabilityManifest", "Capabilities", "Read deployed frontend capability evidence and compatibility gaps", openAPIAdminSecurity(), openAPIJSONResponse("Frontend capability snapshot", openAPIRef("FrontendCapabilitySnapshot"))),
	}
	paths["/frontend-capability-manifest/validate"] = map[string]any{
		"post": openAPIOperation("validateFrontendCapabilityManifest", "Capabilities", "Validate deployed frontend routes, permissions, domain bindings and artifact hashes without persisting", openAPIAdminSecurity(), openAPIJSONRequest(openAPIRef("FrontendCapabilityManifest")), openAPIJSONResponse("Frontend capability validation", openAPIRef("FrontendCapabilityValidation"))),
	}
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
	addBuilderPath(paths, "/agent-dialog/runs", "Global Agent", "post")
	addBuilderPath(paths, "/agent-dialog/runs/{runID}", "Global Agent", "get")
	addBuilderPath(paths, "/agent-dialog/task-runs/{taskRunID}", "Global Agent", "get")
	addBuilderPath(paths, "/agent-dialog/sessions", "Global Agent", "get", "post")
	addBuilderPath(paths, "/agent-dialog/sessions/{externalSessionID}/archive", "Global Agent", "post")
	addBuilderPath(paths, "/agent-dialog/sessions/{externalSessionID}/restore", "Global Agent", "post")
	addBuilderPath(paths, "/agent-dialog/proposals", "Global Agent", "get", "post")
	addBuilderPath(paths, "/agent-dialog/proposals/{proposalID}", "Global Agent", "get")
	addBuilderPath(paths, "/agent-dialog/proposals/{proposalID}/approve", "Global Agent", "post")
	addBuilderPath(paths, "/agent-dialog/proposals/{proposalID}/reject", "Global Agent", "post")
	addBusinessStreamOpenAPIPaths(paths)
	annotateNotificationRuntimeClient(paths)
	addBusinessAgentExtendedOpenAPIPaths(paths)
	addBuilderPath(paths, "/operations/agent/tasks", "Agent Task Operations", "get")
	addBuilderPath(paths, "/operations/agent/tasks/{taskRunID}", "Agent Task Operations", "get")
	addBuilderPath(paths, "/operations/agent/tasks/{taskRunID}/retry", "Agent Task Operations", "post")
	addBuilderPath(paths, "/operations/agent/tasks/{taskRunID}/cancel", "Agent Task Operations", "post")
	addBuilderPath(paths, "/operations/agent/tasks/{taskRunID}/resolve", "Agent Task Operations", "post")
	addBuilderPath(paths, "/operations/agent/tasks/{taskRunID}/reconcile", "Agent Task Operations", "post")
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
	addBuilderPath(paths, "/tenant-admin/scheduler/definitions", "Scheduler Administration", "get")
	addBuilderPath(paths, "/tenant-admin/scheduler/definitions/{definitionID}", "Scheduler Administration", "get")
	addBuilderPath(paths, "/tenant-admin/scheduler/authoring-contract", "Scheduler Administration", "get")
	addBuilderPath(paths, "/tenant-admin/scheduler/definitions/validate", "Scheduler Administration", "post")
	addBuilderPath(paths, "/tenant-admin/scheduler/schedules/preview", "Scheduler Administration", "post")
	addBuilderPath(paths, "/tenant-admin/scheduler/definitions/{definitionID}/simulate", "Scheduler Administration", "post")
	addBuilderPath(paths, "/operations/scheduler/state", "Scheduler Operations", "get")
	addBuilderPath(paths, "/operations/scheduler/definitions/{definitionID}/reschedule", "Scheduler Operations", "post")
	addBuilderPath(paths, "/operations/scheduler/definitions/{definitionID}/run", "Scheduler Operations", "post")
	addBuilderPath(paths, "/operations/scheduler/runs/{runID}/retry", "Scheduler Operations", "post")
	addBuilderPath(paths, "/operations/scheduler/runs/{runID}/cancel", "Scheduler Operations", "post")
	addBuilderPath(paths, "/operations/scheduler/dead-letters/{deadLetterID}/resolve", "Scheduler Operations", "post")
	addBuilderPath(paths, "/operations/scheduler/dead-letters/{deadLetterID}/requeue", "Scheduler Operations", "post")
	addBuilderPath(paths, "/automation-rules/authoring-fragments/{capabilityKey}/validate", "Automation", "post")

	addBuilderPath(paths, "/tenant-admin/integrations/catalog", "Integration Administration", "get")
	addBuilderPath(paths, "/tenant-admin/integrations/connectors", "Integration Administration", "get")
	addBuilderPath(paths, "/tenant-admin/integrations/secrets", "Integration Administration", "get")
	addBuilderPath(paths, "/tenant-admin/integrations/secrets/{secretKey}", "Integration Administration", "put")
	addBuilderPath(paths, "/tenant-admin/integrations/secrets/{secretKey}/disable", "Integration Administration", "post")
	addBuilderPath(paths, "/tenant-admin/integrations/secrets/{secretKey}/rotate", "Integration Administration", "post")
	addBuilderPath(paths, "/tenant-admin/integrations/secrets/{secretKey}/expire", "Integration Administration", "post")
	addBuilderPath(paths, "/tenant-admin/integrations/secrets/{secretKey}/revoke", "Integration Administration", "post")
	addBuilderPath(paths, "/tenant-admin/integrations/connections", "Integration Administration", "get")
	addBuilderPath(paths, "/tenant-admin/integrations/connections/{connectionKey}", "Integration Administration", "get", "put", "delete")
	addBuilderPath(paths, "/tenant-admin/integrations/connections/{connectionKey}/versions", "Integration Administration", "get")
	addBuilderPath(paths, "/tenant-admin/integrations/connections/{connectionKey}/validate", "Integration Administration", "post")
	addBuilderPath(paths, "/tenant-admin/integrations/connections/{connectionKey}/disable", "Integration Administration", "post")
	addBuilderPath(paths, "/tenant-admin/integrations/connections/{connectionKey}/rotate", "Integration Administration", "post")
	addBuilderPath(paths, "/tenant-admin/integrations/connections/{connectionKey}/test-operation", "Integration Administration", "post")
	addBuilderPath(paths, "/tenant-admin/integrations/connections/{connectionKey}/oauth/google/start", "Integration Administration", "post")
	addBuilderPath(paths, "/tenant-admin/integrations/bindings/validate", "Integration Administration", "post")
	addBuilderPath(paths, "/tenant-admin/integrations/external-identities", "Integration Administration", "get")
	addBuilderPath(paths, "/tenant-admin/integrations/external-identities/resolve", "Integration Administration", "post")
	addBuilderPath(paths, "/tenant-admin/integrations/external-identities/{identityKey}", "Integration Administration", "put")
	addBuilderPath(paths, "/tenant-admin/integrations/external-identities/{identityKey}/disable", "Integration Administration", "post")
	addBuilderPath(paths, "/tenant-admin/integrations/api-keys", "Integration Administration", "get", "post")
	addBuilderPath(paths, "/tenant-admin/integrations/api-keys/{apiKey}/disable", "Integration Administration", "post")
	addBuilderPath(paths, "/tenant-admin/integrations/api-keys/{apiKey}/rotate", "Integration Administration", "post")
	addBuilderPath(paths, "/tenant-admin/integrations/webhook-subscriptions", "Integration Administration", "get")
	addBuilderPath(paths, "/tenant-admin/integrations/webhook-subscriptions/{subscriptionKey}", "Integration Administration", "put", "delete")
	addBuilderPath(paths, "/tenant-admin/integrations/webhook-subscriptions/{subscriptionKey}/disable", "Integration Administration", "post")
	addBuilderPath(paths, "/tenant-admin/integrations/webhook-subscriptions/publish", "Integration Administration", "post")
	addBuilderPath(paths, "/operations/integrations/activity", "Integration Operations", "get")
	addBuilderPath(paths, "/operations/integrations/events/process-due", "Integration Operations", "post")
	addBuilderPath(paths, "/operations/integrations/events/{eventID}/retry", "Integration Operations", "post")
	addBuilderPath(paths, "/operations/integrations/events/{eventID}/replay", "Integration Operations", "post")
	addBuilderPath(paths, "/operations/integrations/outbox/process-due", "Integration Operations", "post")
	addBuilderPath(paths, "/operations/integrations/outbox/{messageID}/retry", "Integration Operations", "post")
	addBuilderPath(paths, "/business/integration-intents/{messageID}", "Integration Business", "get")

	addBuilderPath(paths, "/tenant-admin/metadata/localized-texts", "Metadata", "get")
	addBuilderPath(paths, "/tenant-admin/metadata/localized-texts/coverage", "Metadata", "get")
	addBuilderPath(paths, "/tenant-admin/metadata/localized-texts/export", "Metadata", "get")
	addBuilderPath(paths, "/tenant-admin/metadata/localized-texts/export.xlsx", "Metadata", "get")
	addBuilderPath(paths, "/surfaces/{surfaceKey}/context", "Surfaces", "post")
	paths["/surfaces/{surfaceKey}/context"] = map[string]any{
		"post": openAPIOperation("surfaceContext", "Surfaces", "Batch records and readable relation projections for a surface", openAPIAdminSecurity(), openAPIPathParameter("surfaceKey", "Surface key"), openAPIJSONRequest(openAPIRef("SurfaceContextRequest")), openAPIJSONResponse("Surface context", openAPIRef("SurfaceContextResult"))),
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

func addBusinessAgentExtendedOpenAPIPaths(paths map[string]any) {
	agentRunRequest := openAPIRequiredObject([]string{"message"}, map[string]any{
		"message": map[string]any{"type": "string"}, "response_mode": map[string]any{"type": "string"}, "timeout_seconds": map[string]any{"type": "integer"},
		"new_session": map[string]any{"type": "boolean"}, "external_session_id": map[string]any{"type": "string"}, "context": openAPIObject(nil), "metadata": openAPIObject(nil), "idempotency_key": map[string]any{"type": "string"},
	})
	paths["/agent-dialog/runs/stream"] = map[string]any{"post": openAPIRuntimeClient(openAPIOperation(
		"streamAgentDialogRun", "Global Agent", "Execute an authorized interactive Agent run and stream accepted, result, or stable error events",
		openAPIAdminSecurity(), openAPIParameter{Value: openAPIHeaderParameter("Idempotency-Key", "Stable caller key; body idempotency_key is the compatibility fallback", true)}, openAPIJSONRequest(agentRunRequest),
		openAPIResponse("Interactive Agent SSE stream", "text/event-stream", map[string]any{"type": "string"}),
	), "runAgentStream")}
	paths["/agent-dialog/analysis/query"] = map[string]any{"post": openAPIRuntimeClient(openAPIOperation(
		"queryAgentAnalysis", "Global Agent", "Validate or execute a principal-scoped Agent analysis query and record report governance evidence",
		openAPIAdminSecurity(), openAPIJSONRequest(openAPIRequiredObject([]string{"intent"}, map[string]any{
			"intent": map[string]any{"type": "string"}, "sql": map[string]any{"type": "string"}, "metric_spec": openAPIObject(nil), "dry_run": map[string]any{"type": "boolean"}, "max_rows": map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
		})), openAPIJSONResponse("Governed Agent analysis result", agentAnalysisResultSchema()),
	), "queryAgentAnalysis")}
	paths["/agent-dialog/task-tools/invoke"] = map[string]any{"post": openAPIOperation(
		"invokeAgentTaskTool", "Global Agent", "Invoke a task-scoped tool with leased-run, credential, authorization, and idempotency evidence",
		openAPIAdminSecurity(), openAPIJSONRequest(openAPIRequiredObject([]string{"credential", "workspace_id", "task_run_id", "tool", "input", "idempotency_key"}, map[string]any{
			"credential": map[string]any{"type": "string", "writeOnly": true}, "workspace_id": map[string]any{"type": "string"}, "task_run_id": map[string]any{"type": "string"}, "tool": map[string]any{"type": "string"}, "input": openAPIObject(nil), "idempotency_key": map[string]any{"type": "string"},
		})), openAPIJSONResponse("Agent task tool result", openAPIRequiredObject([]string{"status", "tool", "call_ref", "authorization"}, map[string]any{
			"status": map[string]any{"type": "string"}, "tool": map[string]any{"type": "string"}, "call_ref": map[string]any{"type": "string"}, "output": openAPIObject(nil), "proposal": openAPIObject(nil), "authorization": openAPIObject(nil),
		})),
	)}
	for path, operation := range map[string]struct{ id, summary, response string }{
		"/agent-dialog/report-query-runs/{queryRef}":    {"getAgentReportQueryRun", "Get principal-scoped Agent report query evidence", "query"},
		"/agent-dialog/report-export-audits/{queryRef}": {"getAgentReportExportAudit", "Get principal-scoped Agent report export audit", "audit"},
		"/agent-dialog/download-tasks/{queryRef}":       {"getAgentReportDownloadTask", "Get principal-scoped Agent report download task", "download"},
	} {
		method := map[string]string{"query": "getAgentReportQueryRun", "audit": "getAgentReportExportAudit", "download": "getAgentReportDownloadTask"}[operation.response]
		paths[path] = map[string]any{"get": openAPIRuntimeClient(openAPIOperation(operation.id, "Global Agent", operation.summary, openAPIAdminSecurity(), openAPIPathParameter("queryRef", "Agent analysis query reference"), openAPIJSONResponse("Agent report governance record", agentReportStateSchema(operation.response))), method)}
	}
	paths["/agent-dialog/download-tasks/{queryRef}/prepare"] = map[string]any{"post": openAPIRuntimeClient(openAPIOperation(
		"prepareAgentReportDownloadHandoff", "Global Agent", "Prepare the governed handoff from Agent analysis to report-center export and authorized download",
		openAPIAdminSecurity(), openAPIPathParameter("queryRef", "Agent analysis query reference"), openAPIJSONRequest(openAPIObject(nil)),
		openAPIJSONResponse("Prepared report-center handoff", openAPIRequiredObject([]string{"query_ref", "status", "handoff", "report_query_run", "report_export_audit", "download_task", "scope", "next_step"}, map[string]any{
			"query_ref": map[string]any{"type": "string"}, "status": map[string]any{"type": "string"}, "handoff": map[string]any{"type": "string"}, "report_query_run": agentReportStateSchema("query"), "report_export_audit": agentReportStateSchema("audit"), "download_task": agentReportStateSchema("download"), "scope": map[string]any{"type": "string"}, "next_step": map[string]any{"type": "string"},
		})),
	), "prepareAgentReportHandoff")}
}

func agentAnalysisResultSchema() map[string]any {
	return openAPIRequiredObject([]string{"query_ref", "status", "execution_mode", "scope_note", "workspace_id", "role", "truncated", "audit_event_key"}, map[string]any{
		"query_ref": map[string]any{"type": "string"}, "status": map[string]any{"type": "string"}, "execution_mode": map[string]any{"type": "string"},
		"html_fragment": map[string]any{"type": "string"}, "rendered_report": openAPIObject(nil), "report_provenance": openAPIObject(nil), "report_governance": openAPIObject(nil),
		"scope_note": map[string]any{"type": "string"}, "report_center_ref": map[string]any{"type": "string"}, "proposal_suggestion": openAPIObject(nil),
		"workspace_id": map[string]any{"type": "string"}, "role": map[string]any{"type": "string"}, "object_key": map[string]any{"type": "string"}, "report_key": map[string]any{"type": "string"},
		"rows": openAPIArray(openAPIObject(nil)), "row_count": map[string]any{"type": "integer"}, "total": map[string]any{"type": "integer"}, "truncated": map[string]any{"type": "boolean"},
		"masked_fields": openAPIArray(map[string]any{"type": "string"}), "audit_event_key": map[string]any{"type": "string"},
	})
}

func agentReportStateSchema(kind string) map[string]any {
	properties := map[string]any{
		"query_ref": map[string]any{"type": "string"}, "report_key": map[string]any{"type": "string"}, "workspace_id": map[string]any{"type": "string"}, "user_id": map[string]any{"type": "string"}, "role": map[string]any{"type": "string"},
		"status": map[string]any{"type": "string"}, "created_at": map[string]any{"type": "integer", "format": "int64"}, "prepared_at": map[string]any{"type": "integer", "format": "int64"}, "prepared_by": map[string]any{"type": "string"}, "next_step": map[string]any{"type": "string"},
	}
	required := []string{"query_ref", "status", "created_at"}
	switch kind {
	case "query":
		properties["execution_mode"], properties["row_count"], properties["total"], properties["truncated"], properties["audit_event_key"], properties["scope"], properties["metadata"] = map[string]any{"type": "string"}, map[string]any{"type": "integer"}, map[string]any{"type": "integer"}, map[string]any{"type": "boolean"}, map[string]any{"type": "string"}, map[string]any{"type": "string"}, openAPIObject(nil)
		required = append(required, "execution_mode", "row_count", "total", "truncated", "audit_event_key", "scope")
	case "audit":
		properties["audit_event_key"], properties["handoff"] = map[string]any{"type": "string"}, map[string]any{"type": "string"}
		required = append(required, "audit_event_key", "handoff")
	case "download":
		properties["task_key"], properties["handoff"] = map[string]any{"type": "string"}, map[string]any{"type": "string"}
		required = append(required, "task_key", "handoff")
	}
	return openAPIRequiredObject(required, properties)
}
