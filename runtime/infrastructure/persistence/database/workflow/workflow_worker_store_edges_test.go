package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func openWorkflowWorkerEdgeStore(t *testing.T) WorkflowWorkerStore {
	t.Helper()
	store := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	return NewWorkflowWorkerStore(store)
}

func TestWorkflowContinuationRecoveryDiscoversRegisteredWorkspaces(t *testing.T) {
	repository := openWorkflowWorkerEdgeStore(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, workspaceID := range []string{"workspace-a", "workspace-b"} {
		execution := workflowWorkerEdgeExecution("execution-" + workspaceID)
		execution.WorkspaceID, execution.Status, execution.UpdatedAt = workspaceID, "pending", now
		if err := repository.InsertExecution(t.Context(), workspaceID, execution); err != nil {
			t.Fatal(err)
		}
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "recover workflow continuations")
	workspaces, err := repository.ListWorkflowContinuationWorkspaces(t.Context(), scope, 10)
	if err != nil || len(workspaces) != 2 || workspaces[0] != "workspace-a" || workspaces[1] != "workspace-b" {
		t.Fatalf("workspaces=%#v err=%v", workspaces, err)
	}
	if _, err := repository.ListWorkflowContinuationWorkspaces(t.Context(), principalmodel.SystemScope{}, 10); err == nil {
		t.Fatal("unscoped workflow continuation recovery was accepted")
	}
}

func workflowWorkerEdgeExecution(id string) workflowmodel.WorkflowExecution {
	return workflowmodel.WorkflowExecution{
		ID: id, OperationID: "operation-1", WorkflowKey: "approval", Name: "Approval", Trigger: "manual", Status: "pending", ActionType: "record",
		Action: map[string]any{"kind": "record"}, Payload: map[string]any{"id": id}, Result: map[string]any{},
		ActorID: "user", Attempt: 0, MaxAttempts: 3, CreatedAt: "2026-07-20T00:00:00Z", UpdatedAt: "2026-07-20T00:00:00Z",
	}
}

func TestWorkflowWorkerExecutionCRUDAndBoundaryEdges(t *testing.T) {
	repository := openWorkflowWorkerEdgeStore(t)
	execution := workflowWorkerEdgeExecution("execution-edge")
	if err := repository.InsertExecution(t.Context(), " ", execution); err == nil {
		t.Fatal("blank workspace accepted")
	}
	invalid := execution
	invalid.Action = map[string]any{"invalid": make(chan int)}
	if err := repository.InsertExecution(t.Context(), "workspace-a", invalid); err == nil {
		t.Fatal("invalid execution JSON accepted")
	}
	if err := repository.InsertExecution(t.Context(), " workspace-a ", execution); err != nil {
		t.Fatal(err)
	}
	if got, found, err := repository.GetExecution(t.Context(), "workspace-a", execution.ID); err != nil || !found || got.WorkspaceID != "workspace-a" || got.OperationID != "operation-1" {
		t.Fatalf("execution=%+v found=%v error=%v", got, found, err)
	}
	if _, found, err := repository.GetExecution(t.Context(), "workspace-a", "missing"); err != nil || found {
		t.Fatalf("missing found=%v error=%v", found, err)
	}
	if _, _, err := repository.GetExecution(t.Context(), " ", execution.ID); err == nil {
		t.Fatal("blank get workspace accepted")
	}
	for _, limit := range []int{0, 1001} {
		if values, err := repository.ListExecutions(t.Context(), "workspace-a", limit); err != nil || len(values) != 1 {
			t.Fatalf("limit=%d values=%v error=%v", limit, values, err)
		}
	}
	if _, err := repository.ListExecutions(t.Context(), " ", 1); err == nil {
		t.Fatal("blank list workspace accepted")
	}
	execution.Status = "running"
	if err := repository.UpdateExecution(t.Context(), "workspace-a", execution); err != nil {
		t.Fatal(err)
	}
	if err := repository.UpdateExecution(t.Context(), " ", execution); err == nil {
		t.Fatal("blank update workspace accepted")
	}
	invalid = execution
	invalid.Result = map[string]any{"invalid": make(chan int)}
	if err := repository.UpdateExecution(t.Context(), "workspace-a", invalid); err == nil {
		t.Fatal("invalid update JSON accepted")
	}
	missing := execution
	missing.ID = "missing"
	if err := repository.UpdateExecution(t.Context(), "workspace-a", missing); err == nil {
		t.Fatal("missing execution updated")
	}
	if won, err := repository.UpdateExecutionWhere(t.Context(), "workspace-a", execution, map[string]any{"status": "running"}); err != nil || !won {
		t.Fatalf("conditional update won=%v error=%v", won, err)
	}
	if _, err := repository.UpdateExecutionWhere(t.Context(), " ", execution, nil); err == nil {
		t.Fatal("blank conditional workspace accepted")
	}
	if _, err := repository.UpdateExecutionWhere(t.Context(), "workspace-a", invalid, nil); err == nil {
		t.Fatal("invalid conditional JSON accepted")
	}
}

func TestWorkflowWorkerCancellationAndScanFailureEdges(t *testing.T) {
	repository := openWorkflowWorkerEdgeStore(t)
	execution := workflowWorkerEdgeExecution("execution-corrupt")
	if err := repository.InsertExecution(t.Context(), "workspace-a", execution); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.store.DB().ExecContext(t.Context(), `UPDATE _workflow_executions SET attempt = 'invalid' WHERE id = ?`, execution.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.GetExecution(t.Context(), "workspace-a", execution.ID); err == nil {
		t.Fatal("corrupt execution scanned")
	}
	if _, err := repository.ListExecutions(t.Context(), "workspace-a", 1); err == nil {
		t.Fatal("corrupt execution listed")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	checks := map[string]func() error{
		"get execution":  func() error { _, _, err := repository.GetExecution(cancelled, "workspace-a", execution.ID); return err },
		"list execution": func() error { _, err := repository.ListExecutions(cancelled, "workspace-a", 1); return err },
		"conditional update": func() error {
			_, err := repository.UpdateExecutionWhere(cancelled, "workspace-a", workflowWorkerEdgeExecution("x"), nil)
			return err
		},
		"list task":   func() error { _, err := repository.ListTasks(cancelled, "workspace-a", "", "", "", 1); return err },
		"get process": func() error { _, _, err := repository.GetProcess(cancelled, "workspace-a", "process"); return err },
		"list events": func() error {
			_, err := repository.ListProcessEvents(cancelled, "workspace-a", "process", 1)
			return err
		},
		"insert event": func() error {
			return repository.InsertProcessEvent(cancelled, "workspace-a", workflowmodel.WorkflowProcessEvent{ID: "event"})
		},
		"update task": func() error {
			return repository.UpdateTask(cancelled, "workspace-a", workflowmodel.WorkflowTask{ID: "task"})
		},
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			if err := check(); !errors.Is(err, context.Canceled) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestWorkflowWorkerTaskDeadlineAndEventEdges(t *testing.T) {
	repository := openWorkflowWorkerEdgeStore(t)
	processStore := NewWorkflowProcessStore(repository.store)
	process := workflowmodel.WorkflowProcessInstance{
		ID: "process-edge", WorkflowKey: "approval", WorkflowName: "Approval", DefinitionVersionID: "version", DefinitionVersion: 1,
		DefinitionSnapshot: definitionmodel.WorkflowSchema{Key: "approval"}, InitiatorID: "user", Status: "running", Variables: map[string]any{}, Result: map[string]any{}, CreatedAt: "now", UpdatedAt: "now",
	}
	if err := processStore.InsertProcess(t.Context(), "workspace-a", process); err != nil {
		t.Fatal(err)
	}
	task := workflowmodel.WorkflowTask{ID: "task-edge", ProcessID: process.ID, NodeInstanceID: "node", NodeID: "approve", Title: "Approve", Status: "open", DueAt: "2026-07-20T01:00:00Z", CreatedAt: "now", UpdatedAt: "now"}
	if err := processStore.InsertTask(t.Context(), "workspace-a", task); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{0, 501} {
		if values, err := repository.ListTasks(t.Context(), "workspace-a", " ", " ", " ", limit); err != nil || len(values) != 1 {
			t.Fatalf("limit=%d tasks=%v error=%v", limit, values, err)
		}
	}
	if _, err := repository.ListTasks(t.Context(), " ", "", "", "", 1); err == nil {
		t.Fatal("blank task workspace accepted")
	}
	if _, found, err := repository.GetProcess(t.Context(), "workspace-a", "missing"); err != nil || found {
		t.Fatalf("missing process found=%v error=%v", found, err)
	}
	if _, _, err := repository.GetProcess(t.Context(), " ", process.ID); err == nil {
		t.Fatal("blank process workspace accepted")
	}
	event := workflowmodel.WorkflowProcessEvent{ID: "event-edge", ProcessID: process.ID, Event: "created", Metadata: map[string]any{"ok": true}, CreatedAt: "now"}
	if err := repository.InsertProcessEvent(t.Context(), "workspace-a", event); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{0, 1001} {
		if events, err := repository.ListProcessEvents(t.Context(), "workspace-a", process.ID, limit); err != nil || len(events) != 1 {
			t.Fatalf("limit=%d events=%v error=%v", limit, events, err)
		}
	}
	if _, err := repository.ListProcessEvents(t.Context(), " ", process.ID, 1); err == nil {
		t.Fatal("blank event workspace accepted")
	}
	if err := repository.InsertProcessEvent(t.Context(), " ", event); err == nil {
		t.Fatal("blank event insert workspace accepted")
	}
	if err := repository.UpdateTask(t.Context(), " ", task); err == nil {
		t.Fatal("blank task update workspace accepted")
	}
	task.ID = "missing"
	if err := repository.UpdateTask(t.Context(), "workspace-a", task); err == nil {
		t.Fatal("missing task updated")
	}
}
