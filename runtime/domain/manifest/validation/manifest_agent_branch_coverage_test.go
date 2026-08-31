package validation

import (
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestManifestAgentTopLevelValidationBranches(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*manifestmodel.ManifestSchema)
	}{
		{"invalid skill key", func(m *manifestmodel.ManifestSchema) { m.Skills[0].Key = "" }},
		{"missing skill name", func(m *manifestmodel.ManifestSchema) { m.Skills[0].Name = "" }},
		{"invalid agent key", func(m *manifestmodel.ManifestSchema) { m.Agents[0].Key = "" }},
		{"duplicate agent", func(m *manifestmodel.ManifestSchema) { m.Agents = append(m.Agents, m.Agents[0]) }},
		{"missing agent name", func(m *manifestmodel.ManifestSchema) { m.Agents[0].Name = "" }},
		{"service contract", func(m *manifestmodel.ManifestSchema) { m.AgentServicePrincipals[0].ContractVersion = "" }},
		{"invalid service key", func(m *manifestmodel.ManifestSchema) { m.AgentServicePrincipals[0].Key = "" }},
		{"duplicate service", func(m *manifestmodel.ManifestSchema) {
			m.AgentServicePrincipals = append(m.AgentServicePrincipals, m.AgentServicePrincipals[0])
		}},
		{"task contract", func(m *manifestmodel.ManifestSchema) { m.AgentTasks[0].ContractVersion = "" }},
		{"invalid task key", func(m *manifestmodel.ManifestSchema) { m.AgentTasks[0].Key = "" }},
		{"duplicate task", func(m *manifestmodel.ManifestSchema) { m.AgentTasks = append(m.AgentTasks, m.AgentTasks[0]) }},
		{"missing task version", func(m *manifestmodel.ManifestSchema) { m.AgentTasks[0].Version = "" }},
		{"missing instruction", func(m *manifestmodel.ManifestSchema) { m.AgentTasks[0].Instruction = "" }},
		{"unknown task object", func(m *manifestmodel.ManifestSchema) { m.AgentTasks[0].AllowedObjects = []string{"missing"} }},
		{"entrypoint contract", func(m *manifestmodel.ManifestSchema) { m.AgentEntrypoints[0].ContractVersion = "" }},
		{"invalid entrypoint key", func(m *manifestmodel.ManifestSchema) { m.AgentEntrypoints[0].Key = "" }},
		{"duplicate entrypoint", func(m *manifestmodel.ManifestSchema) {
			m.AgentEntrypoints = append(m.AgentEntrypoints, m.AgentEntrypoints[0])
		}},
		{"unversioned entrypoint agent", func(m *manifestmodel.ManifestSchema) { m.Agents[0].Version = ""; m.AgentTasks = nil }},
		{"empty entrypoint permissions", func(m *manifestmodel.ManifestSchema) { m.AgentEntrypoints[0].RequiredPermissions = nil }},
		{"empty entrypoint routes", func(m *manifestmodel.ManifestSchema) { m.AgentEntrypoints[0].RoutePatterns = nil }},
		{"nil workflow graph", func(m *manifestmodel.ManifestSchema) { m.Workflows[0].Graph = nil }},
		{"missing agent node contract", func(m *manifestmodel.ManifestSchema) { m.Workflows[0].Graph.Nodes[1].Contract = nil }},
		{"missing nested agent node contract", func(m *manifestmodel.ManifestSchema) {
			m.Workflows[0].Graph.Nodes[1].Contract = &definitionmodel.WorkflowNodeContract{}
		}},
		{"unknown action task agent", func(m *manifestmodel.ManifestSchema) { m.AgentTasks[0].AgentKey = "missing" }},
		{"unknown action-capable task agent", func(m *manifestmodel.ManifestSchema) {
			m.AgentTasks[0].AgentKey = "missing"
			m.AgentTasks[0].SideEffectMode = agentsdk.AgentTaskSideEffectActionAllowed
			m.AgentTasks[0].AllowedActions = []string{"customer.update"}
		}},
		{"route non-match and task owner mismatch", func(m *manifestmodel.ManifestSchema) {
			m.Agents = append(m.Agents, agentsdk.AgentSchema{Key: "other-agent", Version: "1"})
			m.AgentEntrypoints[0].AgentKey = "other-agent"
		}},
		{"disabled assignment targets", func(m *manifestmodel.ManifestSchema) {
			m.AgentEntrypoints[0].Enabled = false
			m.AgentTasks[0].Enabled = false
			m.Workflows[0].Enabled = false
		}},
		{"edge label fallback", func(m *manifestmodel.ManifestSchema) {
			m.Workflows[0].Graph.Edges[1].Branch = ""
			m.Workflows[0].Graph.Edges[1].Label = "missing"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := validAgentContractManifest()
			test.mutate(&manifest)
			state := newValidationState(manifest, nil)
			state.validateAgents()
		})
	}
}

func TestManifestAgentHelperValidationBranches(t *testing.T) {
	manifest := validAgentContractManifest()
	state := newValidationState(manifest, nil)
	deep := map[string]any{}
	cursor := deep
	for index := 0; index < 34; index++ {
		next := map[string]any{}
		cursor["properties"] = []any{next}
		cursor = next
	}
	validateAgentJSONSchemaValue(state, "schema", deep, deep, 0)
	validateAgentJSONSchemaValue(state, "schema", map[string]any{"$ref": 3}, map[string]any{}, 0)
	validateAgentJSONSchemaValue(state, "schema", map[string]any{"$ref": ""}, map[string]any{}, 0)
	validateAgentTaskOutcomes(state, "outcomes", nil)
	validateGlobalAgentContextContract(state, "context", agentsdk.GlobalAgentContextContract{AllowedHintFields: []string{"locale", "locale"}, MaxSelectedRecord: 1, MaxContextBytes: 1024})
	validateGlobalAgentContextContract(state, "context", agentsdk.GlobalAgentContextContract{MaxSelectedRecord: 0, MaxContextBytes: 1048577})
	validateAgentJSONSchema(state, "schema", map[string]any{"type": 3})
	validateAgentRoutingContract(state, "routing", agentsdk.AgentRoutingContract{AllowedRouteTypes: []string{agentsdk.AgentRouteTask, agentsdk.AgentRouteTask}})
	validateAgentRoutingContract(state, "routing", agentsdk.AgentRoutingContract{ContractVersion: agentsdk.AgentRoutingContractVersion})
	validateAgentStringSet(state, "strings", []string{"", "value", "value"})
	if !agentAllowsCapability(agentsdk.AgentSchema{Tools: []string{" invoke_action "}}, nil, "invoke_action") {
		t.Fatal("direct agent tool ignored")
	}
	if agentAllowsCapability(agentsdk.AgentSchema{}, nil, "invoke_action") {
		t.Fatal("missing capability accepted")
	}
	if agentAllowsCapability(agentsdk.AgentSchema{Tools: []string{"query_records"}}, nil, "invoke_action") {
		t.Fatal("unrelated capability accepted")
	}
	patterns := []string{"", "bad route", "**", "*", "missing*"}
	for _, pattern := range patterns {
		if validAgentRoutePattern(pattern, map[string]bool{"workspace.home": true}) {
			t.Fatalf("invalid pattern %q accepted", pattern)
		}
	}
	if !validAgentRoutePattern("workspace.*", map[string]bool{"workspace.home": true}) || !agentRoutePatternMatches("workspace.*", "workspace.home") || agentRoutePatternMatches("other", "workspace.home") {
		t.Fatal("route pattern matching failed")
	}
	if !validAgentRoutePattern("workspace.home", map[string]bool{"workspace.home": true}) {
		t.Fatal("exact route pattern rejected")
	}
	if len(state.errs) == 0 {
		t.Fatal("helper validation produced no errors")
	}
}
