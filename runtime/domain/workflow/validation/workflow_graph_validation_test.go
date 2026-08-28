package validation

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestValidateWorkflowGraphAcceptsApprovalFlowAndRejectsCycles(t *testing.T) {
	graph := &definitionmodel.WorkflowGraphSchema{
		Version: 2,
		Nodes: []definitionmodel.WorkflowGraphNode{
			{ID: "trigger", Type: "trigger", Name: "Leave requested"},
			{ID: "approval", Type: "approval", Name: "Manager approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{Mode: "any", EmptyAssigneePolicy: "fail", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"manager"}}}}}},
			{ID: "cc", Type: "cc", Name: "HR copy", Contract: &definitionmodel.WorkflowNodeContract{CC: &definitionmodel.WorkflowCCNodeContract{NotificationActionKey: "notify.hr", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"hr"}}}}}},
			{ID: "rejected", Type: "condition", Name: "Request rejected", Contract: &definitionmodel.WorkflowNodeContract{Condition: &definitionmodel.WorkflowConditionContract{Type: "always"}}},
		},
		Edges: []definitionmodel.WorkflowGraphEdge{
			{ID: "trigger-approval", Source: "trigger", Target: "approval"},
			{ID: "approval-cc", Source: "approval", Target: "cc", Branch: "approved"},
			{ID: "approval-rejected", Source: "approval", Target: "rejected", Branch: "rejected"},
		},
	}
	if err := WorkflowValidateGraph(graph); err != nil {
		t.Fatalf("expected approval flow to validate: %v", err)
	}
	graph.Edges = append(graph.Edges, definitionmodel.WorkflowGraphEdge{ID: "cc-trigger", Source: "cc", Target: "trigger"})
	if err := WorkflowValidateGraph(graph); err == nil {
		t.Fatal("expected workflow graph cycle to be rejected")
	}
}
