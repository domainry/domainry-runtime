package workflow

import (
	"fmt"
	"github.com/domainry/domainry-foundation/apperror"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	"testing"
)

func TestWorkflowWithdrawalFailureRollsBackBusinessAndAllWorkflowRows(t *testing.T) {
	for _, fail := range []bool{true, false} {
		name := "commit"
		if fail {
			name = "event_failure"
		}
		t.Run(name, func(t *testing.T) {
			store, object, process, _, _ := workflowDecisionStoreFixture(t)
			defer store.Close()
			repository := NewWorkflowProcessStore(store)
			if fail {
				if err := repository.InsertEvent(t.Context(), process.WorkspaceID, workflowmodel.WorkflowProcessEvent{ID: "withdrawal", ProcessID: process.ID, CreatedAt: "v2"}); err != nil {
					t.Fatal(err)
				}
			}
			withdrawn := process
			withdrawn.Status, withdrawn.UpdatedAt, withdrawn.CompletedAt, withdrawn.CurrentNodeIDs = "cancelled", "v2", "v2", nil
			commit := transactionmodel.WorkflowWithdrawalCommit{Process: withdrawn, ExpectedStatus: "waiting", ExpectedUpdatedAt: "v1", ActorID: "requester", CommandID: "withdrawal"}
			tx, err := store.DB().BeginTx(t.Context(), recordMutationTxOptions())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			mutation := transactionmodel.RecordMutationCommit{Operation: "update", Object: object, Record: recordmodel.Record{ID: "business_1", CreatedAt: "v1", UpdatedAt: "v2", Data: map[string]any{"status": "withdrawn"}}, ExpectedUpdatedAt: "v1", WorkflowWithdrawals: []transactionmodel.WorkflowWithdrawalCommit{commit}}
			err = recordpersistence.NewRecordStore(store).ApplyRecordMutationTx(t.Context(), tx, process.WorkspaceID, mutation)
			if fail {
				if err == nil {
					t.Fatal("duplicate withdrawal event accepted")
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				assertWorkflowDecisionState(t, store, object, "open", "waiting", "waiting", "pending")
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			assertWorkflowDecisionState(t, store, object, "cancelled", "cancelled", "cancelled", "withdrawn")
			// A stale withdrawal never overwrites a concurrent approval or cancellation.
			tx, err = store.DB().BeginTx(t.Context(), recordMutationTxOptions())
			if err != nil {
				t.Fatal(err)
			}
			err = database.ApplyWorkflowWithdrawalTx(t.Context(), store, tx, process.WorkspaceID, commit)
			if apperror.CodeOf(err) != "backend.workflow.withdrawal_snapshot_changed" {
				t.Fatalf("stale snapshot=%v", err)
			}
			_ = tx.Rollback()
		})
	}
}

func TestWorkflowWithdrawalCancelsMoreThan500TasksWithoutTouchingHistory(t *testing.T) {
	worker := openWorkflowWorkerEdgeStore(t)
	repository := NewWorkflowProcessStore(worker.store)
	process := workflowmodel.WorkflowProcessInstance{WorkspaceID: "workspace-a", ID: "process", WorkflowKey: "approval", InitiatorID: "user", Status: "waiting", CreatedAt: "v1", UpdatedAt: "v1"}
	if err := repository.InsertProcess(t.Context(), "workspace-a", process); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 520; i++ {
		task := workflowmodel.WorkflowTask{ID: fmt.Sprintf("task_%04d", i), ProcessID: process.ID, Status: "open", CreatedAt: "v1", UpdatedAt: "v1"}
		if i == 519 {
			task.Status, task.Decision = "approved", "approved"
		}
		if err := repository.InsertTask(t.Context(), "workspace-a", task); err != nil {
			t.Fatal(err)
		}
	}
	process.Status, process.UpdatedAt, process.CompletedAt = "cancelled", "v2", "v2"
	if err := repository.CommitWorkflowWithdrawal(t.Context(), transactionmodel.WorkflowWithdrawalCommit{Process: process, ExpectedStatus: "waiting", ExpectedUpdatedAt: "v1", ActorID: "user", CommandID: "withdraw-all"}); err != nil {
		t.Fatal(err)
	}
	open, err := repository.ListTasks(t.Context(), "workspace-a", process.ID, "", "open", 500)
	if err != nil || len(open) != 0 {
		t.Fatalf("uncancelled tasks=%d err=%v", len(open), err)
	}
	history, ok, err := repository.GetTask(t.Context(), "workspace-a", fmt.Sprintf("task_%04d", 519))
	if err != nil || !ok || history.Status != "approved" || history.Decision != "approved" {
		t.Fatalf("history changed=%#v err=%v", history, err)
	}
}

func TestWorkflowWithdrawalAndApprovalComputedFromOneRevisionHaveOneWinner(t *testing.T) {
	store, object, process, node, task := workflowDecisionStoreFixture(t)
	defer store.Close()
	withdrawn := process
	withdrawn.Status, withdrawn.UpdatedAt, withdrawn.CompletedAt, withdrawn.CurrentNodeIDs = "cancelled", "withdraw-v2", "withdraw-v2", nil
	approved := process
	approved.Status, approved.UpdatedAt, approved.CompletedAt, approved.CurrentNodeIDs = "completed", "approve-v2", "approve-v2", nil
	decided := task
	decided.Status, decided.Decision, decided.CompletedBy, decided.UpdatedAt = "approved", "approved", task.AssigneeUserID, "approve-v2"
	node.Status = "approved"
	start := make(chan struct{})
	results := make(chan struct {
		kind string
		won  bool
		err  error
	}, 2)
	go func() {
		<-start
		won, err := NewWorkflowDecisionStore(store).CommitWorkflowDecision(t.Context(), transactionmodel.WorkflowDecisionCommit{WorkspaceID: process.WorkspaceID, Process: &approved, ExpectedProcessUpdatedAt: process.UpdatedAt, DecidedTask: decided, ExpectedTaskStatus: "open", ExpectedAssigneeID: task.AssigneeUserID, UpdateNodes: []workflowmodel.WorkflowNodeInstance{node}, RecordMutations: []transactionmodel.RecordMutationCommit{{Operation: "update", Object: object, Record: recordmodel.Record{ID: "business_1", CreatedAt: "v1", UpdatedAt: "approve-v2", Data: map[string]any{"status": "approved"}}, ExpectedUpdatedAt: "v1"}}})
		results <- struct {
			kind string
			won  bool
			err  error
		}{"approval", won, err}
	}()
	go func() {
		<-start
		tx, err := store.DB().BeginTx(t.Context(), recordMutationTxOptions())
		if err == nil {
			err = recordpersistence.NewRecordStore(store).ApplyRecordMutationTx(t.Context(), tx, process.WorkspaceID, transactionmodel.RecordMutationCommit{Operation: "update", Object: object, Record: recordmodel.Record{ID: "business_1", CreatedAt: "v1", UpdatedAt: "withdraw-v2", Data: map[string]any{"status": "withdrawn"}}, ExpectedUpdatedAt: "v1", WorkflowWithdrawals: []transactionmodel.WorkflowWithdrawalCommit{{Process: withdrawn, ExpectedStatus: "waiting", ExpectedUpdatedAt: process.UpdatedAt, ActorID: process.InitiatorID, CommandID: "withdraw-race"}}})
			if err == nil {
				err = tx.Commit()
			} else {
				_ = tx.Rollback()
			}
		}
		results <- struct {
			kind string
			won  bool
			err  error
		}{"withdrawal", err == nil, err}
	}()
	close(start)
	first, second := <-results, <-results
	if first.won == second.won {
		t.Fatalf("expected one winner: first=%+v second=%+v", first, second)
	}
	winner := first
	if second.won {
		winner = second
	}
	if winner.kind == "approval" {
		assertWorkflowDecisionState(t, store, object, "approved", "approved", "completed", "approved")
	} else {
		assertWorkflowDecisionState(t, store, object, "cancelled", "cancelled", "cancelled", "withdrawn")
	}
}

func TestWorkflowWithdrawalFencesTimerAndLateEngineReceipts(t *testing.T) {
	worker := openWorkflowWorkerEdgeStore(t)
	repository := NewWorkflowProcessStore(worker.store)
	process := workflowmodel.WorkflowProcessInstance{WorkspaceID: "workspace-a", ID: "timer-process", WorkflowKey: "approval", InitiatorID: "user", Status: "waiting", CreatedAt: "v1", UpdatedAt: "v1"}
	if err := repository.InsertProcess(t.Context(), process.WorkspaceID, process); err != nil {
		t.Fatal(err)
	}
	node := workflowmodel.WorkflowNodeInstance{ID: "timer-node", ProcessID: process.ID, NodeID: "timer", Status: "waiting", StartedAt: "v1"}
	if err := repository.InsertNode(t.Context(), process.WorkspaceID, node); err != nil {
		t.Fatal(err)
	}
	execution := workflowWorkerEdgeExecution("timer-execution")
	execution.ProcessID, execution.Status, execution.NextRunAt = process.ID, "failed", "2026-09-14T00:00:00Z"
	if err := worker.InsertExecution(t.Context(), process.WorkspaceID, execution); err != nil {
		t.Fatal(err)
	}
	cancelled := process
	cancelled.Status, cancelled.UpdatedAt, cancelled.CompletedAt = "cancelled", "v2", "v2"
	if err := repository.CommitWorkflowWithdrawal(t.Context(), transactionmodel.WorkflowWithdrawalCommit{Process: cancelled, ExpectedStatus: "waiting", ExpectedUpdatedAt: "v1", ActorID: "user", CommandID: "withdraw-timer"}); err != nil {
		t.Fatal(err)
	}
	process.Status, process.UpdatedAt, node.Status = "running", "v3", "success"
	if claimed, err := repository.ClaimWorkflowTimer(t.Context(), process.WorkspaceID, process, node, "v1"); err != nil || claimed {
		t.Fatalf("cancelled timer resumed: claimed=%v err=%v", claimed, err)
	}
	if err := repository.UpdateProcess(t.Context(), process.WorkspaceID, process); err == nil {
		t.Fatal("stale engine revived process")
	}
	if err := worker.UpdateExecution(t.Context(), process.WorkspaceID, execution); err == nil {
		t.Fatal("late engine revived execution")
	}
	execution.ID = "late-insert"
	if err := worker.InsertExecution(t.Context(), process.WorkspaceID, execution); err == nil {
		t.Fatal("late engine inserted live execution for cancelled process")
	}
}

func TestWorkflowTimerClaimRollsBackWhenTimerNodeWriteFails(t *testing.T) {
	worker := openWorkflowWorkerEdgeStore(t)
	repository := NewWorkflowProcessStore(worker.store)
	process := workflowmodel.WorkflowProcessInstance{WorkspaceID: "workspace-a", ID: "process", WorkflowKey: "approval", InitiatorID: "user", Status: "waiting", CreatedAt: "v1", UpdatedAt: "v1"}
	if err := repository.InsertProcess(t.Context(), process.WorkspaceID, process); err != nil {
		t.Fatal(err)
	}
	process.Status, process.UpdatedAt = "running", "v2"
	node := workflowmodel.WorkflowNodeInstance{ID: "missing-timer-node", ProcessID: process.ID, Status: "success"}
	if claimed, err := repository.ClaimWorkflowTimer(t.Context(), process.WorkspaceID, process, node, "v1"); err == nil || claimed {
		t.Fatalf("missing timer node was committed: claimed=%v err=%v", claimed, err)
	}
	persisted, ok, err := repository.GetProcess(t.Context(), process.WorkspaceID, process.ID)
	if err != nil || !ok || persisted.Status != "waiting" || persisted.UpdatedAt != "v1" {
		t.Fatalf("failed node write stranded process: %#v err=%v", persisted, err)
	}
}
