package policy

import (
	"reflect"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestWorkflowApprovalTimingResolverAndAssigneeHelpers(t *testing.T) {
	created := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	if got := WorkflowApprovalDueAt(definitionmodel.WorkflowApprovalNodeContract{DueSeconds: 60}, definitionmodel.WorkflowGraphNode{}, created); got != "2026-07-19T12:01:00Z" {
		t.Fatalf("relative due = %q", got)
	}
	if got := WorkflowApprovalDueAt(definitionmodel.WorkflowApprovalNodeContract{}, definitionmodel.WorkflowGraphNode{Config: map[string]any{"due_at": " fixed "}}, created); got != "fixed" {
		t.Fatalf("fixed due = %q", got)
	}
	if got := WorkflowApprovalDueAt(definitionmodel.WorkflowApprovalNodeContract{}, definitionmodel.WorkflowGraphNode{}, created); got != "" {
		t.Fatalf("missing due = %q", got)
	}
	resolvers := []definitionmodel.WorkflowAssigneeResolver{{Type: " users ", Priority: 2}, {Type: "role", Priority: 0}, {Type: "users", Priority: 1}}
	if got := WorkflowCandidateSource(resolvers); got != "users,role" {
		t.Fatalf("candidate source = %q", got)
	}
	ordered := WorkflowOrderedApprovalResolvers(resolvers)
	if ordered[0].Priority != 1 || ordered[1].Priority != 2 || ordered[2].Priority != 0 || resolvers[0].Priority != 2 {
		t.Fatalf("ordered resolvers = %#v", ordered)
	}
	if got := WorkflowApprovalSubjectUserID(map[string]any{"employee": "user-1", "owner": "user-2"}, "employee"); got != "user-1" {
		t.Fatalf("employee subject = %q", got)
	}
	if got := WorkflowApprovalSubjectUserID(map[string]any{"owner": "user-2"}, "employee"); got != "user-2" {
		t.Fatalf("owner subject = %q", got)
	}
	if got := WorkflowApprovalSubjectUserID(map[string]any{"initiating_user_id": "user-3"}, ""); got != "user-3" {
		t.Fatalf("initiator subject = %q", got)
	}
	if WorkflowApprovalSubjectUserID(nil, "employee") != "" {
		t.Fatal("missing subject resolved")
	}
	if got := WorkflowUniqueAssignees([]string{" a ", "", "a", "b"}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("unique assignees = %#v", got)
	}
}

func TestWorkflowTerminalApprovalOutcomeMatrix(t *testing.T) {
	approval := func(id, mode string) definitionmodel.WorkflowGraphNode {
		return definitionmodel.WorkflowGraphNode{ID: id, Contract: &definitionmodel.WorkflowNodeContract{Approval: &definitionmodel.WorkflowApprovalNodeContract{Mode: mode}}}
	}
	process := workflowmodel.WorkflowProcessInstance{DefinitionSnapshot: definitionmodel.WorkflowSchema{Graph: &definitionmodel.WorkflowGraphSchema{Nodes: []definitionmodel.WorkflowGraphNode{approval("any", ""), approval("all", "all")}}}}
	if outcome, terminal := WorkflowTerminalApprovalOutcome(process, nil, workflowmodel.WorkflowTask{Decision: "rejected"}); outcome != "rejected" || !terminal {
		t.Fatalf("rejected = %q/%v", outcome, terminal)
	}
	if outcome, terminal := WorkflowTerminalApprovalOutcome(process, nil, workflowmodel.WorkflowTask{Decision: "returned"}); outcome != "returned" || !terminal {
		t.Fatalf("returned = %q/%v", outcome, terminal)
	}
	if outcome, terminal := WorkflowTerminalApprovalOutcome(process, nil, workflowmodel.WorkflowTask{ID: "missing", NodeID: "any"}); outcome != "" || terminal {
		t.Fatalf("no tasks = %q/%v", outcome, terminal)
	}
	tasks := []workflowmodel.WorkflowTask{{ID: "one", NodeID: "any", Status: "pending"}, {ID: "two", NodeID: "any", Status: "pending"}, {ID: "other", NodeID: "other", Status: "approved"}}
	if outcome, terminal := WorkflowTerminalApprovalOutcome(process, tasks, workflowmodel.WorkflowTask{ID: "one", NodeID: "any", Status: "approved"}); outcome != "approved" || !terminal {
		t.Fatalf("any approved = %q/%v", outcome, terminal)
	}
	if _, terminal := WorkflowTerminalApprovalOutcome(process, tasks, workflowmodel.WorkflowTask{ID: "one", NodeID: "any", Status: "pending"}); terminal {
		t.Fatal("any pending became terminal")
	}
	allTasks := []workflowmodel.WorkflowTask{{ID: "one", NodeID: "all", Status: "approved"}, {ID: "two", NodeID: "all", Status: "pending"}}
	if _, terminal := WorkflowTerminalApprovalOutcome(process, allTasks, workflowmodel.WorkflowTask{ID: "two", NodeID: "all", Status: "pending"}); terminal {
		t.Fatal("partial all became terminal")
	}
	if outcome, terminal := WorkflowTerminalApprovalOutcome(process, allTasks, workflowmodel.WorkflowTask{ID: "two", NodeID: "all", Status: "approved"}); outcome != "approved" || !terminal {
		t.Fatalf("all approved = %q/%v", outcome, terminal)
	}
}
