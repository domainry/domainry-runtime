package openapi

import (
	"testing"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
)

func TestOpenAPIPublishesVersionedAgentContracts(t *testing.T) {
	document := Build(metadatamodel.MetadataSchemaSnapshot{})
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
	paths := document["paths"].(map[string]any)
	for _, path := range []string{"/agent-dialog/runs", "/agent-dialog/runs/stream", "/agent-dialog/analysis/query", "/agent-dialog/task-tools/invoke", "/agent-dialog/task-runs/{taskRunID}", "/agent-dialog/sessions", "/agent-dialog/proposals", "/agent-dialog/proposals/{proposalID}/approve", "/agent-dialog/proposals/{proposalID}/reject", "/agent-dialog/report-query-runs/{queryRef}", "/agent-dialog/report-export-audits/{queryRef}", "/agent-dialog/download-tasks/{queryRef}", "/agent-dialog/download-tasks/{queryRef}/prepare", "/operations/agent/tasks", "/operations/agent/tasks/{taskRunID}/retry", "/operations/agent/tasks/{taskRunID}/cancel", "/operations/agent/tasks/{taskRunID}/resolve", "/operations/agent/tasks/{taskRunID}/reconcile"} {
		if paths[path] == nil {
			t.Fatalf("missing Agent path %q", path)
		}
	}
	stream := paths["/agent-dialog/runs/stream"].(map[string]any)["post"].(map[string]any)
	responses := stream["responses"].(map[string]any)
	content := responses["200"].(map[string]any)["content"].(map[string]any)
	if content["text/event-stream"] == nil || stream["requestBody"] == nil {
		t.Fatalf("Agent stream contract=%#v", stream)
	}
}
