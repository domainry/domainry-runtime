package validation

import (
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestAgentBusinessScenarioDefinitionsCompileAsPublishedContracts(t *testing.T) {
	manifest := agentBusinessScenarioManifest()
	state := newValidationState(manifest, nil)
	state.validateAgents()
	if len(state.errs) != 0 {
		t.Fatalf("business Agent scenarios rejected: %v", state.errs)
	}

	resume := manifest.AgentTasks[0]
	if resume.Key != "resume.screen" || resume.SideEffectMode != agentsdk.AgentTaskSideEffectProposalOnly || strings.Join(resume.AllowedActions, ",") != "candidate.reject" {
		t.Fatalf("resume screening contract = %#v", resume)
	}
	service := manifest.Workflows[1].Graph.Nodes[1].Contract.AgentTask.Identity
	if service.Mode != agentsdk.AgentTaskIdentityService || service.PrincipalKey != "support_preprocessor" {
		t.Fatalf("customer preprocessing identity = %#v", service)
	}
	shift := manifest.Workflows[2].Graph
	if shift.Nodes[1].Type != "action" || shift.Nodes[1].Contract.Action.ActionKey != "shift.filter_eligible" || shift.Nodes[2].Type != "agent_task" || shift.Nodes[3].Contract.Action.ActionKey != "shift.assign" {
		t.Fatalf("shift deterministic/Agent/atomic order = %#v", shift.Nodes)
	}
}

func TestAgentBusinessScenarioContractsRejectPrivilegeAndOrderingDrift(t *testing.T) {
	for name, test := range map[string]struct {
		mutate func(*manifestmodel.ManifestSchema)
	}{
		"resume direct write": {func(m *manifestmodel.ManifestSchema) {
			m.AgentTasks[0].SideEffectMode = agentsdk.AgentTaskSideEffectActionAllowed
		}},
		"support inherits sender": {func(m *manifestmodel.ManifestSchema) {
			m.Workflows[1].Graph.Nodes[1].Contract.AgentTask.Identity = definitionmodel.WorkflowAgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit}
		}},
		"shift Agent bypasses filter": {func(m *manifestmodel.ManifestSchema) { m.Workflows[2].Graph.Edges[0].Target = "rank" }},
	} {
		t.Run(name, func(t *testing.T) {
			manifest := agentBusinessScenarioManifest()
			test.mutate(&manifest)
			switch name {
			case "resume direct write":
				if manifest.AgentTasks[0].SideEffectMode != agentsdk.AgentTaskSideEffectProposalOnly {
					return
				}
			case "support inherits sender":
				identity := manifest.Workflows[1].Graph.Nodes[1].Contract.AgentTask.Identity
				if identity.Mode != agentsdk.AgentTaskIdentityService || identity.PrincipalKey == "" {
					return
				}
			case "shift Agent bypasses filter":
				if manifest.Workflows[2].Graph.Edges[0].Target != "filter" {
					return
				}
			}
			t.Fatalf("scenario drift was not detected: %s", name)
		})
	}
}

func agentBusinessScenarioManifest() manifestmodel.ManifestSchema {
	objects := []definitionmodel.ObjectSchema{{Key: "candidate"}, {Key: "resume"}, {Key: "support_message"}, {Key: "order"}, {Key: "employee"}, {Key: "leave"}, {Key: "shift"}}
	actions := []definitionmodel.ActionSchema{{Key: "candidate.reject", ObjectKey: "candidate"}, {Key: "candidate.screen_complete", ObjectKey: "candidate"}, {Key: "support.classify_complete", ObjectKey: "support_message"}, {Key: "order.refund", ObjectKey: "order"}, {Key: "order.change", ObjectKey: "order"}, {Key: "shift.filter_eligible", ObjectKey: "shift"}, {Key: "shift.assign", ObjectKey: "shift"}, {Key: "shift.manual_review", ObjectKey: "shift"}}
	skills := []agentsdk.SkillSchema{
		{Key: "talent_reader", Version: "1.0.0", Name: "Talent reader", AllowedTools: []string{"query_records", "invoke_action"}, AllowedObjects: []string{"candidate", "resume"}},
		{Key: "support_reader", Version: "1.0.0", Name: "Support reader", AllowedTools: []string{"query_records", "invoke_action"}, AllowedObjects: []string{"support_message", "order"}},
		{Key: "shift_ranker", Version: "1.0.0", Name: "Shift ranker", AllowedTools: []string{"query_records"}, AllowedObjects: []string{"employee", "leave", "shift"}},
	}
	agents := []agentsdk.AgentSchema{
		{Key: "talent_agent", Version: "1.0.0", Name: "Talent agent", SkillKeys: []string{"talent_reader"}},
		{Key: "support_agent", Version: "1.0.0", Name: "Support agent", SkillKeys: []string{"support_reader"}},
		{Key: "shift_agent", Version: "1.0.0", Name: "Shift agent", SkillKeys: []string{"shift_ranker"}},
	}
	tasks := []agentsdk.AgentTaskDefinition{
		agentScenarioTask("resume.screen", "talent_agent", []string{"candidate", "resume"}, []string{"candidate.reject"}, agentsdk.AgentTaskSideEffectProposalOnly, []string{"success", "manual_review", "rejected", "error"}, map[string]any{"score": map[string]any{"type": "number"}, "match": map[string]any{"type": "array"}, "risks": map[string]any{"type": "array"}, "review": map[string]any{"type": "string"}}),
		agentScenarioTask("support.preprocess", "support_agent", []string{"support_message", "order"}, []string{"order.refund", "order.change"}, agentsdk.AgentTaskSideEffectProposalOnly, []string{"success", "manual_review", "error"}, map[string]any{"redacted_summary": map[string]any{"type": "string"}, "category": map[string]any{"type": "string"}, "sentiment": map[string]any{"type": "string"}, "priority": map[string]any{"type": "string"}}),
		agentScenarioTask("shift.rank", "shift_agent", []string{"employee", "leave", "shift"}, nil, agentsdk.AgentTaskSideEffectAnalysisOnly, []string{"success", "manual_review", "no_result", "error"}, map[string]any{"ranked_employee_ids": map[string]any{"type": "array"}, "explanations": map[string]any{"type": "array"}}),
	}
	servicePrincipals := []agentsdk.AgentServicePrincipalBinding{{ContractVersion: agentsdk.AgentServicePrincipalContractVersion, Key: "support_preprocessor", UserID: "agent_support_preprocessor", RoleKey: "support_service", Enabled: true, RotationVersion: 1}}
	workflows := []definitionmodel.WorkflowSchema{
		agentScenarioWorkflow("resume_screening", agentScenarioNode("screen", "resume.screen", definitionmodel.WorkflowAgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit}, []string{"candidate", "resume"}, []string{"candidate.reject"}, []string{"success", "manual_review", "rejected", "error"}), "candidate.screen_complete"),
		agentScenarioWorkflow("support_preprocessing", agentScenarioNode("preprocess", "support.preprocess", definitionmodel.WorkflowAgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityService, PrincipalKey: "support_preprocessor"}, []string{"support_message", "order"}, []string{"order.refund", "order.change"}, []string{"success", "manual_review", "error"}), "support.classify_complete"),
		shiftScenarioWorkflow(),
	}
	return manifestmodel.ManifestSchema{Objects: objects, Actions: actions, Skills: skills, Agents: agents, AgentTasks: tasks, AgentServicePrincipals: servicePrincipals, Workflows: workflows}
}

func agentScenarioTask(key, agentKey string, objects, actions []string, mode string, outcomes []string, properties map[string]any) agentsdk.AgentTaskDefinition {
	return agentsdk.AgentTaskDefinition{ContractVersion: agentsdk.AgentTaskContractVersion, Key: key, Version: "1.0.0", AgentKey: agentKey, Instruction: key, InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object", "properties": properties, "additionalProperties": false}, AllowedObjects: objects, AllowedActions: actions, AllowedOutcomes: outcomes, SideEffectMode: mode, ExecutionLimits: agentsdk.AgentExecutionLimits{MaxSteps: 6, TimeoutSeconds: 60, MaxToolCalls: 4, MaxInputBytes: 8192, MaxOutputBytes: 8192}, Enabled: true}
}

func agentScenarioNode(id, taskKey string, identity definitionmodel.WorkflowAgentTaskIdentity, objects, actions, outcomes []string) definitionmodel.WorkflowGraphNode {
	return definitionmodel.WorkflowGraphNode{ID: id, Type: "agent_task", Contract: &definitionmodel.WorkflowNodeContract{AgentTask: &definitionmodel.WorkflowAgentTaskNodeContract{TaskKey: taskKey, TaskVersion: "1.0.0", Identity: identity, Input: map[string]any{"record_id": "${record.id}"}, OutputVariable: id + "_output", ExecutionMode: "async", TimeoutSeconds: 60, Retry: &definitionmodel.WorkflowRetryPolicy{MaxAttempts: 3}, OnError: "error_branch", AllowedObjects: objects, AllowedActions: actions, AllowedOutcomes: outcomes}}}
}

func agentScenarioWorkflow(key string, node definitionmodel.WorkflowGraphNode, terminalAction string) definitionmodel.WorkflowSchema {
	return definitionmodel.WorkflowSchema{Key: key, Enabled: true, Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, node, {ID: "done", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: terminalAction}}}}, Edges: []definitionmodel.WorkflowGraphEdge{{Source: "trigger", Target: node.ID}, {Source: node.ID, Target: "done", Branch: "success"}, {Source: node.ID, Target: "done", Branch: "error"}}}}
}

func shiftScenarioWorkflow() definitionmodel.WorkflowSchema {
	node := agentScenarioNode("rank", "shift.rank", definitionmodel.WorkflowAgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit}, []string{"employee", "leave", "shift"}, nil, []string{"success", "manual_review", "no_result", "error"})
	return definitionmodel.WorkflowSchema{Key: "shift_assignment", Enabled: true, Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{
		{ID: "trigger", Type: "trigger"},
		{ID: "filter", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "shift.filter_eligible"}}},
		node,
		{ID: "assign", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "shift.assign"}}},
		{ID: "manual", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "shift.manual_review"}}},
	}, Edges: []definitionmodel.WorkflowGraphEdge{{Source: "trigger", Target: "filter"}, {Source: "filter", Target: "rank"}, {Source: "rank", Target: "assign", Branch: "success"}, {Source: "rank", Target: "manual", Branch: "manual_review"}, {Source: "rank", Target: "manual", Branch: "no_result"}, {Source: "rank", Target: "manual", Branch: "error"}}}}
}
