package policy

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestWorkflowQuorumValidation(t *testing.T) {
	for _, test := range []struct {
		name, mode string
		count      int
		valid      bool
	}{
		{"two approvals", "quorum", 2, true},
		{"one approval", "quorum", 1, true},
		{"missing threshold", "quorum", 0, false},
		{"negative threshold", "quorum", -1, false},
		{"threshold on any", "any", 2, false},
		{"threshold on all", "all", 2, false},
		{"threshold on sequential", "sequential", 2, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			approval := definitionmodel.WorkflowApprovalNodeContract{Mode: test.mode, RequiredApprovals: test.count, Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "role", RoleKey: "approver"}}}
			graph := &definitionmodel.WorkflowGraphSchema{Version: 2,
				Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, {ID: "approval", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &approval}}, {ID: "approved", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "order.approve"}}}, {ID: "rejected", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "order.reject"}}}},
				Edges: []definitionmodel.WorkflowGraphEdge{{ID: "start", Source: "trigger", Target: "approval"}, {ID: "approved", Source: "approval", Target: "approved", Branch: "approved"}, {ID: "rejected", Source: "approval", Target: "rejected", Branch: "rejected"}},
			}
			err := WorkflowValidateGraph(graph)
			if (err == nil) != test.valid {
				t.Fatalf("validation=%v want valid=%v", err, test.valid)
			}
		})
	}
}

func TestWorkflowQuorumCountsOnlyCurrentNodeInstance(t *testing.T) {
	node := definitionmodel.WorkflowGraphNode{ID: "approval", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{Mode: "quorum", RequiredApprovals: 2}}}
	process := workflowmodel.WorkflowProcessInstance{DefinitionSnapshot: definitionmodel.WorkflowSchema{Graph: &definitionmodel.WorkflowGraphSchema{Nodes: []definitionmodel.WorkflowGraphNode{node}}}}
	tasks := []workflowmodel.WorkflowTask{
		{ID: "old", NodeID: node.ID, NodeInstanceID: "previous", Status: "approved"},
		{ID: "a", NodeID: node.ID, NodeInstanceID: "current", Status: "open"},
		{ID: "b", NodeID: node.ID, NodeInstanceID: "current", Status: "open"},
		{ID: "c", NodeID: node.ID, NodeInstanceID: "current", Status: "open"},
	}
	first := tasks[1]
	first.Status, first.Decision = "approved", "approved"
	if _, complete := WorkflowTerminalApprovalOutcome(process, tasks, first); complete {
		t.Fatal("an approval from an earlier node instance counted toward quorum")
	}
	tasks[1] = first
	second := tasks[2]
	second.Status, second.Decision = "approved", "approved"
	if outcome, complete := WorkflowTerminalApprovalOutcome(process, tasks, second); outcome != "approved" || !complete {
		t.Fatalf("second approval=%q/%v", outcome, complete)
	}
	for _, decision := range []string{"rejected", "returned"} {
		second.Status, second.Decision = decision, decision
		if outcome, complete := WorkflowTerminalApprovalOutcome(process, tasks, second); outcome != decision || !complete {
			t.Fatalf("%s=%q/%v", decision, outcome, complete)
		}
	}
	for _, count := range []int{0, 1, 2, 3} {
		if err := WorkflowValidateApprovalAssigneeCount(node, count); (err == nil) != (count >= 2) {
			t.Fatalf("assignee count %d error=%v", count, err)
		}
	}
}
