package workflow

import (
	"errors"
	"fmt"
	"testing"

	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestWorkflowQuorumSnapshotRejectsStaleDistinctTaskDecision(t *testing.T) {
	store, _, process, _, task := workflowDecisionStoreFixture(t)
	t.Cleanup(func() { _ = store.Close() })
	processes := NewWorkflowProcessStore(store)
	decisions := NewWorkflowDecisionStore(store)
	other := task
	other.ID, other.AssigneeUserID = "other-task", "other-manager"
	if err := processes.InsertTask(t.Context(), process.WorkspaceID, other); err != nil {
		t.Fatal(err)
	}
	makeCommit := func(task workflowmodel.WorkflowTask, before, after string) transactionmodel.WorkflowDecisionCommit {
		updated := process
		updated.UpdatedAt = after
		task.Status, task.Decision, task.CompletedBy, task.UpdatedAt = "approved", "approved", task.AssigneeUserID, after
		return transactionmodel.WorkflowDecisionCommit{WorkspaceID: process.WorkspaceID, DecidedTask: task, ExpectedTaskStatus: "open", ExpectedAssigneeID: task.AssigneeUserID, Process: &updated, ExpectedProcessUpdatedAt: before}
	}
	first := makeCommit(task, "v1", "v2")
	stale := makeCommit(other, "v1", "v3")
	if won, err := decisions.CommitWorkflowDecision(t.Context(), first); err != nil || !won {
		t.Fatalf("first won=%v err=%v", won, err)
	}
	if won, err := decisions.CommitWorkflowDecision(t.Context(), stale); won || !errors.Is(err, workflowcontract.ErrWorkflowDecisionSnapshotChanged) {
		t.Fatalf("stale won=%v err=%v", won, err)
	}
	unchanged, _, err := processes.GetTask(t.Context(), process.WorkspaceID, other.ID)
	if err != nil || unchanged.Status != "open" {
		t.Fatalf("stale commit changed task=%+v err=%v", unchanged, err)
	}
	stale.ExpectedProcessUpdatedAt = "v2"
	if won, err := decisions.CommitWorkflowDecision(t.Context(), stale); err != nil || !won {
		t.Fatalf("fresh won=%v err=%v", won, err)
	}
}

func TestWorkflowQuorumSnapshotRollsBackWithFailedCommit(t *testing.T) {
	store, _, process, _, task := workflowDecisionStoreFixture(t)
	t.Cleanup(func() { _ = store.Close() })
	processes := NewWorkflowProcessStore(store)
	event := workflowmodel.WorkflowProcessEvent{ID: "duplicate", ProcessID: process.ID, Event: "task_approved", ActorID: task.AssigneeUserID, CreatedAt: "v1"}
	if err := processes.InsertEvent(t.Context(), process.WorkspaceID, event); err != nil {
		t.Fatal(err)
	}
	updated := process
	updated.UpdatedAt = "v2"
	commit := workflowDecisionEdgeCommit(task)
	commit.Process, commit.ExpectedProcessUpdatedAt, commit.Events = &updated, "v1", []workflowmodel.WorkflowProcessEvent{event}
	if won, err := NewWorkflowDecisionStore(store).CommitWorkflowDecision(t.Context(), commit); err == nil || won {
		t.Fatalf("failed commit won=%v err=%v", won, err)
	}
	actual, _, err := processes.GetProcess(t.Context(), process.WorkspaceID, process.ID)
	if err != nil || actual.UpdatedAt != "v1" {
		t.Fatalf("process snapshot did not roll back=%+v err=%v", actual, err)
	}
	actualTask, _, err := processes.GetTask(t.Context(), process.WorkspaceID, task.ID)
	if err != nil || actualTask.Status != "open" {
		t.Fatalf("task did not roll back=%+v err=%v", actualTask, err)
	}
}

func TestWorkflowApprovalTaskReaderIsCompleteAndScoped(t *testing.T) {
	store, _, process, node, task := workflowDecisionStoreFixture(t)
	t.Cleanup(func() { _ = store.Close() })
	repository := NewWorkflowProcessStore(store)
	for i := 1; i <= 501; i++ {
		value := task
		value.ID, value.Sequence = fmt.Sprintf("task-%d", i), i+1
		if err := repository.InsertTask(t.Context(), process.WorkspaceID, value); err != nil {
			t.Fatal(err)
		}
	}
	other := task
	other.ID, other.NodeInstanceID = "previous-task", "previous-node"
	if err := repository.InsertTask(t.Context(), process.WorkspaceID, other); err != nil {
		t.Fatal(err)
	}
	tasks, err := repository.ListApprovalTasks(t.Context(), process.WorkspaceID, process.ID, node.ID)
	if err != nil || len(tasks) != 502 {
		t.Fatalf("node task count=%d err=%v", len(tasks), err)
	}
	for _, scope := range []struct{ workspace, process, node string }{
		{"other-workspace", process.ID, node.ID}, {process.WorkspaceID, "other-process", node.ID}, {process.WorkspaceID, process.ID, "other-node"},
	} {
		if tasks, err := repository.ListApprovalTasks(t.Context(), scope.workspace, scope.process, scope.node); err != nil || len(tasks) != 0 {
			t.Fatalf("scope=%+v tasks=%d err=%v", scope, len(tasks), err)
		}
	}
}
