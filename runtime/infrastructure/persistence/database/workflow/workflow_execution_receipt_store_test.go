package workflow

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestWorkflowExecutionReceiptStoreIsAtomicReplayableAndFenced(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewWorkflowWorkerStore(store)
	now := time.Date(2026, 7, 19, 15, 0, 0, 0, time.UTC)
	request := workflowmodel.WorkflowExecutionClaimRequest{
		Receipt:            workflowmodel.WorkflowExecutionReceipt{WorkspaceID: "workspace-a", WorkflowKey: "approval", IdempotencyKey: "key-a"},
		RequestFingerprint: "fingerprint-a", LeaseOwner: "runtime-a", LeaseTTL: time.Minute, Now: now,
	}
	first, err := repository.TryBeginExecution(t.Context(), request)
	if err != nil || first.Decision != idempotency.DecisionAcquired || first.Receipt.FencingToken != 1 {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	second, err := repository.TryBeginExecution(t.Context(), request)
	if err != nil || second.Decision != idempotency.DecisionInProgress {
		t.Fatalf("second=%#v err=%v", second, err)
	}
	conflicting := request
	conflicting.RequestFingerprint = "fingerprint-b"
	conflict, err := repository.TryBeginExecution(t.Context(), conflicting)
	if err != nil || conflict.Decision != idempotency.DecisionFingerprintConflict {
		t.Fatalf("conflict=%#v err=%v", conflict, err)
	}
	wrong := workflowmodel.WorkflowExecutionReceiptCompletion{ReceiptID: first.Receipt.ID, ExecutionID: "execution-a", LeaseOwner: "runtime-a", FencingToken: 99, Now: now, ExpiresAt: now.Add(time.Hour)}
	if err := repository.CompleteExecutionReceipt(t.Context(), wrong); !mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
		t.Fatalf("wrong completion=%v", err)
	}
	wrong.FencingToken = first.Receipt.FencingToken
	if err := repository.CompleteExecutionReceipt(t.Context(), wrong); err != nil {
		t.Fatal(err)
	}
	replay, err := repository.TryBeginExecution(t.Context(), request)
	if err != nil || replay.Decision != idempotency.DecisionReplay || replay.Receipt.ExecutionID != "execution-a" {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
}

func TestWorkflowExecutionReceiptStoreReclaimsAndIsolatesWorkspace(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewWorkflowWorkerStore(store)
	now := time.Date(2026, 7, 19, 16, 0, 0, 0, time.UTC)
	claim := func(workspace, owner string, at time.Time) workflowmodel.WorkflowExecutionClaimResult {
		result, err := repository.TryBeginExecution(t.Context(), workflowmodel.WorkflowExecutionClaimRequest{Receipt: workflowmodel.WorkflowExecutionReceipt{WorkspaceID: workspace, WorkflowKey: "approval", IdempotencyKey: "shared"}, RequestFingerprint: "same", LeaseOwner: owner, LeaseTTL: time.Minute, Now: at})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first, isolated := claim("workspace-a", "runtime-a", now), claim("workspace-b", "runtime-b", now)
	if first.Decision != idempotency.DecisionAcquired || isolated.Decision != idempotency.DecisionAcquired || first.Receipt.ID == isolated.Receipt.ID {
		t.Fatalf("workspace scope collided: first=%#v isolated=%#v", first, isolated)
	}
	reclaimed := claim("workspace-a", "runtime-c", now.Add(2*time.Minute))
	if reclaimed.Decision != idempotency.DecisionAcquired || reclaimed.Receipt.FencingToken != 2 || reclaimed.Receipt.LeaseOwner != "runtime-c" {
		t.Fatalf("reclaimed=%#v", reclaimed)
	}
}

func TestWorkflowExecutionReceiptStoreHundredConcurrentClaimsHaveOneOwner(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	store.DB().SetMaxOpenConns(16)
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewWorkflowWorkerStore(store)
	now := time.Date(2026, 7, 19, 17, 0, 0, 0, time.UTC)
	var acquired atomic.Int64
	errorsFound := make(chan error, 100)
	var wait sync.WaitGroup
	for worker := 0; worker < 100; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			claim, err := repository.TryBeginExecution(t.Context(), workflowmodel.WorkflowExecutionClaimRequest{Receipt: workflowmodel.WorkflowExecutionReceipt{WorkspaceID: "workspace-a", WorkflowKey: "approval", IdempotencyKey: "concurrent"}, RequestFingerprint: "same", LeaseOwner: fmt.Sprintf("runtime-%d", worker), LeaseTTL: time.Minute, Now: now})
			if err != nil {
				errorsFound <- err
				return
			}
			if claim.Decision == idempotency.DecisionAcquired {
				acquired.Add(1)
			} else if claim.Decision != idempotency.DecisionInProgress {
				errorsFound <- fmt.Errorf("unexpected decision %s", claim.Decision)
			}
		}(worker)
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
	if got := acquired.Load(); got != 1 {
		t.Fatalf("acquired=%d want 1", got)
	}
}
