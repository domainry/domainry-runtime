package projection

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func routeProjectionProcess() workflowmodel.WorkflowProcessInstance {
	node := definitionmodel.WorkflowGraphNode{ID: "review", Type: "approval", Name: "Review", Contract: &definitionmodel.WorkflowNodeContract{
		Approval: &definitionmodel.WorkflowApprovalNodeContract{Mode: "any", Route: &definitionmodel.WorkflowApprovalRouteContract{Source: "instance", MinSteps: 1, MaxSteps: 3, MaxAssigneesPerStep: 2, DeferredSteps: "allow"}},
	}}
	return workflowmodel.WorkflowProcessInstance{
		ID: "process_1", InitiatorID: "initiator",
		DefinitionSnapshot: definitionmodel.WorkflowSchema{Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{node}}},
	}
}

func routeProjectionSteps() []workflowmodel.WorkflowRouteStep {
	return []workflowmodel.WorkflowRouteStep{
		{StepNo: 1, StepKey: "s1", NodeID: "review", Status: "approved", Mode: "any", NodeInstanceID: "node_1", AssigneeSnapshot: []workflowmodel.WorkflowRouteAssignee{{UserID: "first"}}},
		{StepNo: 2, StepKey: "s2", NodeID: "review", Status: "active", Mode: "any", NodeInstanceID: "node_2", AssigneeSnapshot: []workflowmodel.WorkflowRouteAssignee{{UserID: "second"}}},
		{StepNo: 3, StepKey: "s3", NodeID: "review", Status: "configurable", Mode: "any"},
	}
}

func TestWorkflowRouteProjectionDisclosesAssigneesOnlyWhereTheReaderBelongs(t *testing.T) {
	process, steps := routeProjectionProcess(), routeProjectionSteps()
	tasks := []workflowmodel.WorkflowTask{
		{NodeInstanceID: "node_1", Status: "approved"}, {NodeInstanceID: "node_2", Status: "open"},
	}
	initiator := WorkflowRouteForPrincipal(process, steps, tasks, "initiator")
	if len(initiator.Steps) != 3 || initiator.Steps[0].ApprovedCount != 1 || initiator.Steps[1].ApprovedCount != 0 {
		t.Fatalf("initiator view=%#v", initiator.Steps)
	}
	for index, step := range initiator.Steps[:2] {
		if len(step.Assignees) != 1 {
			t.Fatalf("the initiator must see step %d approvers: %#v", index+1, step)
		}
	}
	if len(initiator.PendingConfiguration) != 1 || initiator.PendingConfiguration[0] != "s3" {
		t.Fatalf("pending configuration=%#v", initiator.PendingConfiguration)
	}
	// The route's deferred configurer defaults to the previous step approver,
	// so the initiator may not configure step 3 while "second" may.
	if initiator.ConfigurableByMe {
		t.Fatalf("the initiator configured a previous_step_approver route: %#v", initiator)
	}
	second := WorkflowRouteForPrincipal(process, steps, tasks, "second")
	if !second.ConfigurableByMe || !second.Steps[2].ConfigurableByMe {
		t.Fatalf("the step-2 approver must configure step 3: %#v", second)
	}
	if len(second.Steps[1].Assignees) != 1 {
		t.Fatalf("an approver must see its own step: %#v", second.Steps[1])
	}
	if len(second.Steps[0].Assignees) != 1 || second.Steps[0].AssigneeCount != 1 {
		t.Fatalf("a completed step stays visible to a participant: %#v", second.Steps[0])
	}
	active := WorkflowRouteForPrincipal(process, steps, tasks, "first")
	if len(active.Steps[1].Assignees) != 0 || active.Steps[1].AssigneeCount != 1 {
		t.Fatalf("an unrelated active step must expose only its count: %#v", active.Steps[1])
	}
	if !WorkflowRouteVisibleTo(process, steps, "initiator") || !WorkflowRouteVisibleTo(process, steps, "second") {
		t.Fatal("participants must see the route")
	}
	if WorkflowRouteVisibleTo(process, steps, "outsider") || WorkflowRouteVisibleTo(process, steps, " ") {
		t.Fatal("an unrelated reader must not see the route")
	}
}

func TestWorkflowRouteConfigurerPolicySelectsTheAuthorizedPrincipal(t *testing.T) {
	process, steps := routeProjectionProcess(), routeProjectionSteps()
	route, ok := WorkflowRouteContract(process, steps)
	if !ok || route.DeferredConfigurer != "previous_step_approver" {
		t.Fatalf("route contract=%#v ok=%v", route, ok)
	}
	if !WorkflowRouteConfigurerAllows(route, steps, 2, process, "second") || WorkflowRouteConfigurerAllows(route, steps, 2, process, "initiator") {
		t.Fatal("previous_step_approver must authorize only the previous step approvers")
	}
	if WorkflowRouteConfigurerAllows(route, steps, 0, process, "second") {
		t.Fatal("a first step can never be configured by a previous approver")
	}
	route.DeferredConfigurer = "initiator"
	if !WorkflowRouteConfigurerAllows(route, steps, 2, process, "initiator") || WorkflowRouteConfigurerAllows(route, steps, 2, process, "second") {
		t.Fatal("initiator policy must authorize only the initiator")
	}
}
