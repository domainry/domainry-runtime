package policy

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func workflowTestAction(id string) definitionmodel.WorkflowGraphNode {
	return definitionmodel.WorkflowGraphNode{ID: id, Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "record.update", OnError: "fail"}}}
}

func workflowTestBaseGraph() *definitionmodel.WorkflowGraphSchema {
	return &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, workflowTestAction("action")}, Edges: []definitionmodel.WorkflowGraphEdge{{ID: "edge", Source: "trigger", Target: "action"}}}
}

func workflowGraphCode(err error) string {
	if err == nil {
		return ""
	}
	return apperror.CodeOf(err)
}

func TestWorkflowValidateGraphNodeContractsAndStructuralFailures(t *testing.T) {
	approvalResolver := definitionmodel.WorkflowAssigneeResolver{Type: "users", UserIDs: []string{"user-1"}}
	approval := func(contract *definitionmodel.WorkflowApprovalNodeContract) definitionmodel.WorkflowGraphNode {
		return definitionmodel.WorkflowGraphNode{ID: "approval", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: contract}}
	}
	condition := func(contract *definitionmodel.WorkflowConditionContract) definitionmodel.WorkflowGraphNode {
		return definitionmodel.WorkflowGraphNode{ID: "condition", Type: "condition", Contract: &definitionmodel.WorkflowNodeContract{Condition: contract}}
	}
	cc := func(contract *definitionmodel.WorkflowCCNodeContract) definitionmodel.WorkflowGraphNode {
		return definitionmodel.WorkflowGraphNode{ID: "cc", Type: "cc", Contract: &definitionmodel.WorkflowNodeContract{CC: contract}}
	}
	tests := []struct {
		name  string
		graph *definitionmodel.WorkflowGraphSchema
		code  string
	}{
		{name: "nil", code: "backend.workflow.graph_v2_required"},
		{name: "version", graph: &definitionmodel.WorkflowGraphSchema{Version: 1}, code: "backend.workflow.graph_invalid"},
		{name: "empty graph", graph: &definitionmodel.WorkflowGraphSchema{Version: 2}, code: "backend.workflow.graph_invalid"},
		{name: "empty node id", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{Type: "trigger"}}}, code: "backend.workflow.graph_node_invalid"},
		{name: "unknown node type", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "x", Type: "unknown"}}}, code: "backend.workflow.graph_node_invalid"},
		{name: "duplicate node", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, {ID: "trigger", Type: "action"}}}, code: "backend.workflow.graph_node_invalid"},
		{name: "duplicate trigger", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "one", Type: "trigger"}, {ID: "two", Type: "trigger"}}}, code: "backend.workflow.graph_trigger_required"},
		{name: "approval mode", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, approval(&definitionmodel.WorkflowApprovalNodeContract{Mode: "invalid"})}}, code: "backend.workflow.graph_approval_mode_invalid"},
		{name: "approval missing contract", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, {ID: "approval", Type: "approval"}}}, code: "backend.workflow.approval_resolver_required"},
		{name: "approval nil contract value", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, {ID: "approval", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{}}}}, code: "backend.workflow.approval_resolver_required"},
		{name: "approval missing resolver", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, approval(&definitionmodel.WorkflowApprovalNodeContract{Mode: "any"})}}, code: "backend.workflow.approval_resolver_required"},
		{name: "approval invalid resolver", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, approval(&definitionmodel.WorkflowApprovalNodeContract{Mode: "any", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "users"}}})}}, code: "backend.workflow.approval_resolver_invalid"},
		{name: "approval empty policy", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, approval(&definitionmodel.WorkflowApprovalNodeContract{Mode: "any", Resolvers: []definitionmodel.WorkflowAssigneeResolver{approvalResolver}, EmptyAssigneePolicy: "invalid"})}}, code: "backend.workflow.approval_empty_policy_invalid"},
		{name: "condition missing", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, {ID: "condition", Type: "condition"}}}, code: "backend.workflow.condition_contract_invalid"},
		{name: "condition nil contract value", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, {ID: "condition", Type: "condition", Contract: &definitionmodel.WorkflowNodeContract{}}}}, code: "backend.workflow.condition_contract_invalid"},
		{name: "condition invalid", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, condition(&definitionmodel.WorkflowConditionContract{Type: "field_equals"})}}, code: "backend.workflow.condition_contract_invalid"},
		{name: "action missing", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, {ID: "action", Type: "action"}}}, code: "backend.workflow.action_key_required"},
		{name: "action nil contract value", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, {ID: "action", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{}}}}, code: "backend.workflow.action_key_required"},
		{name: "action empty key", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, {ID: "action", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{}}}}}, code: "backend.workflow.action_key_required"},
		{name: "action timeout", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, {ID: "action", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "x", TimeoutSeconds: -1}}}}}, code: "backend.workflow.action_execution_policy_invalid"},
		{name: "action retry", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, {ID: "action", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "x", Retry: &definitionmodel.WorkflowRetryPolicy{MaxAttempts: 0}}}}}}, code: "backend.workflow.action_execution_policy_invalid"},
		{name: "action on error", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, {ID: "action", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "x", OnError: "invalid"}}}}}, code: "backend.workflow.action_error_policy_invalid"},
		{name: "cc missing", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, {ID: "cc", Type: "cc"}}}, code: "backend.workflow.cc_contract_required"},
		{name: "cc nil contract value", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, {ID: "cc", Type: "cc", Contract: &definitionmodel.WorkflowNodeContract{}}}}, code: "backend.workflow.cc_contract_required"},
		{name: "cc missing action", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, cc(&definitionmodel.WorkflowCCNodeContract{Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "initiator_manager"}}})}}, code: "backend.workflow.cc_contract_required"},
		{name: "cc incomplete", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, cc(&definitionmodel.WorkflowCCNodeContract{NotificationActionKey: "notify"})}}, code: "backend.workflow.cc_contract_required"},
		{name: "no trigger", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{workflowTestAction("action")}}, code: "backend.workflow.graph_trigger_required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := workflowGraphCode(WorkflowValidateGraph(test.graph)); got != test.code {
				t.Fatalf("code = %q, want %q", got, test.code)
			}
		})
	}
}

func TestWorkflowValidateGraphEdgesBranchesCyclesAndConnectivity(t *testing.T) {
	validApproval := definitionmodel.WorkflowGraphNode{ID: "approval", Type: "approval", Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{Mode: "any", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "initiator_manager"}}}}}
	validCondition := definitionmodel.WorkflowGraphNode{ID: "condition", Type: "condition", Contract: &definitionmodel.WorkflowNodeContract{Condition: &definitionmodel.WorkflowConditionContract{Type: "always"}}}
	graphs := []struct {
		name  string
		graph *definitionmodel.WorkflowGraphSchema
		code  string
	}{
		{name: "valid", graph: workflowTestBaseGraph()},
		{name: "valid cc", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, {ID: "cc", Type: "cc", Contract: &definitionmodel.WorkflowNodeContract{CC: &definitionmodel.WorkflowCCNodeContract{NotificationActionKey: "notify", Resolvers: []definitionmodel.WorkflowAssigneeResolver{{Type: "initiator_manager"}}}}}}, Edges: []definitionmodel.WorkflowGraphEdge{{Source: "trigger", Target: "cc"}}}},
		{name: "valid retry action", graph: &definitionmodel.WorkflowGraphSchema{
			Version: 2,
			Nodes: []definitionmodel.WorkflowGraphNode{
				{ID: "trigger", Type: "trigger"},
				{ID: "action", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "x", Retry: &definitionmodel.WorkflowRetryPolicy{MaxAttempts: 1}}}},
			},
			Edges: workflowTestBaseGraph().Edges,
		}},
		{name: "valid converging DAG", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, workflowTestAction("one"), workflowTestAction("two"), workflowTestAction("join")}, Edges: []definitionmodel.WorkflowGraphEdge{{Source: "trigger", Target: "one"}, {Source: "trigger", Target: "two"}, {Source: "one", Target: "join"}, {Source: "two", Target: "join"}}}},
		{name: "missing edge node", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: workflowTestBaseGraph().Nodes, Edges: []definitionmodel.WorkflowGraphEdge{{ID: "bad", Source: "trigger", Target: "missing"}}}, code: "backend.workflow.graph_edge_invalid"},
		{name: "missing edge source", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: workflowTestBaseGraph().Nodes, Edges: []definitionmodel.WorkflowGraphEdge{{ID: "bad", Source: "missing", Target: "action"}}}, code: "backend.workflow.graph_edge_invalid"},
		{name: "self edge", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: workflowTestBaseGraph().Nodes, Edges: []definitionmodel.WorkflowGraphEdge{{ID: "bad", Source: "trigger", Target: "trigger"}}}, code: "backend.workflow.graph_edge_invalid"},
		{name: "duplicate branch", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, workflowTestAction("one"), workflowTestAction("two")}, Edges: []definitionmodel.WorkflowGraphEdge{{Source: "trigger", Target: "one", Branch: "success"}, {Source: "trigger", Target: "two", Label: "SUCCESS"}}}, code: "backend.workflow.graph_branch_duplicate"},
		{name: "condition empty branch", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, validCondition, workflowTestAction("action")}, Edges: []definitionmodel.WorkflowGraphEdge{{Source: "trigger", Target: "condition"}, {Source: "condition", Target: "action"}}}, code: "backend.workflow.condition_branch_invalid"},
		{name: "condition unsupported", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, validCondition, workflowTestAction("action")}, Edges: []definitionmodel.WorkflowGraphEdge{{Source: "trigger", Target: "condition"}, {Source: "condition", Target: "action", Branch: "maybe"}}}, code: "backend.workflow.condition_branch_invalid"},
		{name: "valid condition branches", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, validCondition, workflowTestAction("yes"), workflowTestAction("no")}, Edges: []definitionmodel.WorkflowGraphEdge{{Source: "trigger", Target: "condition"}, {Source: "condition", Target: "yes", Branch: "true"}, {Source: "condition", Target: "no", Branch: "false"}}}},
		{name: "approval empty branch", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, validApproval, workflowTestAction("action")}, Edges: []definitionmodel.WorkflowGraphEdge{{Source: "trigger", Target: "approval"}, {Source: "approval", Target: "action"}}}, code: "backend.workflow.approval_branch_invalid"},
		{name: "approval unsupported", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, validApproval, workflowTestAction("action")}, Edges: []definitionmodel.WorkflowGraphEdge{{Source: "trigger", Target: "approval"}, {Source: "approval", Target: "action", Branch: "maybe"}}}, code: "backend.workflow.approval_branch_invalid"},
		{name: "approval outcomes", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, validApproval, workflowTestAction("action")}, Edges: []definitionmodel.WorkflowGraphEdge{{Source: "trigger", Target: "approval"}, {Source: "approval", Target: "action", Branch: "approved"}}}, code: "backend.workflow.approval_outcomes_required"},
		{name: "approval rejected only", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, validApproval, workflowTestAction("action")}, Edges: []definitionmodel.WorkflowGraphEdge{{Source: "trigger", Target: "approval"}, {Source: "approval", Target: "action", Branch: "rejected"}}}, code: "backend.workflow.approval_outcomes_required"},
		{name: "valid approval outcomes", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, validApproval, workflowTestAction("yes"), workflowTestAction("no")}, Edges: []definitionmodel.WorkflowGraphEdge{{Source: "trigger", Target: "approval"}, {Source: "approval", Target: "yes", Branch: "approved"}, {Source: "approval", Target: "no", Branch: "rejected"}}}},
		{name: "action error branch", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, {ID: "action", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "x", OnError: "error_branch"}}}, workflowTestAction("next")}, Edges: []definitionmodel.WorkflowGraphEdge{{Source: "trigger", Target: "action"}, {Source: "action", Target: "next", Branch: "success"}}}, code: "backend.workflow.action_error_branch_required"},
		{name: "valid action error branch", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{{ID: "trigger", Type: "trigger"}, {ID: "action", Type: "action", Contract: &definitionmodel.WorkflowNodeContract{Action: &definitionmodel.WorkflowBusinessActionNodeContract{ActionKey: "x", OnError: "error_branch"}}}, workflowTestAction("next")}, Edges: []definitionmodel.WorkflowGraphEdge{{Source: "trigger", Target: "action"}, {Source: "action", Target: "next", Branch: "error"}}}},
		{name: "cycle", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: workflowTestBaseGraph().Nodes, Edges: []definitionmodel.WorkflowGraphEdge{{Source: "trigger", Target: "action"}, {Source: "action", Target: "trigger"}}}, code: "backend.workflow.graph_cycle"},
		{name: "disconnected", graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: append(workflowTestBaseGraph().Nodes, workflowTestAction("other")), Edges: workflowTestBaseGraph().Edges}, code: "backend.workflow.graph_disconnected"},
	}
	for _, test := range graphs {
		t.Run(test.name, func(t *testing.T) {
			if got := workflowGraphCode(WorkflowValidateGraph(test.graph)); got != test.code {
				t.Fatalf("code = %q, want %q", got, test.code)
			}
		})
	}
}

func TestWorkflowGraphRuleHelpersCompleteMatrix(t *testing.T) {
	for _, resolver := range []definitionmodel.WorkflowAssigneeResolver{{Type: "users", UserIDs: []string{"", " user "}}, {Type: "record_field", Field: "owner"}, {Type: "manager", UserField: "employee"}, {Type: "manager_of", UserField: "employee"}, {Type: "initiator_manager"}, {Type: "role", RoleKey: "admin"}} {
		if !validWorkflowAssigneeResolver(resolver) {
			t.Fatalf("valid resolver rejected: %#v", resolver)
		}
	}
	for _, resolver := range []definitionmodel.WorkflowAssigneeResolver{{Type: "users"}, {Type: "record_field"}, {Type: "manager"}, {Type: "role"}, {Type: "unknown"}} {
		if validWorkflowAssigneeResolver(resolver) {
			t.Fatalf("invalid resolver accepted: %#v", resolver)
		}
	}
	if valueOrDefault(" value ", "fallback") != "value" || valueOrDefault("", "fallback") != "fallback" {
		t.Fatal("value fallback mismatch")
	}
	if !hasUnsupportedWorkflowBranch(map[string]bool{"true": true, "maybe": true}, "true", "false") || hasUnsupportedWorkflowBranch(map[string]bool{"": true, "true": true}, "true") {
		t.Fatal("unsupported branch classification mismatch")
	}
	validChild := definitionmodel.WorkflowConditionContract{Type: "always"}
	for _, contract := range []definitionmodel.WorkflowConditionContract{{}, {Type: "field_equals", Field: "status"}, {Type: "field_changed", Field: "status"}, {Type: "expression", Expression: "x"}, {Type: "all", Conditions: []definitionmodel.WorkflowConditionContract{validChild}}, {Type: "and", Conditions: []definitionmodel.WorkflowConditionContract{validChild}}, {Type: "any", Conditions: []definitionmodel.WorkflowConditionContract{validChild}}, {Type: "or", Conditions: []definitionmodel.WorkflowConditionContract{validChild}}, {Type: "not", Condition: &validChild}} {
		if !WorkflowConditionContractIsValid(contract) {
			t.Fatalf("valid condition rejected: %#v", contract)
		}
	}
	for _, contract := range []definitionmodel.WorkflowConditionContract{{Type: "field_equals"}, {Type: "expression"}, {Type: "all"}, {Type: "all", Conditions: []definitionmodel.WorkflowConditionContract{{Type: "invalid"}}}, {Type: "not"}, {Type: "invalid"}} {
		if WorkflowConditionContractIsValid(contract) {
			t.Fatalf("invalid condition accepted: %#v", contract)
		}
	}
	if workflowApprovalNodeContract(definitionmodel.WorkflowGraphNode{}).Mode != "" || workflowBusinessActionNodeContract(definitionmodel.WorkflowGraphNode{}).ActionKey != "" || workflowCCNodeContract(definitionmodel.WorkflowGraphNode{}).NotificationActionKey != "" {
		t.Fatal("missing node contracts did not return zero values")
	}
	emptyContract := definitionmodel.WorkflowGraphNode{Contract: &definitionmodel.WorkflowNodeContract{}}
	if workflowApprovalNodeContract(emptyContract).Mode != "" || workflowBusinessActionNodeContract(emptyContract).ActionKey != "" || workflowCCNodeContract(emptyContract).NotificationActionKey != "" {
		t.Fatal("nil typed node contracts did not return zero values")
	}
	if err := badRequest("code", "", "ignored", "node", "one", "orphan"); apperror.CodeOf(err) != "code" {
		t.Fatalf("bad request = %v", err)
	}
}
