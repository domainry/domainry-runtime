package workflow

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestWorkflowStoreWorkspaceIsolationContract(t *testing.T) {
	const executionV1 = "2026-09-24T00:00:00Z"
	const executionV2 = "2026-09-24T00:00:01Z"
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}

	processes := NewWorkflowProcessStore(store)
	workers := NewWorkflowWorkerStore(store)
	workspaceA, workspaceB := "workspace-a", "workspace-b"
	sharedProcess := workflowmodel.WorkflowProcessInstance{
		ID: "shared-process", WorkflowKey: "approval", WorkflowName: "Approval", DefinitionVersion: 1,
		DefinitionSnapshot: definitionmodel.WorkflowSchema{Key: "approval"}, InitiatorID: "requester", Status: "waiting",
		CurrentNodeIDs: []string{"approve"}, Variables: map[string]any{}, Result: map[string]any{}, CreatedAt: workflowTestTimeV1, UpdatedAt: workflowTestTimeV1,
	}
	for _, workspaceID := range []string{workspaceA, workspaceB} {
		if err := processes.InsertProcess(t.Context(), workspaceID, sharedProcess); err != nil {
			t.Fatalf("insert process in %s: %v", workspaceID, err)
		}
		execution := workflowmodel.WorkflowExecution{ID: "shared-execution", WorkflowKey: "approval", Name: "Approval", Trigger: "manual", Status: "pending", Action: map[string]any{}, Payload: map[string]any{}, Result: map[string]any{}, ActorID: "requester", CreatedAt: executionV1, UpdatedAt: executionV1}
		if err := workers.InsertExecution(t.Context(), workspaceID, execution); err != nil {
			t.Fatalf("insert execution in %s: %v", workspaceID, err)
		}
	}

	processA, found, err := processes.GetProcess(t.Context(), workspaceA, sharedProcess.ID)
	if err != nil || !found {
		t.Fatalf("get workspace A process: found=%v err=%v", found, err)
	}
	processA.Status, processA.UpdatedAt = "completed", workflowTestTimeV2
	if err := processes.UpdateProcess(t.Context(), workspaceA, processA); err != nil {
		t.Fatal(err)
	}
	processB, found, err := processes.GetProcess(t.Context(), workspaceB, sharedProcess.ID)
	if err != nil || !found || processB.Status != "waiting" {
		t.Fatalf("workspace A process update leaked to B: process=%+v found=%v err=%v", processB, found, err)
	}

	executionA, found, err := workers.GetExecution(t.Context(), workspaceA, "shared-execution")
	if err != nil || !found {
		t.Fatalf("get workspace A execution: found=%v err=%v", found, err)
	}
	executionA.Status, executionA.UpdatedAt = "running", executionV2
	updated, err := workers.UpdateExecutionWhere(t.Context(), workspaceA, executionA, map[string]any{"status": "pending", "updated_at": executionV1})
	if err != nil || !updated {
		t.Fatalf("claim workspace A execution: updated=%v err=%v", updated, err)
	}
	executionB, found, err := workers.GetExecution(t.Context(), workspaceB, executionA.ID)
	if err != nil || !found || executionB.Status != "pending" {
		t.Fatalf("workspace A claim leaked to B: execution=%+v found=%v err=%v", executionB, found, err)
	}

	if _, _, err := processes.GetProcess(t.Context(), "", sharedProcess.ID); err == nil {
		t.Fatal("process access without workspace must be rejected")
	}
	if _, err := workers.ListExecutions(t.Context(), "", 10); err == nil {
		t.Fatal("execution access without workspace must be rejected")
	}
}
