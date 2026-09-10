package workflow

import (
	"reflect"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestConversationWorkflowReceiptNeverReclaimsExpiredStart(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewWorkflowWorkerStore(store)
	now := time.Now().UTC()
	request := workflowmodel.WorkflowExecutionClaimRequest{Receipt: workflowmodel.WorkflowExecutionReceipt{WorkspaceID: "workspace", WorkflowKey: "review", IdempotencyKey: "start"}, RequestFingerprint: "frozen", LeaseOwner: "first", LeaseTTL: time.Minute, Now: now, PreventReclaim: true}
	first, err := repository.TryBeginExecution(t.Context(), request)
	if err != nil || first.Decision != idempotency.DecisionAcquired {
		t.Fatal(first, err)
	}
	before, found, err := repository.FindExecutionReceipt(t.Context(), "workspace", "review", "start")
	if err != nil || !found {
		t.Fatal(before, found, err)
	}
	request.Now, request.LeaseOwner = now.Add(10*time.Minute), "recovery"
	claim, err := repository.TryBeginExecution(t.Context(), request)
	if err != nil || claim.Decision != idempotency.DecisionInProgress || !reflect.DeepEqual(claim.Receipt, before) {
		t.Fatal("expired start was reclaimed", claim, err)
	}
	after, found, err := repository.FindExecutionReceipt(t.Context(), "workspace", "review", "start")
	if err != nil || !found || !reflect.DeepEqual(before, after) {
		t.Fatal("inspection changed the receipt", after, err)
	}
	if _, found, err := repository.FindExecutionReceipt(t.Context(), "other", "review", "start"); err != nil || found {
		t.Fatal("receipt crossed workspace", found, err)
	}
	request.RequestFingerprint = "changed"
	if claim, err := repository.TryBeginExecution(t.Context(), request); err != nil || claim.Decision != idempotency.DecisionFingerprintConflict {
		t.Fatal(claim, err)
	}
	completion := workflowmodel.WorkflowExecutionReceiptCompletion{WorkspaceID: "workspace", ReceiptID: before.ID, ExecutionID: "process-1", LeaseOwner: before.LeaseOwner, FencingToken: before.FencingToken, Now: now, ExpiresAt: now.Add(time.Hour)}
	if err := repository.CompleteExecutionReceipt(t.Context(), completion); err != nil {
		t.Fatal(err)
	}
	request.RequestFingerprint = "frozen"
	if claim, err := repository.TryBeginExecution(t.Context(), request); err != nil || claim.Decision != idempotency.DecisionReplay || claim.Receipt.ExecutionID != "process-1" {
		t.Fatal("completed start was not replayed", claim, err)
	}
}
