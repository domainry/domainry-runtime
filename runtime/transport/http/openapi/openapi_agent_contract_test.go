package openapi

import (
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
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
	if entrypoint["adapter"] != nil || entrypoint["default_for_surface"] != nil {
		t.Fatalf("Agent entrypoint leaked product-shell fields: %#v", entrypoint)
	}
	handoff := schemas["InteractiveAgentHandoff"].(map[string]any)
	if handoff["additionalProperties"] != false {
		t.Fatalf("Interactive handoff must reject dynamic privilege fields: %#v", handoff)
	}
	contract := agentsdk.AgentHTTPAdapterContract()
	routes := make([]modulehttp.Route, 0, len(contract.Routes))
	for _, route := range contract.Routes {
		routes = append(routes, modulehttp.Route{Action: route.Action})
	}
	agentAdapter := openAPIModuleAdapter{owner: "agent", routes: routes, operations: contract.OpenAPI}
	withAgent := BuildWithModuleHTTPAdapters(appschemamodel.ApplicationSchemaSnapshot{}, "Domainry", []modulehttp.Adapter{agentAdapter})
	paths := withAgent["paths"].(map[string]any)
	agentPaths := []string{"/agent/runs", "/agent/runs/stream", "/agent/runs/{runID}", "/agent/analysis/query", "/agent/diagnostics", "/agent/task-tools/invoke", "/agent/task-runs/{taskRunID}", "/agent/sessions", "/agent/sessions/{externalSessionID}/archive", "/agent/sessions/{externalSessionID}/restore", "/agent/proposals", "/agent/proposals/{proposalID}", "/agent/proposals/{proposalID}/approve", "/agent/proposals/{proposalID}/reject", "/agent/tasks", "/agent/tasks/{taskRunID}", "/agent/tasks/{taskRunID}/retry", "/agent/tasks/{taskRunID}/cancel", "/agent/tasks/{taskRunID}/resolve", "/agent/tasks/{taskRunID}/reconcile"}
	for _, path := range agentPaths {
		if paths[path] == nil {
			t.Fatalf("missing Agent path %q", path)
		}
	}
	basePaths := document["paths"].(map[string]any)
	for _, path := range agentPaths {
		if _, exists := basePaths[path]; exists {
			t.Fatalf("Runtime-only OpenAPI retained Agent-owned Adapter path %q", path)
		}
	}
	stream := paths["/agent/runs/stream"].(map[string]any)["post"].(map[string]any)
	responses := stream["responses"].(map[string]any)
	content := responses["200"].(map[string]any)["content"].(map[string]any)
	if content["text/event-stream"] == nil || stream["requestBody"] == nil {
		t.Fatalf("Agent stream contract=%#v", stream)
	}
}
