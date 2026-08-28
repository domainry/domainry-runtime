package workflow

import (
	"fmt"
	"sync"
	"testing"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestUpdateWorkflowExecutionWherePreventsStaleRetryClaim(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureEvidenceSchema(t.Context()); err != nil {
		t.Fatalf("ensure evidence schema: %v", err)
	}
	execution := workflowmodel.WorkflowExecution{
		ID:             "exec_1",
		WorkflowKey:    "daily_task",
		Name:           "Daily task",
		Trigger:        "scheduled:daily_task",
		Status:         "failed",
		ActionType:     "object_action",
		Action:         map[string]any{"type": "object_action"},
		Payload:        map[string]any{"record_id": "r1"},
		Result:         map[string]any{"attempt_failed": true},
		ObjectKey:      "task",
		RecordID:       "r1",
		ActorID:        "system",
		RunAs:          "system",
		IdempotencyKey: "daily_task:r1:20260709",
		Attempt:        1,
		MaxAttempts:    3,
		NextRunAt:      "2026-07-09T00:01:00Z",
		LastError:      "temporary failure",
		Message:        "workflow.message.retryScheduled",
		CreatedAt:      "2026-07-09T00:00:00Z",
		UpdatedAt:      "2026-07-09T00:00:00Z",
	}
	workflowStore := NewWorkflowWorkerStore(store)
	if err := workflowStore.InsertExecution(t.Context(), "default", execution); err != nil {
		t.Fatalf("insert workflow execution: %v", err)
	}

	claimed := execution
	claimed.Status = "running"
	claimed.NextRunAt = ""
	claimed.Message = "workflow.message.retryClaimed"
	claimed.UpdatedAt = "2026-07-09T00:01:01Z"
	ok, err := workflowStore.UpdateExecutionWhere(t.Context(), "default", claimed, map[string]any{"status": "failed", "updated_at": execution.UpdatedAt})
	if err != nil {
		t.Fatalf("first conditional workflow update: %v", err)
	}
	if !ok {
		t.Fatalf("expected first workflow retry claim to succeed")
	}

	stale := execution
	stale.Status = "running"
	stale.NextRunAt = ""
	stale.Message = "workflow.message.retryClaimed"
	stale.UpdatedAt = "2026-07-09T00:01:02Z"
	ok, err = workflowStore.UpdateExecutionWhere(t.Context(), "default", stale, map[string]any{"status": "failed", "updated_at": execution.UpdatedAt})
	if err != nil {
		t.Fatalf("second conditional workflow update: %v", err)
	}
	if ok {
		t.Fatalf("expected stale workflow retry claim to be rejected")
	}
}

func TestWorkflowExecutionLeaseFencesConcurrentAndStaleWorkers(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureEvidenceSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewWorkflowWorkerStore(store)
	execution := workflowmodel.WorkflowExecution{ID: "exec_fencing", WorkflowKey: "daily_task", Name: "Daily task", Trigger: "scheduled", Status: "failed", Action: map[string]any{}, Payload: map[string]any{}, Result: map[string]any{}, ActorID: "system", Attempt: 1, MaxAttempts: 3, NextRunAt: "2026-07-19T00:00:00Z", Message: "retry", CreatedAt: "2026-07-19T00:00:00Z", UpdatedAt: "2026-07-19T00:00:00Z"}
	if err := repository.InsertExecution(t.Context(), "default", execution); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	winners := make(chan workflowmodel.WorkflowExecution, 100)
	errorsFound := make(chan error, 100)
	var group sync.WaitGroup
	for index := range 100 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			candidate := execution
			candidate.Status, candidate.LeaseOwner, candidate.LeaseExpiresAt, candidate.FencingToken = "running", fmt.Sprintf("runtime-%d", index), "2026-07-19T00:01:00Z", 1
			won, err := repository.UpdateExecutionWhere(t.Context(), "default", candidate, map[string]any{"status": "failed", "updated_at": execution.UpdatedAt, "lease_owner": "", "fencing_token": int64(0)})
			if err != nil {
				errorsFound <- err
			} else if won {
				winners <- candidate
			}
		}()
	}
	close(start)
	group.Wait()
	close(winners)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatal(err)
	}
	var first workflowmodel.WorkflowExecution
	count := 0
	for winner := range winners {
		first, count = winner, count+1
	}
	if count != 1 || first.FencingToken != 1 {
		t.Fatalf("workflow claim winners=%d first=%#v", count, first)
	}
	second := first
	second.LeaseOwner, second.LeaseExpiresAt, second.FencingToken = "runtime-restarted", "2026-07-19T00:03:00Z", 2
	if won, err := repository.UpdateExecutionWhere(t.Context(), "default", second, map[string]any{"status": "running", "lease_owner": first.LeaseOwner, "fencing_token": first.FencingToken, "lease_expires_at": first.LeaseExpiresAt}); err != nil || !won {
		t.Fatalf("workflow reclaim won=%v err=%v", won, err)
	}
	stale := first
	stale.Status, stale.LeaseOwner, stale.LeaseExpiresAt = "succeeded", "", ""
	if won, err := repository.UpdateExecutionWhere(t.Context(), "default", stale, map[string]any{"status": "running", "lease_owner": first.LeaseOwner, "fencing_token": first.FencingToken}); err != nil || won {
		t.Fatalf("stale workflow terminal write won=%v err=%v", won, err)
	}
	completed := second
	completed.Status, completed.LeaseOwner, completed.LeaseExpiresAt = "succeeded", "", ""
	if won, err := repository.UpdateExecutionWhere(t.Context(), "default", completed, map[string]any{"status": "running", "lease_owner": second.LeaseOwner, "fencing_token": second.FencingToken}); err != nil || !won {
		t.Fatalf("current workflow terminal write won=%v err=%v", won, err)
	}
}
