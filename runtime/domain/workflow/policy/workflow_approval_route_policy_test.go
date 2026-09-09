package policy

import (
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func routeApprovalNode(route *definitionmodel.WorkflowApprovalRouteContract, resolvers ...definitionmodel.WorkflowAssigneeResolver) definitionmodel.WorkflowGraphNode {
	return definitionmodel.WorkflowGraphNode{ID: "approval", Type: "approval", Name: "Approval", Contract: &definitionmodel.WorkflowNodeContract{
		Approval: &definitionmodel.WorkflowApprovalNodeContract{Mode: "any", Resolvers: resolvers, Route: route},
	}}
}

func TestWorkflowApprovalRouteDefaultsAndValidation(t *testing.T) {
	if _, ok := WorkflowApprovalRoute(routeApprovalNode(nil, definitionmodel.WorkflowAssigneeResolver{Type: "initiator_manager"})); ok {
		t.Fatal("a node without a route contract reported one")
	}
	route, ok := WorkflowApprovalRoute(routeApprovalNode(&definitionmodel.WorkflowApprovalRouteContract{Source: "instance"}))
	if !ok || route.MinSteps != 1 || route.MaxSteps != 1 || route.MaxAssigneesPerStep != 1 ||
		route.DeferredSteps != "deny" || route.DeferredConfigurer != "previous_step_approver" || route.RevalidateOnActivation != "fail" {
		t.Fatalf("route defaults=%#v ok=%v", route, ok)
	}
	for _, test := range []struct {
		name  string
		route definitionmodel.WorkflowApprovalRouteContract
		valid bool
	}{
		{"complete", definitionmodel.WorkflowApprovalRouteContract{Source: "instance", MinSteps: 1, MaxSteps: 3, MaxAssigneesPerStep: 2, EligibleRoles: []string{"approver"}, DeferredSteps: "allow", DeferredConfigurer: "initiator", RevalidateOnActivation: "skip_invalid"}, true},
		{"foreign source", definitionmodel.WorkflowApprovalRouteContract{Source: "template", MinSteps: 1, MaxSteps: 1, MaxAssigneesPerStep: 1}, false},
		{"inverted step range", definitionmodel.WorkflowApprovalRouteContract{Source: "instance", MinSteps: 3, MaxSteps: 2, MaxAssigneesPerStep: 1}, false},
		{"zero assignees per step", definitionmodel.WorkflowApprovalRouteContract{Source: "instance", MinSteps: 1, MaxSteps: 1}, false},
		{"blank eligible role", definitionmodel.WorkflowApprovalRouteContract{Source: "instance", MinSteps: 1, MaxSteps: 1, MaxAssigneesPerStep: 1, EligibleRoles: []string{" "}}, false},
		{"unknown deferred policy", definitionmodel.WorkflowApprovalRouteContract{Source: "instance", MinSteps: 1, MaxSteps: 1, MaxAssigneesPerStep: 1, DeferredSteps: "maybe"}, false},
		{"unknown configurer", definitionmodel.WorkflowApprovalRouteContract{Source: "instance", MinSteps: 1, MaxSteps: 1, MaxAssigneesPerStep: 1, DeferredConfigurer: "anyone"}, false},
		{"unknown revalidation", definitionmodel.WorkflowApprovalRouteContract{Source: "instance", MinSteps: 1, MaxSteps: 1, MaxAssigneesPerStep: 1, RevalidateOnActivation: "ignore"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := WorkflowValidateApprovalRoute("approval", &test.route)
			if (err == nil) != test.valid {
				t.Fatalf("validation=%v want valid=%v", err, test.valid)
			}
			if err != nil && apperror.CodeOf(err) != "backend.workflow.route_contract_invalid" {
				t.Fatalf("code=%q", apperror.CodeOf(err))
			}
		})
	}
}

func TestWorkflowValidateGraphAcceptsRouteWithoutResolversAndRejectsBoth(t *testing.T) {
	graph := func(node definitionmodel.WorkflowGraphNode) *definitionmodel.WorkflowGraphSchema {
		return &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{
			{ID: "trigger", Type: "trigger", Name: "Start"}, node,
		}, Edges: []definitionmodel.WorkflowGraphEdge{{ID: "start", Source: "trigger", Target: "approval"}}}
	}
	route := &definitionmodel.WorkflowApprovalRouteContract{Source: "instance", MinSteps: 1, MaxSteps: 3, MaxAssigneesPerStep: 2}
	if err := WorkflowValidateGraph(graph(routeApprovalNode(route))); err != nil {
		t.Fatalf("route node rejected: %v", err)
	}
	err := WorkflowValidateGraph(graph(routeApprovalNode(route, definitionmodel.WorkflowAssigneeResolver{Type: "initiator_manager"})))
	if apperror.CodeOf(err) != "backend.workflow.approval_resolver_invalid" {
		t.Fatalf("route plus resolvers=%v", err)
	}
	err = WorkflowValidateGraph(graph(routeApprovalNode(nil)))
	if apperror.CodeOf(err) != "backend.workflow.approval_resolver_required" {
		t.Fatalf("no electorate=%v", err)
	}
}

func TestWorkflowRouteStepThresholdAndOrdering(t *testing.T) {
	assignees := []workflowmodel.WorkflowRouteAssignee{{UserID: "a"}, {UserID: "b"}, {UserID: " "}}
	for _, test := range []struct {
		mode     string
		required int
		want     int
	}{{"", 0, 1}, {"any", 0, 1}, {"all", 0, 2}, {"quorum", 2, 2}, {"quorum", 9, 2}, {"quorum", 0, 2}} {
		step := workflowmodel.WorkflowRouteStep{Mode: test.mode, RequiredApprovals: test.required, AssigneeSnapshot: assignees[:2]}
		if got := WorkflowRouteStepThreshold(step); got != test.want {
			t.Fatalf("mode=%q required=%d threshold=%d want=%d", test.mode, test.required, got, test.want)
		}
	}
	if ids := WorkflowRouteStepAssigneeIDs(workflowmodel.WorkflowRouteStep{AssigneeSnapshot: assignees}); len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("assignee ids=%#v", ids)
	}
	steps := []workflowmodel.WorkflowRouteStep{{StepNo: 3}, {StepNo: 1}, {StepNo: 2}}
	if next, ok := WorkflowRouteStepAfter(steps, 1); !ok || next.StepNo != 2 {
		t.Fatalf("next after 1=%#v ok=%v", next, ok)
	}
	if _, ok := WorkflowRouteStepAfter(steps, 3); ok {
		t.Fatal("a step after the last one was reported")
	}
}
