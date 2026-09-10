package action

import (
	"reflect"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
)

func TestBusinessActionConversationRecoveryNeverReclaimsExistingExecution(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repo := NewActionBusinessExecutionStore(store)
	now := time.Now().UTC()
	request := actionmodel.ActionExecutionClaimRequest{Execution: actionmodel.ActionBusinessExecution{WorkspaceID: "workspace-a", ObjectKey: "customer", RecordID: "c1", ActionKey: "customer.rename", IdempotencyKey: "conversation-call", ActorID: "operator"}, RequestFingerprint: "original", LeaseOwner: "original-worker", LeaseTTL: time.Second, Now: now, PreventReclaim: true}
	first, err := repo.TryBeginExecution(t.Context(), request)
	if err != nil || first.Decision != idempotency.DecisionAcquired {
		t.Fatal(first, err)
	}
	// Compare persisted receipts on both sides; the initial insert returns a
	// nil Result while the persisted empty JSON object reads as an empty map.
	baseline, found, err := repo.FindExecution(t.Context(), request.Execution)
	if err != nil || !found {
		t.Fatal(baseline, found, err)
	}
	request.Now, request.LeaseOwner = now.Add(time.Hour), "recovery-worker"
	recovered, err := repo.TryBeginExecution(t.Context(), request)
	if err != nil || recovered.Decision != idempotency.DecisionInProgress || !reflect.DeepEqual(recovered.Execution, baseline) {
		t.Fatal("expired execution was changed by recovery", recovered, err)
	}
	read, found, err := repo.FindExecution(t.Context(), request.Execution)
	if err != nil || !found || !reflect.DeepEqual(read, baseline) {
		t.Fatal("lookup changed receipt", read, found, err)
	}
	missing := request.Execution
	missing.WorkspaceID = "workspace-b"
	if _, found, err := repo.FindExecution(t.Context(), missing); err != nil || found {
		t.Fatal("cross-workspace receipt lookup", found, err)
	}
	completion := actionmodel.ActionExecutionCompletion{Execution: first.Execution, ExecutionID: first.Execution.ID, LeaseOwner: first.Execution.LeaseOwner, FencingToken: first.Execution.FencingToken, Result: map[string]any{"status": "failed"}, ErrorCode: "connector_unknown", Retryable: true, ResponseStatus: 503, Now: now.Add(time.Second), ExpiresAt: now.Add(time.Hour)}
	if _, err := repo.CompleteExecution(t.Context(), completion); err != nil {
		t.Fatal(err)
	}
	recovered, err = repo.TryBeginExecution(t.Context(), request)
	if err != nil || recovered.Decision != idempotency.DecisionInProgress || recovered.Execution.FencingToken != first.Execution.FencingToken {
		t.Fatal("retryable receipt was reclaimed", recovered, err)
	}
	request.Execution.IdempotencyKey = "new-call"
	if fresh, err := repo.TryBeginExecution(t.Context(), request); err != nil || fresh.Decision != idempotency.DecisionAcquired {
		t.Fatal("absent call could not start", fresh, err)
	}
}
