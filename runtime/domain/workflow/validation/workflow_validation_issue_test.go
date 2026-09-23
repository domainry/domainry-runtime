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
	if issue.NodeID != "node-1" || issue.EdgeID != "edge-1" || issue.FieldPath != "graph.edges[edge-1]" || issue.Diagnostic != "diag" {
		t.Fatalf("%#v", issue)
	}
	issue = WorkflowValidationIssueFromError(errors.New("plain"), "backend.workflow.trigger_invalid", "plain")
	if issue.FieldPath != "trigger_contract" || issue.Params != nil {
		t.Fatalf("%#v", issue)
	}
	ref := WorkflowReferenceValidationIssue("backend.workflow.action_key_missing", "node", "action.create")
	if ref.FieldPath != "graph.nodes[node].contract.action.action_key" || ref.Params["reference"] != "action.create" {
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

func TestWorkflowConditionContract(t *testing.T) {
	if !WorkflowConditionContractIsValid(definitionmodel.WorkflowConditionContract{Type: "always"}) {
		t.Fatal("always condition")
	}
	if WorkflowConditionContractIsValid(definitionmodel.WorkflowConditionContract{Type: "unknown"}) {
		t.Fatal("unknown condition")
	}
}
