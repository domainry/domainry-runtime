package openapi

import (
	"testing"

	"github.com/domainry/domainry-foundation/modulehttp"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

func TestOpenAPIPublishesVersionedAgentContracts(t *testing.T) {
	document := Build(appschemamodel.ApplicationSchemaSnapshot{})
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	for _, name := range []string{"AgentTaskDefinition", "AgentEntrypointAssignment", "GlobalAgentContextContract", "AgentRoutingContract", "WorkflowAgentTaskNodeContract", "InteractiveAgentHandoff"} {
		if schemas[name] == nil {
			t.Fatalf("missing OpenAPI Agent schema %q", name)
		}
	}
	task := schemas["AgentTaskDefinition"].(map[string]any)["properties"].(map[string]any)
	if task["contract_version"].(map[string]any)["enum"].([]string)[0] != "agent-task-v1" {
		t.Fatalf("Agent Task contract version schema = %#v", task["contract_version"])
	}
	entrypoint := schemas["AgentEntrypointAssignment"].(map[string]any)["properties"].(map[string]any)
	if entrypoint["context_contract"].(map[string]any)["$ref"] != "#/components/schemas/GlobalAgentContextContract" {
		t.Fatalf("Agent entrypoint context ref = %#v", entrypoint["context_contract"])
	}
	handoff := schemas["InteractiveAgentHandoff"].(map[string]any)
	if handoff["additionalProperties"] != false {
		t.Fatalf("Interactive handoff must reject dynamic privilege fields: %#v", handoff)
	}
	public := []string{
		"POST /agent-dialog/runs", "POST /agent-dialog/runs/stream", "GET /agent-dialog/runs/{runID}",
		"GET /agent-dialog/sessions", "POST /agent-dialog/sessions", "POST /agent-dialog/sessions/{externalSessionID}/archive", "POST /agent-dialog/sessions/{externalSessionID}/restore",
		"GET /agent-dialog/proposals", "GET /agent-dialog/proposals/{proposalID}", "POST /agent-dialog/proposals", "POST /agent-dialog/proposals/{proposalID}/approve", "POST /agent-dialog/proposals/{proposalID}/reject",
		"GET /agent-dialog/task-runs/{taskRunID}", "POST /agent-dialog/analysis/query",
	}
	operations := []string{
		"GET /agent-dialog/diagnostics", "GET /operations/agent/tasks", "GET /operations/agent/tasks/{taskRunID}",
		"POST /operations/agent/tasks/{taskRunID}/retry", "POST /operations/agent/tasks/{taskRunID}/cancel", "POST /operations/agent/tasks/{taskRunID}/resolve", "POST /operations/agent/tasks/{taskRunID}/reconcile",
	}
	routes := make([]modulehttp.Route, 0, len(public)+len(operations)+1)
	for _, pattern := range public {
		route := modulehttp.Route{Pattern: pattern, Exposures: []modulehttp.Exposure{modulehttp.ExposurePublic}, Authentication: modulehttp.AuthenticationAuthenticated, PrincipalOnly: true}
		if pattern == "POST /agent-dialog/runs" || pattern == "POST /agent-dialog/runs/stream" {
			route.Governance = &modulehttp.Governance{EffectClass: modulehttp.EffectWrite, HighRiskPolicy: modulehttp.HighRiskNone, IdempotencyDecision: "caller_key_required", AuditClass: "mutation_audit_required"}
		}
		routes = append(routes, route)
	}
	routes = append(routes, modulehttp.Route{Pattern: "POST /agent-dialog/task-tools/invoke", Exposures: []modulehttp.Exposure{modulehttp.ExposurePublic}, Authentication: modulehttp.AuthenticationAnonymous})
	for _, pattern := range operations {
		routes = append(routes, modulehttp.Route{Pattern: pattern, Exposures: []modulehttp.Exposure{modulehttp.ExposureTenantAdmin, modulehttp.ExposureOps}, Authentication: modulehttp.AuthenticationAuthenticated, AnyPermissions: []string{"agent.task.read", "workspace.admin"}})
	}
	streamPattern := "POST /agent-dialog/runs/stream"
	agentSurface := openAPIModuleSurface{owner: "agent", routes: routes, operations: map[string]map[string]any{streamPattern: {
		"operationId": "runAgentStream", "requestBody": map[string]any{"required": true, "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object"}}}},
		"responses": map[string]any{"200": map[string]any{"description": "Agent event stream", "content": map[string]any{"text/event-stream": map[string]any{"schema": map[string]any{"type": "string"}}}}},
	}}}
	withAgent := BuildWithModuleHTTPSurfaces(appschemamodel.ApplicationSchemaSnapshot{}, "Domainry", []modulehttp.Surface{agentSurface})
	paths := withAgent["paths"].(map[string]any)
	agentPaths := []string{"/agent-dialog/runs", "/agent-dialog/runs/stream", "/agent-dialog/runs/{runID}", "/agent-dialog/analysis/query", "/agent-dialog/diagnostics", "/agent-dialog/task-tools/invoke", "/agent-dialog/task-runs/{taskRunID}", "/agent-dialog/sessions", "/agent-dialog/sessions/{externalSessionID}/archive", "/agent-dialog/sessions/{externalSessionID}/restore", "/agent-dialog/proposals", "/agent-dialog/proposals/{proposalID}", "/agent-dialog/proposals/{proposalID}/approve", "/agent-dialog/proposals/{proposalID}/reject", "/operations/agent/tasks", "/operations/agent/tasks/{taskRunID}", "/operations/agent/tasks/{taskRunID}/retry", "/operations/agent/tasks/{taskRunID}/cancel", "/operations/agent/tasks/{taskRunID}/resolve", "/operations/agent/tasks/{taskRunID}/reconcile"}
	for _, path := range agentPaths {
		if paths[path] == nil {
			t.Fatalf("missing Agent path %q", path)
		}
	}
	basePaths := document["paths"].(map[string]any)
	for _, path := range agentPaths {
		if _, exists := basePaths[path]; exists {
			t.Fatalf("Runtime-only OpenAPI retained Agent-owned Surface path %q", path)
		}
	}
	stream := paths["/agent-dialog/runs/stream"].(map[string]any)["post"].(map[string]any)
	responses := stream["responses"].(map[string]any)
	content := responses["200"].(map[string]any)["content"].(map[string]any)
	if content["text/event-stream"] == nil || stream["requestBody"] == nil {
		t.Fatalf("Agent stream contract=%#v", stream)
	}
}
