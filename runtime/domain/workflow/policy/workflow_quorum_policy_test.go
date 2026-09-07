package policy

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestWorkflowQuorumValidationAndAuthoring(t *testing.T) {
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
			fragment := map[string]any{"mode": test.mode, "required_approvals": test.count, "resolvers": []any{map[string]any{"type": "role", "role_key": "approver"}}}
			err := WorkflowValidateAuthoringFragment("workflow.node.approval", fragment)
			if (err == nil) != test.valid {
				t.Fatalf("validation=%v want valid=%v", err, test.valid)
			}
		})
	}
	for _, capability := range WorkflowAuthoringDomain().Capabilities {
		if capability.Key != "workflow.node.approval" {
			continue
		}
		if capability.InputSchema.Properties["required_approvals"].Minimum == nil || capability.InputSchema.Then == nil || capability.InputSchema.Then.Required[0] != "required_approvals" {
			t.Fatalf("quorum authoring schema=%+v", capability.InputSchema)
		}
	}
	if schema := workflowGraphApprovalSchema(); schema.Then == nil || schema.Then.Required[0] != "required_approvals" {
		t.Fatalf("graph quorum schema=%+v", schema)
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
