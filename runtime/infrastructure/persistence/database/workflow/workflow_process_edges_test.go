package workflow

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestWorkflowProcessStoreDecisionFiltersAndMissingRows(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewWorkflowProcessStore(store)
	process := workflowmodel.WorkflowProcessInstance{
		ID: "process-edge", WorkflowKey: "expense", WorkflowName: "Expense", DefinitionVersionID: "version-2",
		DefinitionVersion: 2, DefinitionHash: "hash", DefinitionSnapshot: definitionmodel.WorkflowSchema{Key: "expense"},
		ObjectKey: "expense_claim", RecordID: "claim-1", InitiatorID: "requester", InitiatorRoleKey: "employee",
		Status: "waiting", CurrentNodeIDs: []string{"approve"}, Variables: map[string]any{}, Result: map[string]any{}, CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-02T00:00:00Z",
	}
	if err := repository.InsertProcess(t.Context(), " workspace-a ", process); err != nil {
		t.Fatal(err)
	}
	node := workflowmodel.WorkflowNodeInstance{ID: "node-edge", ProcessID: process.ID, NodeID: "approve", NodeType: "approval", Iteration: 1, Status: "waiting", Input: map[string]any{}, Output: map[string]any{}, StartedAt: "2026-01-02T00:00:00Z"}
	if err := repository.InsertNode(t.Context(), "workspace-a", node); err != nil {
		t.Fatal(err)
	}
	task := workflowmodel.WorkflowTask{ID: "task-edge", ProcessID: process.ID, NodeInstanceID: node.ID, NodeID: node.NodeID, Title: "Approve", AssigneeUserID: "manager", AssigneeName: "Manager", AssigneeRoleKey: "manager", AssigneeResolverKey: "role", AssigneeEvidence: workflowmodel.AssigneeEvidence{Matches: []workflowmodel.AssigneeEvidenceMatch{{ResolverType: "role", ResolverKey: "role", RoleKey: "manager"}}}, ResolverSnapshot: []definitionmodel.WorkflowAssigneeResolver{}, CandidateSource: "role", NodeDefinitionVersion: 2, Sequence: 1, Status: "open", DueAt: "2026-01-03T00:00:00Z", CreatedAt: "2026-01-02T00:00:00Z", UpdatedAt: "2026-01-02T00:00:00Z"}
	if err := repository.InsertTask(t.Context(), "workspace-a", task); err != nil {
		t.Fatal(err)
	}
	event := workflowmodel.WorkflowProcessEvent{ID: "event-edge", ProcessID: process.ID, Event: "created", ActorID: "system", Metadata: map[string]any{}, CreatedAt: "2026-01-02T00:00:00Z"}
	if err := repository.InsertEvent(t.Context(), "workspace-a", event); err != nil {
		t.Fatal(err)
	}

	filter := workflowmodel.WorkflowProcessFilter{Status: " waiting ", WorkflowKey: " expense ", ObjectKey: " expense_claim ", RecordID: " claim-1 ", InitiatorID: " requester ", DefinitionVersion: 2, UpdatedFrom: " 2026-01-01T00:00:00Z ", UpdatedTo: " 2026-01-03T00:00:00Z ", Limit: 501}
	if values, err := repository.ListProcesses(t.Context(), "workspace-a", filter); err != nil || len(values) != 1 || values[0].WorkspaceID != "workspace-a" {
		t.Fatalf("processes=%#v error=%v", values, err)
	}
	if values, err := repository.ListProcesses(t.Context(), "workspace-a", workflowmodel.WorkflowProcessFilter{ApproverID: "manager"}); err != nil || len(values) != 1 {
		t.Fatalf("approver-filtered processes=%#v error=%v", values, err)
	}
	if values, err := repository.ListProcesses(t.Context(), "workspace-a", workflowmodel.WorkflowProcessFilter{VisibleToUserID: "requester"}); err != nil || len(values) != 1 {
		t.Fatalf("initiator-visible processes=%#v error=%v", values, err)
	}
	if values, err := repository.ListProcesses(t.Context(), "workspace-a", workflowmodel.WorkflowProcessFilter{VisibleToUserID: "manager"}); err != nil || len(values) != 1 {
		t.Fatalf("assignee-visible processes=%#v error=%v", values, err)
	}
	if values, err := repository.ListProcesses(t.Context(), "workspace-a", workflowmodel.WorkflowProcessFilter{VisibleToUserID: "unrelated"}); err != nil || len(values) != 0 {
		t.Fatalf("hidden processes=%#v error=%v", values, err)
	}
	for _, item := range []struct{ id, status string }{{"process-failed", "failed"}, {"process-configuration", "configuration_error"}} {
		candidate := process
		candidate.ID, candidate.Status = item.id, item.status
		candidate.CreatedAt, candidate.UpdatedAt = "2026-01-03T00:00:00Z", "2026-01-03T00:00:00Z"
		if err := repository.InsertProcess(t.Context(), "workspace-a", candidate); err != nil {
			t.Fatal(err)
		}
	}
	if values, err := repository.ListProcesses(t.Context(), "workspace-a", workflowmodel.WorkflowProcessFilter{Statuses: []string{" failed ", "configuration_error"}, Limit: 10}); err != nil || len(values) != 2 {
		t.Fatalf("multi-status processes=%#v error=%v", values, err)
	}
	if values, err := repository.ListProcesses(t.Context(), "workspace-a", workflowmodel.WorkflowProcessFilter{ProcessID: " process-configuration ", Limit: 10}); err != nil || len(values) != 1 || values[0].ID != "process-configuration" {
		t.Fatalf("resource process=%#v error=%v", values, err)
	}
	if values, err := repository.ListTasks(t.Context(), "workspace-a", " ", " ", " ", 0); err != nil || len(values) != 1 || values[0].AssigneeResolverKey != "role" || len(values[0].AssigneeEvidence.Matches) != 1 || values[0].AssigneeEvidence.Matches[0].RoleKey != "manager" {
		t.Fatalf("tasks=%#v error=%v", values, err)
	}
	if values, err := repository.ListEvents(t.Context(), "workspace-a", process.ID, 1001); err != nil || len(values) != 1 {
		t.Fatalf("events=%#v error=%v", values, err)
	}
	secondTask := task
	secondTask.ID, secondTask.Sequence = "task-edge-2", 2
	if err := repository.InsertTask(t.Context(), "workspace-a", secondTask); err != nil {
		t.Fatal(err)
	}
	updatedFirst := task
	updatedFirst.AssigneeName, updatedFirst.UpdatedAt = "Updated Manager", "2026-01-02T00:30:00Z"
	updatedSecond := secondTask
	updatedSecond.Status, updatedSecond.UpdatedAt = "cancelled", "2026-01-02T00:30:00Z"
	if err := repository.UpdateTasks(t.Context(), "workspace-a", []workflowmodel.WorkflowTask{updatedFirst, updatedSecond}); err != nil {
		t.Fatal(err)
	}
	if loaded, found, err := repository.GetTask(t.Context(), "workspace-a", secondTask.ID); err != nil || !found || loaded.Status != "cancelled" {
		t.Fatalf("batch-updated task=%#v found=%v err=%v", loaded, found, err)
	}

	decided, found, err := repository.DecideTask(t.Context(), "workspace-a", task.ID, "manager", "approved", "looks good", "2026-01-02T01:00:00Z")
	if err != nil || !found || decided.Status != "approved" || decided.Decision != "approved" || decided.CompletedBy != "manager" {
		t.Fatalf("decided=%#v found=%v error=%v", decided, found, err)
	}
	if _, found, err := repository.DecideTask(t.Context(), "workspace-a", task.ID, "manager", "rejected", "late", "2026-01-02T02:00:00Z"); err != nil || found {
		t.Fatalf("second decision found=%v error=%v", found, err)
	}
	if _, found, err := repository.DecideTask(t.Context(), "workspace-a", "missing", "manager", "approved", "", "2026-01-02T02:00:00Z"); err != nil || found {
		t.Fatalf("missing decision found=%v error=%v", found, err)
	}

	missingProcess := process
	missingProcess.ID = "missing-process"
	if err := repository.UpdateProcess(t.Context(), "workspace-a", missingProcess); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing process update = %v", err)
	}
	missingNode := node
	missingNode.ID = "missing-node"
	if err := repository.UpdateNode(t.Context(), "workspace-a", missingNode); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing node update = %v", err)
	}
	missingTask := task
	missingTask.ID = "missing-task"
	if err := repository.UpdateTask(t.Context(), "workspace-a", missingTask); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing task update = %v", err)
	}
	if _, found, err := repository.GetProcess(t.Context(), "workspace-a", "missing"); err != nil || found {
		t.Fatalf("missing process found=%v error=%v", found, err)
	}
	if _, found, err := repository.GetTask(t.Context(), "workspace-a", "missing"); err != nil || found {
		t.Fatalf("missing task found=%v error=%v", found, err)
	}
}

func TestWorkflowProcessStoreRejectsBlankWorkspaceAndPropagatesCancellation(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewWorkflowProcessStore(store)
	process := workflowmodel.WorkflowProcessInstance{ID: "process"}
	node := workflowmodel.WorkflowNodeInstance{ID: "node"}
	task := workflowmodel.WorkflowTask{ID: "task"}
	event := workflowmodel.WorkflowProcessEvent{ID: "event"}
	checks := []struct {
		name string
		call func() error
	}{
		{name: "insert process", call: func() error { return repository.InsertProcess(t.Context(), " ", process) }},
		{name: "update process", call: func() error { return repository.UpdateProcess(t.Context(), " ", process) }},
		{name: "get process", call: func() error { _, _, err := repository.GetProcess(t.Context(), " ", "id"); return err }},
		{name: "list processes", call: func() error {
			_, err := repository.ListProcesses(t.Context(), " ", workflowmodel.WorkflowProcessFilter{})
			return err
		}},
		{name: "insert node", call: func() error { return repository.InsertNode(t.Context(), " ", node) }},
		{name: "update node", call: func() error { return repository.UpdateNode(t.Context(), " ", node) }},
		{name: "list nodes", call: func() error { _, err := repository.ListNodes(t.Context(), " ", "id"); return err }},
		{name: "insert task", call: func() error { return repository.InsertTask(t.Context(), " ", task) }},
		{name: "update task", call: func() error { return repository.UpdateTask(t.Context(), " ", task) }},
		{name: "get task", call: func() error { _, _, err := repository.GetTask(t.Context(), " ", "id"); return err }},
		{name: "list tasks", call: func() error { _, err := repository.ListTasks(t.Context(), " ", "", "", "", 1); return err }},
		{name: "decide task", call: func() error {
			_, _, err := repository.DecideTask(t.Context(), " ", "id", "user", "approved", "", "now")
			return err
		}},
		{name: "insert event", call: func() error { return repository.InsertEvent(t.Context(), " ", event) }},
		{name: "list events", call: func() error { _, err := repository.ListEvents(t.Context(), " ", "id", 1); return err }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.call(); err == nil {
				t.Fatal("blank workspace accepted")
			}
		})
	}

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	for name, call := range map[string]func() error{
		"update process": func() error { return repository.UpdateProcess(canceled, "workspace-a", process) },
		"list processes": func() error {
			_, err := repository.ListProcesses(canceled, "workspace-a", workflowmodel.WorkflowProcessFilter{})
			return err
		},
		"list nodes":  func() error { _, err := repository.ListNodes(canceled, "workspace-a", "id"); return err },
		"update node": func() error { return repository.UpdateNode(canceled, "workspace-a", node) },
		"list tasks":  func() error { _, err := repository.ListTasks(canceled, "workspace-a", "", "", "", 1); return err },
		"update task": func() error { return repository.UpdateTask(canceled, "workspace-a", task) },
		"decide task": func() error {
			_, _, err := repository.DecideTask(canceled, "workspace-a", "id", "user", "approved", "", "now")
			return err
		},
		"list events": func() error { _, err := repository.ListEvents(canceled, "workspace-a", "id", 1); return err },
	} {
		t.Run("canceled "+name, func(t *testing.T) {
			if err := call(); !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestWorkflowExecutionJSONAndConditionHelpers(t *testing.T) {
	valid := workflowmodel.WorkflowExecution{Action: map[string]any{"kind": "record"}, Payload: map[string]any{"id": 1}, Result: map[string]any{"ok": true}}
	columns, values, err := workflowExecutionInsertValues(valid)
	if err != nil || len(columns) != len(values) || len(columns) != len(workflowExecutionColumns()) {
		t.Fatalf("columns=%d values=%d error=%v", len(columns), len(values), err)
	}
	if values, err := workflowExecutionMutableValues(valid); err != nil || len(values) != len(workflowExecutionMutableColumns()) {
		t.Fatalf("mutable values=%d error=%v", len(values), err)
	}
	for _, invalid := range []workflowmodel.WorkflowExecution{
		{Action: map[string]any{"bad": make(chan int)}},
		{Action: map[string]any{}, Payload: map[string]any{"bad": make(chan int)}},
		{Action: map[string]any{}, Payload: map[string]any{}, Result: map[string]any{"bad": make(chan int)}},
	} {
		if _, _, err := workflowExecutionInsertValues(invalid); err == nil {
			t.Fatal("invalid execution insert JSON accepted")
		}
		if _, err := workflowExecutionMutableValues(invalid); err == nil {
			t.Fatal("invalid execution mutable JSON accepted")
		}
	}
	if value := workflowExecutionConditionValue("next_run_at", ""); value != int64(0) {
		t.Fatalf("empty next run value = %#v", value)
	}
	if value := workflowExecutionConditionValue("next_run_at", 3); value != int64(3) {
		t.Fatalf("typed next run value = %#v", value)
	}
	if value := workflowExecutionConditionValue("status", "running"); value != "running" {
		t.Fatalf("status value = %#v", value)
	}
}
