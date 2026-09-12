package workflow

import (
	"context"
	"errors"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"testing"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestWorkflowProcessStoreLifecycleAndCancellation(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewWorkflowProcessStore(store)
	const workspaceID = "workspace-a"
	process := workflowmodel.WorkflowProcessInstance{ID: "process-1", WorkflowKey: "expense", WorkflowName: "Expense", DefinitionVersionID: "version-1", DefinitionVersion: 1, DefinitionHash: "hash", DefinitionSnapshot: definitionmodel.WorkflowSchema{Key: "expense", Name: "Expense"}, InitiatorID: "user-1", Status: "running", CurrentNodeIDs: []string{"approve"}, Variables: map[string]any{"amount": 10}, Result: map[string]any{}, CreatedAt: "v1", UpdatedAt: "v1"}
	if err := repository.InsertProcess(t.Context(), workspaceID, process); err != nil {
		t.Fatalf("insert process: %v", err)
	}
	node := workflowmodel.WorkflowNodeInstance{ID: "node-1", ProcessID: process.ID, NodeID: "approve", NodeType: "approval", Iteration: 1, Status: "waiting", Input: map[string]any{"amount": 10}, Output: map[string]any{}, StartedAt: "v1"}
	if err := repository.InsertNode(t.Context(), workspaceID, node); err != nil {
		t.Fatalf("insert node: %v", err)
	}
	task := workflowmodel.WorkflowTask{ID: "task-1", ProcessID: process.ID, NodeInstanceID: node.ID, NodeID: node.NodeID, Title: "Approve", AssigneeUserID: "manager", Sequence: 1, Status: "open", CreatedAt: "v1", UpdatedAt: "v1"}
	if err := repository.InsertTask(t.Context(), workspaceID, task); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	event := workflowmodel.WorkflowProcessEvent{ID: "event-1", ProcessID: process.ID, NodeID: node.NodeID, TaskID: task.ID, Event: "task_created", ActorID: "system", Summary: "Created", Metadata: map[string]any{"request_id": "req-1"}, CreatedAt: "v1"}
	if err := repository.InsertEvent(t.Context(), workspaceID, event); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	process.Status, process.UpdatedAt = "waiting", "v2"
	if err := repository.UpdateProcess(t.Context(), workspaceID, process); err != nil {
		t.Fatalf("update process: %v", err)
	}
	node.Status, node.Output, node.CompletedAt = "success", map[string]any{"decision": "approved"}, "v2"
	if err := repository.UpdateNode(t.Context(), workspaceID, node); err != nil {
		t.Fatalf("update node: %v", err)
	}
	task.Status, task.Decision, task.UpdatedAt = "approved", "approved", "v2"
	if err := repository.UpdateTask(t.Context(), workspaceID, task); err != nil {
		t.Fatalf("update task: %v", err)
	}
	if value, found, err := repository.GetProcess(t.Context(), workspaceID, process.ID); err != nil || !found || value.Status != "waiting" || value.DefinitionSnapshot.DefinitionVersionID != "version-1" || value.DefinitionSnapshot.PublishedVersion != 1 {
		t.Fatalf("get process=%#v found=%v err=%v", value, found, err)
	}
	if values, err := repository.ListProcesses(t.Context(), workspaceID, workflowmodel.WorkflowProcessFilter{Status: "waiting", Limit: 10}); err != nil || len(values) != 1 {
		t.Fatalf("list processes=%#v err=%v", values, err)
	}
	if values, err := repository.ListNodes(t.Context(), workspaceID, process.ID); err != nil || len(values) != 1 || values[0].Status != "success" {
		t.Fatalf("nodes=%#v err=%v", values, err)
	}
	if values, err := repository.ListNodesForProcesses(t.Context(), workspaceID, []string{process.ID, "missing"}); err != nil || len(values) != 1 || values[0].ProcessID != process.ID {
		t.Fatalf("batch nodes=%#v err=%v", values, err)
	}
	if value, found, err := repository.GetTask(t.Context(), workspaceID, task.ID); err != nil || !found || value.Decision != "approved" {
		t.Fatalf("get task=%#v found=%v err=%v", value, found, err)
	}
	if values, err := repository.ListTasks(t.Context(), workspaceID, process.ID, "manager", "approved", 10); err != nil || len(values) != 1 {
		t.Fatalf("tasks=%#v err=%v", values, err)
	}
	if values, err := repository.ListEvents(t.Context(), workspaceID, process.ID, 10); err != nil || len(values) != 1 || values[0].Metadata["request_id"] != "req-1" {
		t.Fatalf("events=%#v err=%v", values, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := repository.GetProcess(cancelled, workspaceID, process.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled get error=%v", err)
	}
	if err := repository.InsertEvent(cancelled, workspaceID, workflowmodel.WorkflowProcessEvent{ID: "never"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled insert error=%v", err)
	}
}
