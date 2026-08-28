package workflow

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestWorkflowSimulateNodeTypeAndOutcomeMatrix(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user", WorkspaceID: "workspace"}}
	process := workflowmodel.WorkflowProcessInstance{Variables: map[string]any{"amount": 10}, InitiatorID: "user"}
	actionExists := true
	engine := NewWorkflowProcessRuntime(WorkflowDependencies{
		Identity:     workflowIdentityEdgeStub{},
		ActionExists: func(context.Context, string) bool { return actionExists },
	}).ProcessEngine()

	trigger := definitionmodel.WorkflowGraphNode{ID: "trigger", Type: "trigger"}
	preview, outcomes, err := engine.simulateNode(t.Context(), process, trigger, principal)
	if err != nil || preview.Outcome != "success" || len(outcomes) != 1 {
		t.Fatalf("trigger preview=%+v outcomes=%v err=%v", preview, outcomes, err)
	}
	condition := definitionmodel.WorkflowGraphNode{ID: "condition", Type: "condition", Contract: &definitionmodel.WorkflowNodeContract{Condition: &definitionmodel.WorkflowConditionContract{Type: "field_equals", Field: "amount", Value: 10}}}
	preview, outcomes, err = engine.simulateNode(t.Context(), process, condition, principal)
	if err != nil || preview.Outcome != "true" || outcomes[0] != "true" {
		t.Fatalf("condition preview=%+v outcomes=%v err=%v", preview, outcomes, err)
	}
	condition.Contract.Condition.Value = 11
	preview, outcomes, err = engine.simulateNode(t.Context(), process, condition, principal)
	if err != nil || preview.Outcome != "false" || outcomes[0] != "false" {
		t.Fatalf("false condition preview=%+v outcomes=%v err=%v", preview, outcomes, err)
	}
	approvalContract := definitionmodel.WorkflowApprovalNodeContract{ResolverMode: "all", EmptyAssigneePolicy: "fail", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"approver"}}}}
	approval := definitionmodel.WorkflowGraphNode{ID: "approval", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &approvalContract}}
	preview, outcomes, err = engine.simulateNode(t.Context(), process, approval, principal)
	if err != nil || len(preview.ResolvedAssignees) != 1 || len(outcomes) != 2 {
		t.Fatalf("approval preview=%+v outcomes=%v err=%v", preview, outcomes, err)
	}
	invalidApproval := approval
	invalidContract := approvalContract
	invalidContract.Resolvers = []definitionmodel.WorkflowAssigneeResolver{{Type: "invalid"}}
	invalidApproval.Contract = &definitionmodel.WorkflowNodeContract{Approval: &invalidContract}
	if _, _, err := engine.simulateNode(t.Context(), process, invalidApproval, principal); apperror.CodeOf(err) != "backend.workflow.approval_resolver_invalid" {
		t.Fatalf("approval error=%v", err)
	}
	actionContract := definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "send", OnError: "error_branch"}
	action := definitionmodel.WorkflowGraphNode{ID: "action", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &actionContract}}
	preview, outcomes, err = engine.simulateNode(t.Context(), process, action, principal)
	if err != nil || preview.ActionKey != "send" || len(outcomes) != 2 {
		t.Fatalf("action preview=%+v outcomes=%v err=%v", preview, outcomes, err)
	}
	actionContract.OnError = "fail"
	action.Contract = &definitionmodel.WorkflowNodeContract{Action: &actionContract}
	_, outcomes, err = engine.simulateNode(t.Context(), process, action, principal)
	if err != nil || len(outcomes) != 1 {
		t.Fatalf("action outcomes=%v err=%v", outcomes, err)
	}
	actionExists = false
	if _, _, err := engine.simulateNode(t.Context(), process, action, principal); apperror.CodeOf(err) != "backend.workflow.action_not_found" {
		t.Fatalf("missing action error=%v", err)
	}
	ccContract := definitionmodel.WorkflowCCNodeContract{NotificationActionKey: "notify", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"recipient"}}}}
	cc := definitionmodel.WorkflowGraphNode{ID: "cc", Type: "cc", Contract: &definitionmodel.WorkflowNodeContract{CC: &ccContract}}
	preview, outcomes, err = engine.simulateNode(t.Context(), process, cc, principal)
	if err != nil || preview.ActionKey != "notify" || len(preview.ResolvedAssignees) != 1 || len(outcomes) != 1 {
		t.Fatalf("cc preview=%+v outcomes=%v err=%v", preview, outcomes, err)
	}
	ccContract.Resolvers = []definitionmodel.WorkflowAssigneeResolver{{Type: "invalid"}}
	cc.Contract = &definitionmodel.WorkflowNodeContract{CC: &ccContract}
	if _, _, err := engine.simulateNode(t.Context(), process, cc, principal); err == nil {
		t.Fatal("expected cc resolver error")
	}
	if _, _, err := engine.simulateNode(t.Context(), process, definitionmodel.WorkflowGraphNode{ID: "unknown", Type: "unknown"}, principal); apperror.CodeOf(err) != "backend.workflow.graph_node_type_invalid" {
		t.Fatalf("unknown node error=%v", err)
	}
}

func TestWorkflowSimulationAuthorizationAndGraphValidation(t *testing.T) {
	engine := NewWorkflowProcessRuntime(WorkflowDependencies{}).ProcessEngine()
	if _, err := engine.Simulate(t.Context(), definitionmodel.WorkflowSchema{}, nil, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization error=%v", err)
	}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}}
	if _, err := engine.Simulate(t.Context(), definitionmodel.WorkflowSchema{}, nil, principal); err == nil {
		t.Fatal("expected graph validation error")
	}
	actionContract := definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "send"}
	approvalContract := definitionmodel.WorkflowApprovalNodeContract{Mode: "any", EmptyAssigneePolicy: "fail", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"approver"}}}}
	workflow := definitionmodel.WorkflowSchema{Key: "flow", Graph: &definitionmodel.WorkflowGraphSchema{
		Version: 2,
		Nodes: []definitionmodel.WorkflowGraphNode{
			{ID: "trigger", Type: "trigger"},
			{ID: "approval", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &approvalContract}},
			{ID: "action", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &actionContract}},
		},
		Edges: []definitionmodel.WorkflowGraphEdge{
			{ID: "edge-1", Source: "trigger", Target: "approval", Branch: "success"},
			{ID: "edge-2", Source: "approval", Target: "action", Branch: "approved"},
			{ID: "edge-3", Source: "approval", Target: "action", Branch: "rejected"},
		},
	}}
	engine = NewWorkflowProcessRuntime(WorkflowDependencies{Identity: workflowIdentityEdgeStub{}, ActionExists: func(context.Context, string) bool { return true }}).ProcessEngine()
	nodes, err := engine.Simulate(t.Context(), workflow, nil, principal)
	if err != nil || len(nodes) != 3 {
		t.Fatalf("nodes=%v err=%v", nodes, err)
	}
}
