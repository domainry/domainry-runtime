package policy

import (
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestWorkflowAgentTaskGraphContract(t *testing.T) {
	if err := WorkflowValidateGraph(validWorkflowAgentTaskGraph()); err != nil {
		t.Fatalf("valid Agent Task graph rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*definitionmodel.WorkflowGraphSchema)
		code   string
	}{
		{name: "missing contract", mutate: func(g *definitionmodel.WorkflowGraphSchema) { g.Nodes[1].Contract = nil }, code: "backend.workflow.agent_task_contract_required"},
		{name: "missing agent contract", mutate: func(g *definitionmodel.WorkflowGraphSchema) {
			g.Nodes[1].Contract = &definitionmodel.WorkflowNodeContract{}
		}, code: "backend.workflow.agent_task_contract_required"},
		{name: "missing task", mutate: func(g *definitionmodel.WorkflowGraphSchema) { g.Nodes[1].Contract.AgentTask.TaskKey = "" }, code: "backend.workflow.agent_task_contract_invalid"},
		{name: "missing task version", mutate: func(g *definitionmodel.WorkflowGraphSchema) { g.Nodes[1].Contract.AgentTask.TaskVersion = "" }, code: "backend.workflow.agent_task_contract_invalid"},
		{name: "missing input", mutate: func(g *definitionmodel.WorkflowGraphSchema) { g.Nodes[1].Contract.AgentTask.Input = nil }, code: "backend.workflow.agent_task_contract_invalid"},
		{name: "missing output", mutate: func(g *definitionmodel.WorkflowGraphSchema) { g.Nodes[1].Contract.AgentTask.OutputVariable = "" }, code: "backend.workflow.agent_task_contract_invalid"},
		{name: "synchronous", mutate: func(g *definitionmodel.WorkflowGraphSchema) { g.Nodes[1].Contract.AgentTask.ExecutionMode = "sync" }, code: "backend.workflow.agent_task_execution_policy_invalid"},
		{name: "timeout", mutate: func(g *definitionmodel.WorkflowGraphSchema) { g.Nodes[1].Contract.AgentTask.TimeoutSeconds = 0 }, code: "backend.workflow.agent_task_execution_policy_invalid"},
		{name: "retry", mutate: func(g *definitionmodel.WorkflowGraphSchema) { g.Nodes[1].Contract.AgentTask.Retry.MaxAttempts = 0 }, code: "backend.workflow.agent_task_execution_policy_invalid"},
		{name: "inherit principal", mutate: func(g *definitionmodel.WorkflowGraphSchema) {
			g.Nodes[1].Contract.AgentTask.Identity = definitionmodel.WorkflowAgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit, PrincipalKey: "root"}
		}, code: "backend.workflow.agent_task_identity_invalid"},
		{name: "service principal", mutate: func(g *definitionmodel.WorkflowGraphSchema) {
			g.Nodes[1].Contract.AgentTask.Identity = definitionmodel.WorkflowAgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityService}
		}, code: "backend.workflow.agent_task_identity_invalid"},
		{name: "unknown identity", mutate: func(g *definitionmodel.WorkflowGraphSchema) { g.Nodes[1].Contract.AgentTask.Identity.Mode = "root" }, code: "backend.workflow.agent_task_identity_invalid"},
		{name: "unknown on error", mutate: func(g *definitionmodel.WorkflowGraphSchema) { g.Nodes[1].Contract.AgentTask.OnError = "continue" }, code: "backend.workflow.agent_task_error_policy_invalid"},
		{name: "unknown outcome", mutate: func(g *definitionmodel.WorkflowGraphSchema) {
			g.Nodes[1].Contract.AgentTask.AllowedOutcomes = []string{"root"}
		}, code: "backend.workflow.agent_task_outcome_invalid"},
		{name: "duplicate outcome", mutate: func(g *definitionmodel.WorkflowGraphSchema) {
			g.Nodes[1].Contract.AgentTask.AllowedOutcomes = []string{"success", "success"}
		}, code: "backend.workflow.agent_task_outcome_invalid"},
		{name: "empty branch", mutate: func(g *definitionmodel.WorkflowGraphSchema) { g.Edges[1].Branch = "" }, code: "backend.workflow.agent_task_branch_invalid"},
		{name: "unknown branch", mutate: func(g *definitionmodel.WorkflowGraphSchema) { g.Edges[1].Branch = "root" }, code: "backend.workflow.agent_task_branch_invalid"},
		{name: "missing error branch", mutate: func(g *definitionmodel.WorkflowGraphSchema) { g.Edges = g.Edges[:2] }, code: "backend.workflow.agent_task_error_branch_required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			graph := validWorkflowAgentTaskGraph()
			test.mutate(graph)
			if got := workflowGraphCode(WorkflowValidateGraph(graph)); got != test.code {
				t.Fatalf("code = %q, want %q", got, test.code)
			}
		})
	}
}

func TestWorkflowAgentTaskOptionalRetryAndErrorBranchPolicy(t *testing.T) {
	graph := validWorkflowAgentTaskGraph()
	graph.Nodes[1].Contract.AgentTask.Retry = nil
	graph.Nodes[1].Contract.AgentTask.OnError = "fail"
	if err := WorkflowValidateGraph(graph); err != nil {
		t.Fatalf("optional policy rejected: %v", err)
	}
}

func TestWorkflowAgentTaskContractHelperReturnsZeroValue(t *testing.T) {
	if workflowAgentTaskNodeContract(definitionmodel.WorkflowGraphNode{}).TaskKey != "" {
		t.Fatal("missing Agent Task contract did not return zero value")
	}
	if workflowAgentTaskNodeContract(definitionmodel.WorkflowGraphNode{Contract: &definitionmodel.WorkflowNodeContract{}}).TaskKey != "" {
		t.Fatal("nil typed Agent Task contract did not return zero value")
	}
}

func TestWorkflowAgentTaskVersionIsPartOfDefinitionSnapshotHash(t *testing.T) {
	workflow := definitionmodel.WorkflowSchema{Key: "customer.review", Graph: validWorkflowAgentTaskGraph()}
	first := WorkflowDefinitionHash(workflow)
	workflow.Graph.Nodes[1].Contract.AgentTask.TaskVersion = "1.0.1"
	second := WorkflowDefinitionHash(workflow)
	if first == "" || second == "" || first == second {
		t.Fatalf("Agent Task version did not change Workflow definition snapshot hash: first=%q second=%q", first, second)
	}
}

func validWorkflowAgentTaskGraph() *definitionmodel.WorkflowGraphSchema {
	return &definitionmodel.WorkflowGraphSchema{
		Version: 2,
		Nodes: []definitionmodel.WorkflowGraphNode{
			{ID: "trigger", Type: "trigger"},
			{ID: "agent", Type: "agent_task", Contract: &definitionmodel.WorkflowNodeContract{AgentTask: &definitionmodel.WorkflowAgentTaskNodeContract{
				TaskKey: "customer.summarize", TaskVersion: "1.0.0", Identity: definitionmodel.WorkflowAgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityService, PrincipalKey: "agent_service"},
				Input: map[string]any{}, OutputVariable: "summary", ExecutionMode: "async", TimeoutSeconds: 30,
				Retry: &definitionmodel.WorkflowRetryPolicy{MaxAttempts: 2}, OnError: "error_branch", AllowedOutcomes: []string{"success", "error"},
			}}},
			{ID: "done", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "customer.finish"}}},
		},
		Edges: []definitionmodel.WorkflowGraphEdge{
			{Source: "trigger", Target: "agent"},
			{Source: "agent", Target: "done", Branch: "success"},
			{Source: "agent", Target: "done", Branch: "error"},
		},
	}
}
