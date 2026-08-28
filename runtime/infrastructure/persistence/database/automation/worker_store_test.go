package automation

import (
	"context"
	"errors"
	automationcontract "github.com/domainry/domainry-runtime/runtime/domain/automation/contract"
	"sync"
	"testing"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
)

func TestAutomationWorkerStoreImplementsContractAndCancelsSQL(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewAutomationWorkerStore(store)
	var _ automationcontract.AutomationWorkerStore = repository
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := repository.ClaimInstruction(ctx, "default", automationmodel.AutomationInstructionExecution{IdempotencyKey: "cancelled"}, "worker-a", "", "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("claim error=%v, want context.Canceled", err)
	}
}

func TestAutomationInstructionConcurrentClaimHasSingleWinner(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewAutomationWorkerStore(store)
	request := automationmodel.AutomationInstructionExecution{WorkspaceID: "default", IdempotencyKey: "concurrent-claim", RuleKey: "rule", ObjectKey: "customer", RecordID: "customer_1", RecordVersion: "v1", Operation: "update", InstructionKey: "notify"}
	start := make(chan struct{})
	results := make(chan bool, 100)
	errorsFound := make(chan error, 100)
	var group sync.WaitGroup
	for range 100 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, claimed, err := repository.ClaimInstruction(t.Context(), request.WorkspaceID, request, "worker-concurrent", "2026-01-01T00:00:00Z", "2026-01-01T00:05:00Z")
			if err != nil {
				errorsFound <- err
				return
			}
			results <- claimed
		}()
	}
	close(start)
	group.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatalf("concurrent claim: %v", err)
	}
	winners := 0
	for claimed := range results {
		if claimed {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("expected one automation claim winner, got %d", winners)
	}
}

func TestAutomationInstructionStaleLeaseCannotCompleteReclaimedWork(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewAutomationWorkerStore(store)
	request := automationmodel.AutomationInstructionExecution{WorkspaceID: "default", IdempotencyKey: "rule:record:instruction", RuleKey: "rule", ObjectKey: "customer", RecordID: "customer_1", RecordVersion: "v1", Operation: "update", InstructionKey: "notify"}
	first, claimed, err := repository.ClaimInstruction(t.Context(), request.WorkspaceID, request, "worker-a", "2026-01-01T00:00:00Z", "2026-01-01T00:01:00Z")
	if err != nil || !claimed {
		t.Fatalf("first claim: claimed=%v err=%v", claimed, err)
	}
	first, err = repository.HeartbeatInstruction(t.Context(), request.WorkspaceID, request.IdempotencyKey, first.LeaseOwner, first.FencingToken, "2026-01-01T00:02:30Z", "2026-01-01T00:01:00Z")
	if err != nil || first.LeaseExpiresAt != "2026-01-01T00:02:30Z" || first.LeaseOwner == "" {
		t.Fatalf("heartbeat instruction: %#v err=%v", first, err)
	}
	second, claimed, err := repository.ClaimInstruction(t.Context(), request.WorkspaceID, request, "worker-b", "2026-01-01T00:03:00Z", "2026-01-01T00:04:00Z")
	if err != nil || !claimed {
		t.Fatalf("reclaim expired lease: claimed=%v err=%v", claimed, err)
	}
	if second.FencingToken != first.FencingToken+1 {
		t.Fatalf("reclaim fencing token=%d want %d", second.FencingToken, first.FencingToken+1)
	}
	if _, err := repository.CompleteInstruction(t.Context(), request.WorkspaceID, request.IdempotencyKey, first.LeaseOwner, first.FencingToken, "succeeded", map[string]any{"worker": "stale"}, "", "2026-01-01T00:06:00Z"); err == nil {
		t.Fatal("expected stale worker completion to lose lease")
	}
	completed, err := repository.CompleteInstruction(t.Context(), request.WorkspaceID, request.IdempotencyKey, second.LeaseOwner, second.FencingToken, "succeeded", map[string]any{"worker": "owner"}, "", "2026-01-01T00:06:00Z")
	if err != nil {
		t.Fatalf("complete current lease: %v", err)
	}
	if completed.Status != "succeeded" || completed.Result["worker"] != "owner" {
		t.Fatalf("unexpected completion: %#v", completed)
	}
}
