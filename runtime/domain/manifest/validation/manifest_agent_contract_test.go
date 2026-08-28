package validation

import (
	"strings"
	"testing"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestManifestAgentContractsAcceptCompletePublishedGraph(t *testing.T) {
	state := newValidationState(validAgentContractManifest(), nil)
	state.validateAgents()
	if len(state.errs) != 0 {
		t.Fatalf("valid Agent contract graph rejected: %v", state.errs)
	}
	for _, budget := range []string{"low", "standard", "high"} {
		budgetState := &validationState{}
		validateAgentExecutionLimits(budgetState, "agent.execution_limits", agentmodel.AgentExecutionLimits{CostBudget: budget})
		if len(budgetState.errs) != 0 {
			t.Fatalf("valid cost budget %q rejected: %v", budget, budgetState.errs)
		}
	}
}

func TestManifestAgentContractsFailClosed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*manifestmodel.ManifestSchema)
		want   string
	}{
		{name: "duplicate skill", mutate: func(m *manifestmodel.ManifestSchema) { m.Skills = append(m.Skills, m.Skills[0]) }, want: "duplicate skill"},
		{name: "unknown skill tool", mutate: func(m *manifestmodel.ManifestSchema) { m.Skills[0].AllowedTools = []string{"invented_tool"} }, want: "unsupported Agent tool"},
		{name: "object tool without objects", mutate: func(m *manifestmodel.ManifestSchema) { m.Skills[0].AllowedObjects = nil }, want: "must declare at least one object"},
		{name: "unknown skill object", mutate: func(m *manifestmodel.ManifestSchema) { m.Skills[0].AllowedObjects = []string{"missing"} }, want: "references unknown object"},
		{name: "duplicate skill object", mutate: func(m *manifestmodel.ManifestSchema) { m.Skills[0].AllowedObjects = []string{"customer", "customer"} }, want: "duplicate value"},
		{name: "unknown agent skill", mutate: func(m *manifestmodel.ManifestSchema) { m.Agents[0].SkillKeys = []string{"missing"} }, want: "references unknown skill"},
		{name: "duplicate agent skill", mutate: func(m *manifestmodel.ManifestSchema) {
			m.Agents[0].SkillKeys = []string{"customer_reader", "customer_reader"}
		}, want: "duplicate value"},
		{name: "negative agent limit", mutate: func(m *manifestmodel.ManifestSchema) { m.Agents[0].ExecutionLimits.MaxSteps = -1 }, want: "must not be negative"},
		{name: "unknown agent cost budget", mutate: func(m *manifestmodel.ManifestSchema) { m.Agents[0].ExecutionLimits.CostBudget = "unlimited" }, want: "must be one of low, standard, high"},
		{name: "unknown task agent", mutate: func(m *manifestmodel.ManifestSchema) { m.AgentTasks[0].AgentKey = "missing" }, want: "references unknown agent"},
		{name: "unversioned task agent", mutate: func(m *manifestmodel.ManifestSchema) { m.Agents[0].Version = "" }, want: "references unversioned agent"},
		{name: "unversioned task skill", mutate: func(m *manifestmodel.ManifestSchema) { m.Skills[0].Version = "" }, want: "references unversioned skill"},
		{name: "task object outside skill", mutate: func(m *manifestmodel.ManifestSchema) { m.AgentTasks[0].AllowedObjects = []string{"invoice"} }, want: "outside the Agent Skill allowlist"},
		{name: "unknown task action", mutate: func(m *manifestmodel.ManifestSchema) { m.AgentTasks[0].AllowedActions = []string{"missing"} }, want: "references unknown Action"},
		{name: "invalid input schema", mutate: func(m *manifestmodel.ManifestSchema) { m.AgentTasks[0].InputSchema = map[string]any{"type": "string"} }, want: "input_schema.type: must be object"},
		{name: "invalid output schema", mutate: func(m *manifestmodel.ManifestSchema) { m.AgentTasks[0].OutputSchema = nil }, want: "output_schema: is required"},
		{name: "unknown local output schema", mutate: func(m *manifestmodel.ManifestSchema) {
			m.AgentTasks[0].OutputSchema["properties"] = map[string]any{"summary": map[string]any{"$ref": "#/$defs/missing"}}
		}, want: "unknown local schema"},
		{name: "unknown object output schema", mutate: func(m *manifestmodel.ManifestSchema) {
			m.AgentTasks[0].OutputSchema["properties"] = map[string]any{"record": map[string]any{"$ref": "domainry://objects/missing"}}
		}, want: "unknown object schema"},
		{name: "unknown action output schema", mutate: func(m *manifestmodel.ManifestSchema) {
			m.AgentTasks[0].OutputSchema["properties"] = map[string]any{"action": map[string]any{"$ref": "domainry://actions/missing"}}
		}, want: "unknown Action schema"},
		{name: "unsupported output schema reference", mutate: func(m *manifestmodel.ManifestSchema) {
			m.AgentTasks[0].OutputSchema["properties"] = map[string]any{"record": map[string]any{"$ref": "https://example.com/schema"}}
		}, want: "unsupported schema reference"},
		{name: "unknown side effect", mutate: func(m *manifestmodel.ManifestSchema) { m.AgentTasks[0].SideEffectMode = "root" }, want: "unsupported mode"},
		{name: "analysis action", mutate: func(m *manifestmodel.ManifestSchema) { m.AgentTasks[0].AllowedActions = []string{"invoice.approve"} }, want: "must be empty for analysis_only"},
		{name: "proposal without action", mutate: func(m *manifestmodel.ManifestSchema) {
			m.AgentTasks[0].SideEffectMode = agentmodel.AgentTaskSideEffectProposalOnly
		}, want: "must declare at least one Business Action"},
		{name: "proposal without tool", mutate: func(m *manifestmodel.ManifestSchema) {
			m.AgentTasks[0].SideEffectMode = agentmodel.AgentTaskSideEffectProposalOnly
			m.AgentTasks[0].AllowedActions = []string{"invoice.approve"}
			m.Skills[0].AllowedTools = nil
		}, want: "does not allow invoke_action"},
		{name: "unknown outcome", mutate: func(m *manifestmodel.ManifestSchema) { m.AgentTasks[0].AllowedOutcomes = []string{"root"} }, want: "unsupported outcome"},
		{name: "duplicate outcome", mutate: func(m *manifestmodel.ManifestSchema) {
			m.AgentTasks[0].AllowedOutcomes = []string{"success", "success"}
		}, want: "duplicate outcome"},
		{name: "invalid service role key", mutate: func(m *manifestmodel.ManifestSchema) { m.AgentServicePrincipals[0].RoleKey = "" }, want: "stable non-empty Identity role key"},
		{name: "missing service user", mutate: func(m *manifestmodel.ManifestSchema) { m.AgentServicePrincipals[0].UserID = "" }, want: "stable non-empty service identity"},
		{name: "invalid service rotation", mutate: func(m *manifestmodel.ManifestSchema) { m.AgentServicePrincipals[0].RotationVersion = 0 }, want: "must be at least 1"},
		{name: "unknown entrypoint agent", mutate: func(m *manifestmodel.ManifestSchema) { m.AgentEntrypoints[0].AgentKey = "missing" }, want: "references unknown agent"},
		{name: "unknown surface", mutate: func(m *manifestmodel.ManifestSchema) { m.AgentEntrypoints[0].Surface = "root" }, want: "unknown product Surface"},
		{name: "empty entrypoint permission", mutate: func(m *manifestmodel.ManifestSchema) { m.AgentEntrypoints[0].RequiredPermissions = nil }, want: "must declare at least one permission"},
		{name: "unknown route", mutate: func(m *manifestmodel.ManifestSchema) { m.AgentEntrypoints[0].RoutePatterns = []string{"root.*"} }, want: "invalid or unknown route pattern"},
		{name: "cross surface route", mutate: func(m *manifestmodel.ManifestSchema) { m.AgentEntrypoints[0].Surface = "consumer_portal" }, want: "belongs to Surface"},
		{name: "unknown task target", mutate: func(m *manifestmodel.ManifestSchema) { m.AgentEntrypoints[0].AllowedTaskKeys = []string{"missing"} }, want: "unknown Agent Task"},
		{name: "task target belongs to another agent", mutate: func(m *manifestmodel.ManifestSchema) {
			m.Agents = append(m.Agents, agentmodel.AgentSchema{Key: "other", Version: "1.0.0", Name: "Other"})
			m.AgentTasks[0].AgentKey = "other"
		}, want: "not entrypoint Agent"},
		{name: "disabled task target", mutate: func(m *manifestmodel.ManifestSchema) { m.AgentTasks[0].Enabled = false }, want: "disabled Agent Task"},
		{name: "unknown workflow target", mutate: func(m *manifestmodel.ManifestSchema) { m.AgentEntrypoints[0].AllowedWorkflowKeys = []string{"missing"} }, want: "unknown Workflow"},
		{name: "disabled workflow target", mutate: func(m *manifestmodel.ManifestSchema) { m.Workflows[0].Enabled = false }, want: "disabled Workflow"},
		{name: "workflow target not manual", mutate: func(m *manifestmodel.ManifestSchema) { m.Workflows[0].TriggerContract.Type = "scheduled" }, want: "invocation.workflow_entry_mode_invalid"},
		{name: "default conflict", mutate: func(m *manifestmodel.ManifestSchema) {
			duplicate := m.AgentEntrypoints[0]
			duplicate.Key = "assistant.secondary"
			m.AgentEntrypoints = append(m.AgentEntrypoints, duplicate)
		}, want: "conflicts with default assignment"},
		{name: "business entrypoint key conflict", mutate: func(m *manifestmodel.ManifestSchema) { m.AgentEntrypoints[0].Key = "workspace.home" }, want: "conflicts with business entrypoint"},
		{name: "unknown context hint", mutate: func(m *manifestmodel.ManifestSchema) {
			m.AgentEntrypoints[0].ContextContract.AllowedHintFields = []string{"principal"}
		}, want: "unsupported context hint"},
		{name: "context selection bound", mutate: func(m *manifestmodel.ManifestSchema) { m.AgentEntrypoints[0].ContextContract.MaxSelectedRecord = 101 }, want: "must be between 1 and 100"},
		{name: "context byte bound", mutate: func(m *manifestmodel.ManifestSchema) { m.AgentEntrypoints[0].ContextContract.MaxContextBytes = 1 }, want: "must be between 1024 and 1048576"},
		{name: "unknown route type", mutate: func(m *manifestmodel.ManifestSchema) {
			m.AgentEntrypoints[0].RoutingContract.AllowedRouteTypes = []string{"root"}
		}, want: "unsupported route type"},
		{name: "recursive route", mutate: func(m *manifestmodel.ManifestSchema) { m.AgentEntrypoints[0].RoutingContract.AllowRecursive = true }, want: "recursive Agent delegation is not supported"},
		{name: "unknown node task", mutate: func(m *manifestmodel.ManifestSchema) {
			m.Workflows[0].Graph.Nodes[1].Contract.AgentTask.TaskKey = "missing"
		}, want: "references unknown Agent Task"},
		{name: "node task version drift", mutate: func(m *manifestmodel.ManifestSchema) {
			m.Workflows[0].Graph.Nodes[1].Contract.AgentTask.TaskVersion = "2.0.0"
		}, want: "must match Agent Task"},
		{name: "unknown node principal", mutate: func(m *manifestmodel.ManifestSchema) {
			m.Workflows[0].Graph.Nodes[1].Contract.AgentTask.Identity.PrincipalKey = "missing"
		}, want: "unknown service principal"},
		{name: "disabled node principal", mutate: func(m *manifestmodel.ManifestSchema) { m.AgentServicePrincipals[0].Enabled = false }, want: "disabled service principal"},
		{name: "node object widening", mutate: func(m *manifestmodel.ManifestSchema) {
			m.Workflows[0].Graph.Nodes[1].Contract.AgentTask.AllowedObjects = []string{"invoice"}
		}, want: "outside Agent Task allowlist"},
		{name: "node action widening", mutate: func(m *manifestmodel.ManifestSchema) {
			m.Workflows[0].Graph.Nodes[1].Contract.AgentTask.AllowedActions = []string{"invoice.approve"}
		}, want: "outside Agent Task allowlist"},
		{name: "node outcome widening", mutate: func(m *manifestmodel.ManifestSchema) {
			m.Workflows[0].Graph.Nodes[1].Contract.AgentTask.AllowedOutcomes = []string{"manual_review"}
		}, want: "outside Agent Task outcomes"},
		{name: "undeclared edge branch", mutate: func(m *manifestmodel.ManifestSchema) { m.Workflows[0].Graph.Edges[1].Branch = "manual_review" }, want: "is not declared by Agent Task"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := validAgentContractManifest()
			test.mutate(&manifest)
			state := newValidationState(manifest, nil)
			state.validateAgents()
			if got := state.errs.Error(); !strings.Contains(got, test.want) {
				t.Fatalf("validation errors = %q, want substring %q", got, test.want)
			}
		})
	}
}

func validAgentContractManifest() manifestmodel.ManifestSchema {
	return manifestmodel.ManifestSchema{
		Objects:     []definitionmodel.ObjectSchema{{Key: "customer"}, {Key: "invoice"}},
		Actions:     []definitionmodel.ActionSchema{{Key: "invoice.approve"}},
		EntryPoints: []definitionmodel.EntryPointSchema{{Key: "workspace.home", RequiredPermissions: []string{"workspace.use"}, Config: map[string]any{"kind": "backoffice"}}},
		Skills: []agentmodel.SkillSchema{{
			Key: "customer_reader", Version: "1.0.0", Name: "Customer reader",
			AllowedTools: []string{"query_records", "invoke_action"}, AllowedObjects: []string{"customer"},
		}},
		Agents: []agentmodel.AgentSchema{{
			Key: "customer_agent", Version: "1.0.0", Name: "Customer agent", SkillKeys: []string{"customer_reader"},
			ExecutionLimits: agentmodel.AgentExecutionLimits{MaxSteps: 8, TimeoutSeconds: 60, MaxToolCalls: 4, MaxInputBytes: 4096, MaxOutputBytes: 4096},
		}},
		AgentTasks: []agentmodel.AgentTaskDefinition{{
			ContractVersion: agentmodel.AgentTaskContractVersion, Key: "customer.summarize", Version: "1.0.0", AgentKey: "customer_agent",
			Instruction: "Summarize a customer", InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{
				"type": "object", "$defs": map[string]any{"summary": map[string]any{"type": "string"}},
				"properties": map[string]any{"summary": map[string]any{"$ref": "#/$defs/summary"}, "record": map[string]any{"$ref": "domainry://objects/customer"}, "action": map[string]any{"$ref": "domainry://actions/invoice.approve"}},
			},
			AllowedObjects: []string{"customer"}, AllowedOutcomes: []string{"success", "error"}, SideEffectMode: agentmodel.AgentTaskSideEffectAnalysisOnly,
			ExecutionLimits: agentmodel.AgentExecutionLimits{MaxSteps: 4, TimeoutSeconds: 30, MaxToolCalls: 2, MaxInputBytes: 2048, MaxOutputBytes: 2048}, Enabled: true,
		}},
		AgentServicePrincipals: []agentmodel.AgentServicePrincipalBinding{{
			ContractVersion: agentmodel.AgentServicePrincipalContractVersion, Key: "customer_agent_service", UserID: "agent_customer_service", RoleKey: "agent_service", Enabled: true, RotationVersion: 1,
		}},
		AgentEntrypoints: []agentmodel.AgentEntrypointAssignment{{
			ContractVersion: agentmodel.AgentEntrypointContractVersion, Key: "assistant.global", AgentKey: "customer_agent", Surface: "business_workspace",
			DefaultForSurface: true, RequiredPermissions: []string{"agent.use"}, RoutePatterns: []string{"workspace.*"}, AllowedTaskKeys: []string{"customer.summarize"},
			AllowedWorkflowKeys: []string{"customer.review"}, Enabled: true,
			ContextContract: agentmodel.GlobalAgentContextContract{ContractVersion: agentmodel.GlobalAgentContextContractVersion, AllowedHintFields: []string{"route_key", "record_id"}, MaxSelectedRecord: 20, MaxContextBytes: 65536},
			RoutingContract: agentmodel.AgentRoutingContract{ContractVersion: agentmodel.AgentRoutingContractVersion, AllowedRouteTypes: []string{agentmodel.AgentRouteInteractiveQuery, agentmodel.AgentRouteTask, agentmodel.AgentRouteWorkflow}},
		}},
		Workflows: []definitionmodel.WorkflowSchema{{
			Key: "customer.review", Enabled: true, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"},
			Graph: &definitionmodel.WorkflowGraphSchema{Version: 2,
				Nodes: []definitionmodel.WorkflowGraphNode{
					{ID: "trigger", Type: "trigger"},
					{ID: "summarize", Type: "agent_task", Contract: &definitionmodel.WorkflowNodeContract{AgentTask: &definitionmodel.WorkflowAgentTaskNodeContract{
						TaskKey: "customer.summarize", TaskVersion: "1.0.0", Identity: definitionmodel.WorkflowAgentTaskIdentity{Mode: agentmodel.AgentTaskIdentityService, PrincipalKey: "customer_agent_service"},
						Input: map[string]any{"customer_id": "${record.id}"}, OutputVariable: "summary", ExecutionMode: "async", TimeoutSeconds: 30,
						Retry: &definitionmodel.WorkflowRetryPolicy{MaxAttempts: 2}, OnError: "error_branch", AllowedObjects: []string{"customer"}, AllowedOutcomes: []string{"success", "error"},
					}}},
					{ID: "done", Type: "action"},
				},
				Edges: []definitionmodel.WorkflowGraphEdge{{Source: "trigger", Target: "summarize"}, {Source: "summarize", Target: "done", Branch: "success"}, {Source: "summarize", Target: "done", Branch: "error"}},
			},
		}},
	}
}
