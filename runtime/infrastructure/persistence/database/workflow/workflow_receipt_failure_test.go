package workflow

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func workflowWorkerFailureStore(t *testing.T, state *workflowSQLState) WorkflowWorkerStore {
	t.Helper()
	store := openStoreForGeneratedListTest(t)
	db := openWorkflowScriptedDB(state)
	t.Cleanup(func() {
		_ = db.Close()
		_ = store.Close()
	})
	repository := NewWorkflowWorkerStore(store)
	repository.db = db
	return repository
}

func workflowClaimRequest(now time.Time) workflowmodel.WorkflowExecutionClaimRequest {
	return workflowmodel.WorkflowExecutionClaimRequest{
		Receipt:            workflowmodel.WorkflowExecutionReceipt{WorkspaceID: "workspace", WorkflowKey: "workflow", IdempotencyKey: "key"},
		RequestFingerprint: "fingerprint", LeaseOwner: "owner", LeaseTTL: time.Minute, Now: now,
	}
}

func workflowReceiptRow(now time.Time) workflowSQLQueryStep {
	return workflowSQLQueryStep{
		columns: workflowReceiptColumns(),
		rows: [][]driver.Value{{
			"receipt", "workspace", "workflow", "key", "fingerprint", string(idempotency.StatusProcessing), "", "old-owner",
			now.Add(-time.Minute).Format(time.RFC3339Nano), int64(1), now.Add(-time.Hour).Format(time.RFC3339Nano), now.Add(-time.Hour).Format(time.RFC3339Nano), "",
		}},
	}
}

func TestWorkflowExecutionReceiptClaimFailures(t *testing.T) {
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	request := workflowClaimRequest(now)

	t.Run("find after insert", func(t *testing.T) {
		store := workflowWorkerFailureStore(t, &workflowSQLState{execSteps: []workflowSQLExecStep{{err: errWorkflowSQL}}, querySteps: []workflowSQLQueryStep{{err: errWorkflowSQL}}})
		if _, err := store.TryBeginExecution(t.Context(), request); !errors.Is(err, errWorkflowSQL) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("insert conflict disappears", func(t *testing.T) {
		store := workflowWorkerFailureStore(t, &workflowSQLState{execSteps: []workflowSQLExecStep{{err: errWorkflowSQL}}})
		if _, err := store.TryBeginExecution(t.Context(), request); err == nil {
			t.Fatal("expected constraint error")
		}
	})
	t.Run("reclaim update", func(t *testing.T) {
		store := workflowWorkerFailureStore(t, &workflowSQLState{execSteps: []workflowSQLExecStep{{err: errWorkflowSQL}, {err: errWorkflowSQL}}, querySteps: []workflowSQLQueryStep{workflowReceiptRow(now)}})
		if _, err := store.TryBeginExecution(t.Context(), request); !errors.Is(err, errWorkflowSQL) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("reclaim rows affected", func(t *testing.T) {
		store := workflowWorkerFailureStore(t, &workflowSQLState{execSteps: []workflowSQLExecStep{{err: errWorkflowSQL}, {rowsErr: errWorkflowSQL}}, querySteps: []workflowSQLQueryStep{workflowReceiptRow(now)}})
		if _, err := store.TryBeginExecution(t.Context(), request); !errors.Is(err, errWorkflowSQL) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("reclaim reload", func(t *testing.T) {
		store := workflowWorkerFailureStore(t, &workflowSQLState{execSteps: []workflowSQLExecStep{{err: errWorkflowSQL}, {rows: 1}}, querySteps: []workflowSQLQueryStep{workflowReceiptRow(now), {err: errWorkflowSQL}}})
		if _, err := store.TryBeginExecution(t.Context(), request); !errors.Is(err, errWorkflowSQL) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("reclaim race lost", func(t *testing.T) {
		store := workflowWorkerFailureStore(t, &workflowSQLState{execSteps: []workflowSQLExecStep{{err: errWorkflowSQL}, {rows: 0}}, querySteps: []workflowSQLQueryStep{workflowReceiptRow(now), workflowReceiptRow(now)}})
		claim, err := store.TryBeginExecution(t.Context(), request)
		if err != nil || claim.Decision != idempotency.DecisionInProgress {
			t.Fatalf("claim=%#v err=%v", claim, err)
		}
	})
	t.Run("busy wait cancellation", func(t *testing.T) {
		store := workflowWorkerFailureStore(t, &workflowSQLState{execSteps: []workflowSQLExecStep{{err: errors.New("SQLITE_BUSY")}}})
		store.claimBackoff = func(context.Context, int) error { return context.Canceled }
		if _, err := store.TryBeginExecution(t.Context(), request); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("busy retry exhausted", func(t *testing.T) {
		steps := make([]workflowSQLExecStep, 50)
		for index := range steps {
			steps[index].err = errors.New("database is locked")
		}
		store := workflowWorkerFailureStore(t, &workflowSQLState{execSteps: steps})
		store.claimBackoff = func(context.Context, int) error { return nil }
		if _, err := store.TryBeginExecution(t.Context(), request); err == nil || !strings.Contains(err.Error(), "remained busy") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("defaults", func(t *testing.T) {
		store := workflowWorkerFailureStore(t, &workflowSQLState{})
		request := workflowmodel.WorkflowExecutionClaimRequest{Receipt: workflowmodel.WorkflowExecutionReceipt{WorkspaceID: "  ", WorkflowKey: " workflow ", IdempotencyKey: " key "}, RequestFingerprint: " fingerprint ", LeaseOwner: " owner "}
		claim, err := store.TryBeginExecution(t.Context(), request)
		if err != nil || claim.Decision != idempotency.DecisionAcquired || claim.Receipt.WorkspaceID != "default" {
			t.Fatalf("claim=%#v err=%v", claim, err)
		}
	})
}

func TestWorkflowExecutionReceiptCompletionFailures(t *testing.T) {
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	completion := workflowmodel.WorkflowExecutionReceiptCompletion{WorkspaceID: "workspace-a", ReceiptID: "receipt", ExecutionID: "execution", LeaseOwner: "owner", FencingToken: 1, Now: now, ExpiresAt: now.Add(time.Hour)}
	tests := []workflowSQLState{
		{execSteps: []workflowSQLExecStep{{err: errWorkflowSQL}}},
		{execSteps: []workflowSQLExecStep{{rowsErr: errWorkflowSQL}}},
	}
	for index := range tests {
		store := workflowWorkerFailureStore(t, &tests[index])
		if err := store.CompleteExecutionReceipt(t.Context(), completion); !errors.Is(err, errWorkflowSQL) {
			t.Fatalf("case %d error = %v", index, err)
		}
	}
	store := workflowWorkerFailureStore(t, &workflowSQLState{execSteps: []workflowSQLExecStep{{rows: 0}}, querySteps: []workflowSQLQueryStep{{err: errWorkflowSQL}}})
	if err := store.CompleteExecutionReceipt(t.Context(), completion); !mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
		t.Fatalf("error = %v", err)
	}
	store = workflowWorkerFailureStore(t, &workflowSQLState{})
	completion.Now = time.Time{}
	if err := store.CompleteExecutionReceipt(t.Context(), completion); err != nil {
		t.Fatalf("default time completion: %v", err)
	}
}

func TestWorkflowReceiptBackoffConditions(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := workflowClaimBackoff(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
	if err := workflowClaimBackoff(t.Context(), 0); err != nil {
		t.Fatalf("timer error = %v", err)
	}
	store := WorkflowWorkerStore{}
	if err := store.waitForClaimRetry(t.Context(), 0); err != nil {
		t.Fatalf("fallback retry error = %v", err)
	}
}
