package validation

import (
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestWorkflowValidationIssueMappings(t *testing.T) {
	appErr := &apperror.AppError{Kind: apperror.KindBadRequest, Code: "owner", Params: map[string]string{"node": " node-1 ", "edge": " edge-1 ", "extra": "x"}}
	issue := WorkflowValidationIssueFromError(appErr, "backend.workflow.edge_invalid", "diag")
	if issue.NodeID != "node-1" || issue.EdgeID != "edge-1" || issue.FieldPath != "graph.edges[edge-1]" || issue.CapabilityKey != "workflow.graph_edge" || issue.Diagnostic != "diag" {
		t.Fatalf("%#v", issue)
	}
	issue = WorkflowValidationIssueFromError(errors.New("plain"), "backend.workflow.trigger_invalid", "plain")
	if issue.FieldPath != "trigger_contract" || issue.Params != nil || issue.CapabilityKey != "workflow.trigger_contract" {
		t.Fatalf("%#v", issue)
	}
	ref := WorkflowReferenceValidationIssue("backend.workflow.action_key_missing", "node", "action.create")
	if ref.FieldPath != "graph.nodes[node].contract.action.action_key" || ref.Params["reference"] != "action.create" || ref.CapabilityKey != "workflow.node.action" {
		t.Fatalf("%#v", ref)
	}
}

func TestWorkflowValidationFieldPathMatrix(t *testing.T) {
	for _, tc := range []struct{ code, node, want string }{
		{"condition_contract_invalid", "", "condition_contract"}, {"other", "", "graph"},
		{"resolver_invalid", "n", "graph.nodes[n].contract.approval.resolvers"},
		{"approval_mode_invalid", "n", "graph.nodes[n].contract.approval.mode"},
		{"empty_policy_invalid", "n", "graph.nodes[n].contract.approval.empty_assignee_policy"},
		{"deadline_invalid", "n", "graph.nodes[n].contract.approval.due_seconds"},
		{"action_key_invalid", "n", "graph.nodes[n].contract.action.action_key"},
		{"error_policy_invalid", "n", "graph.nodes[n].contract.action.on_error"},
		{"error_branch_invalid", "n", "graph.nodes[n].contract.action.on_error"},
		{"condition_invalid", "n", "graph.nodes[n].contract.condition"},
		{"branch_invalid", "n", "graph.nodes[n].outgoing_edges"},
		{"other", "n", "graph.nodes[n]"},
	} {
		if got := workflowValidationFieldPath(tc.code, tc.node, ""); got != tc.want {
			t.Fatalf("%s => %s", tc.code, got)
		}
	}
}

func TestWorkflowCapabilityMatrixAndConditionContract(t *testing.T) {
	for code, want := range map[string]string{
		"resolver_invalid": "workflow.assignee_resolver", "assignee_invalid": "workflow.assignee_resolver",
		"approval_invalid": "workflow.node.approval", "condition_invalid": "workflow.condition_contract",
		"action_invalid": "workflow.node.action", "edge_invalid": "workflow.graph_edge", "branch_invalid": "workflow.graph_edge",
		"trigger_invalid": "workflow.trigger_contract", "other": "workflow.graph_v2",
	} {
		if got := workflowValidationCapabilityKey(code); got != want {
			t.Fatalf("%s => %s", code, got)
		}
	}
	if !WorkflowConditionContractIsValid(definitionmodel.WorkflowConditionContract{Type: "always"}) {
		t.Fatal("always condition")
	}
	if WorkflowConditionContractIsValid(definitionmodel.WorkflowConditionContract{Type: "unknown"}) {
		t.Fatal("unknown condition")
	}
}
