package changeplan

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func openStoreForGeneratedListTest(t *testing.T) *database.RuntimeStore {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "change-plan.db")})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

type contextBusinessChangePlanContract interface {
	GetDraft(context.Context, string, string) (changeplanmodel.BusinessChangePlanDraft, bool, error)
	SaveDraft(context.Context, string, changeplanmodel.BusinessChangePlanDraft, int) (changeplanmodel.BusinessChangePlanDraft, bool, error)
	PublishDraft(context.Context, string, string, int, string, string) (changeplanmodel.BusinessChangePlanDraft, bool, error)
	TransitionDraft(context.Context, string, string, int, string, string, string, string) (changeplanmodel.BusinessChangePlanDraft, bool, error)
}

var _ contextBusinessChangePlanContract = BusinessChangePlanStore{}

var _ interface {
	TryBeginOperation(context.Context, string, changeplanmodel.ChangePlanOperationClaimRequest) (changeplanmodel.ChangePlanOperationClaimResult, error)
	CompleteOperation(context.Context, string, changeplanmodel.ChangePlanOperationCompletion) (changeplanmodel.ChangePlanOperationExecution, error)
	FailOperation(context.Context, string, changeplanmodel.ChangePlanOperationFailure) (changeplanmodel.ChangePlanOperationExecution, error)
} = BusinessChangePlanStore{}

func TestBusinessChangePlanStoreContractAndCancellation(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewBusinessChangePlanStore(store)
	draft, saved, err := repository.SaveDraft(t.Context(), "default", changeplanmodel.BusinessChangePlanDraft{PlanID: "plan-context", Payload: []byte(`{"plan_id":"plan-context"}`), CreatedBy: "admin", UpdatedBy: "admin", CreatedAt: "2026-07-12T00:00:00Z", UpdatedAt: "2026-07-12T00:00:00Z"}, 0)
	if err != nil || !saved || draft.Revision != 1 {
		t.Fatalf("save draft=%#v saved=%v err=%v", draft, saved, err)
	}
	inReview, ok, err := repository.TransitionDraft(t.Context(), "default", draft.PlanID, draft.Revision, "draft", "in_review", "reviewer", "2026-07-12T00:00:30Z")
	if err != nil || !ok {
		t.Fatalf("review draft=%#v ok=%v err=%v", inReview, ok, err)
	}
	approved, ok, err := repository.TransitionDraft(t.Context(), "default", draft.PlanID, inReview.Revision, "in_review", "approved", "approver", "2026-07-12T00:00:45Z")
	if err != nil || !ok {
		t.Fatalf("approve draft=%#v ok=%v err=%v", approved, ok, err)
	}
	applying, ok, err := repository.TransitionDraft(t.Context(), "default", draft.PlanID, approved.Revision, "approved", "applying", "publisher", "2026-07-12T00:00:55Z")
	if err != nil || !ok {
		t.Fatalf("apply draft=%#v ok=%v err=%v", applying, ok, err)
	}
	published, ok, err := repository.PublishDraft(t.Context(), "default", draft.PlanID, applying.Revision, "admin", "2026-07-12T00:01:00Z")
	if err != nil || !ok || published.Status != "published" {
		t.Fatalf("publish draft=%#v ok=%v err=%v", published, ok, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := repository.GetDraft(cancelled, "default", draft.PlanID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled get error=%v", err)
	}
	if _, _, err := repository.SaveDraft(cancelled, "default", changeplanmodel.BusinessChangePlanDraft{PlanID: "never"}, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled save error=%v", err)
	}
}

func TestBusinessChangePlanDraftIdentityIsWorkspaceScoped(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewBusinessChangePlanStore(store)
	for _, workspaceID := range []string{"workspace-a", "workspace-b"} {
		draft := changeplanmodel.BusinessChangePlanDraft{PlanID: "shared-plan", Payload: []byte(`{"workspace":"` + workspaceID + `"}`), CreatedBy: workspaceID, UpdatedBy: workspaceID, CreatedAt: "2026-07-21T00:00:00Z", UpdatedAt: "2026-07-21T00:00:00Z"}
		saved, ok, err := repository.SaveDraft(t.Context(), workspaceID, draft, 0)
		if err != nil || !ok || saved.WorkspaceID != workspaceID || saved.PlanID != draft.PlanID {
			t.Fatalf("workspace=%s saved=%#v ok=%v err=%v", workspaceID, saved, ok, err)
		}
	}
	inReview, ok, err := repository.TransitionDraft(t.Context(), "workspace-a", "shared-plan", 1, "draft", "in_review", "reviewer-a", "2026-07-21T00:00:20Z")
	if err != nil || !ok {
		t.Fatalf("reviewed=%#v ok=%v err=%v", inReview, ok, err)
	}
	approved, ok, err := repository.TransitionDraft(t.Context(), "workspace-a", "shared-plan", inReview.Revision, "in_review", "approved", "approver-a", "2026-07-21T00:00:30Z")
	if err != nil || !ok {
		t.Fatalf("approved=%#v ok=%v err=%v", approved, ok, err)
	}
	applying, ok, err := repository.TransitionDraft(t.Context(), "workspace-a", "shared-plan", approved.Revision, "approved", "applying", "publisher-a", "2026-07-21T00:00:40Z")
	if err != nil || !ok {
		t.Fatalf("applying=%#v ok=%v err=%v", applying, ok, err)
	}
	published, ok, err := repository.PublishDraft(t.Context(), "workspace-a", "shared-plan", applying.Revision, "admin-a", "2026-07-21T00:01:00Z")
	if err != nil || !ok || published.Status != "published" {
		t.Fatalf("published=%#v ok=%v err=%v", published, ok, err)
	}
	other, found, err := repository.GetDraft(t.Context(), "workspace-b", "shared-plan")
	if err != nil || !found || other.Status != "draft" || other.CreatedBy != "workspace-b" {
		t.Fatalf("other workspace draft leaked or changed: draft=%#v found=%v err=%v", other, found, err)
	}
}

func TestBusinessChangePlanOperationClaimReplayConflictAndReclaim(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewBusinessChangePlanStore(store)
	base := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	request := changeplanmodel.ChangePlanOperationClaimRequest{
		Execution:          changeplanmodel.ChangePlanOperationExecution{WorkspaceID: "workspace-1", PlanID: "plan-1", PlanRevision: 3, Operation: "apply", IdempotencyKey: "operation-key", ActorID: "admin"},
		RequestFingerprint: "fingerprint-1", LeaseOwner: "worker-1", LeaseTTL: time.Minute, Now: base,
	}
	first, err := repository.TryBeginOperation(t.Context(), request.Execution.WorkspaceID, request)
	if err != nil || first.Decision != idempotency.DecisionAcquired || first.Execution.FencingToken != 1 {
		t.Fatalf("first claim=%#v err=%v", first, err)
	}
	live, err := repository.TryBeginOperation(t.Context(), request.Execution.WorkspaceID, request)
	if err != nil || live.Decision != idempotency.DecisionInProgress {
		t.Fatalf("live claim=%#v err=%v", live, err)
	}
	conflicting := request
	conflicting.RequestFingerprint = "fingerprint-2"
	conflict, err := repository.TryBeginOperation(t.Context(), conflicting.Execution.WorkspaceID, conflicting)
	if err != nil || conflict.Decision != idempotency.DecisionFingerprintConflict {
		t.Fatalf("conflict claim=%#v err=%v", conflict, err)
	}
	reclaimRequest := request
	reclaimRequest.LeaseOwner, reclaimRequest.Now = "worker-2", base.Add(2*time.Minute)
	reclaimed, err := repository.TryBeginOperation(t.Context(), reclaimRequest.Execution.WorkspaceID, reclaimRequest)
	if err != nil || reclaimed.Decision != idempotency.DecisionAcquired || reclaimed.Execution.FencingToken != 2 {
		t.Fatalf("reclaimed=%#v err=%v", reclaimed, err)
	}
	if _, err := repository.CompleteOperation(t.Context(), first.Execution.WorkspaceID, changeplanmodel.ChangePlanOperationCompletion{ExecutionID: first.Execution.ID, LeaseOwner: first.Execution.LeaseOwner, FencingToken: first.Execution.FencingToken, Result: map[string]any{"status": "stale"}, Now: base.Add(2 * time.Minute)}); !mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
		t.Fatalf("stale owner error=%v", err)
	}
	completed, err := repository.CompleteOperation(t.Context(), reclaimed.Execution.WorkspaceID, changeplanmodel.ChangePlanOperationCompletion{ExecutionID: reclaimed.Execution.ID, LeaseOwner: reclaimed.Execution.LeaseOwner, FencingToken: reclaimed.Execution.FencingToken, Result: map[string]any{"status": "applied"}, ExpiresAt: base.Add(30 * 24 * time.Hour), Now: base.Add(2 * time.Minute)})
	if err != nil || completed.Status != string(idempotency.StatusSucceeded) {
		t.Fatalf("completed=%#v err=%v", completed, err)
	}
	replayed, err := repository.TryBeginOperation(t.Context(), reclaimRequest.Execution.WorkspaceID, reclaimRequest)
	if err != nil || replayed.Decision != idempotency.DecisionReplay || string(replayed.Execution.Result) != `{"status":"applied"}` {
		t.Fatalf("replayed=%#v err=%v", replayed, err)
	}
	separate := reclaimRequest
	separate.Execution.PlanRevision = 4
	separate.Execution.Operation = "rollback"
	separate.LeaseOwner = "worker-3"
	independent, err := repository.TryBeginOperation(t.Context(), separate.Execution.WorkspaceID, separate)
	if err != nil || independent.Decision != idempotency.DecisionAcquired {
		t.Fatalf("independent scope=%#v err=%v", independent, err)
	}
}

func TestBusinessChangePlanOperationWorkspaceIsolationContract(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewBusinessChangePlanStore(store)
	base := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	requestA := changeplanmodel.ChangePlanOperationClaimRequest{
		Execution:          changeplanmodel.ChangePlanOperationExecution{WorkspaceID: "workspace-a", PlanID: "shared-plan", PlanRevision: 1, Operation: "apply", IdempotencyKey: "shared-key", ActorID: "admin-a"},
		RequestFingerprint: "fingerprint-a", LeaseOwner: "worker-a", LeaseTTL: time.Minute, Now: base,
	}
	requestB := requestA
	requestB.Execution.WorkspaceID = "workspace-b"
	requestB.Execution.ActorID = "admin-b"
	requestB.RequestFingerprint = "fingerprint-b"
	requestB.LeaseOwner = "worker-b"
	claimA, err := repository.TryBeginOperation(t.Context(), "workspace-a", requestA)
	if err != nil || claimA.Decision != idempotency.DecisionAcquired {
		t.Fatalf("workspace A claim=%#v err=%v", claimA, err)
	}
	claimB, err := repository.TryBeginOperation(t.Context(), "workspace-b", requestB)
	if err != nil || claimB.Decision != idempotency.DecisionAcquired {
		t.Fatalf("workspace B claim=%#v err=%v", claimB, err)
	}
	if claimA.Execution.ID == claimB.Execution.ID {
		t.Fatalf("workspace-scoped operations reused id %q", claimA.Execution.ID)
	}
	completionA := changeplanmodel.ChangePlanOperationCompletion{ExecutionID: claimA.Execution.ID, LeaseOwner: claimA.Execution.LeaseOwner, FencingToken: claimA.Execution.FencingToken, Result: map[string]any{"workspace": "a"}, ExpiresAt: base.Add(time.Hour), Now: base.Add(time.Minute)}
	if _, err := repository.CompleteOperation(t.Context(), "workspace-b", completionA); !mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
		t.Fatalf("workspace B completed workspace A operation: %v", err)
	}
	currentA, err := repository.findOperationByID(t.Context(), "workspace-a", claimA.Execution.ID)
	if err != nil || currentA.Status != string(idempotency.StatusProcessing) {
		t.Fatalf("workspace A operation changed after cross-complete: %#v err=%v", currentA, err)
	}
	failureA := changeplanmodel.ChangePlanOperationFailure{ExecutionID: claimA.Execution.ID, LeaseOwner: claimA.Execution.LeaseOwner, FencingToken: claimA.Execution.FencingToken, ErrorCode: "failed", ExpiresAt: base.Add(time.Hour), Now: base.Add(time.Minute)}
	if _, err := repository.FailOperation(t.Context(), "workspace-b", failureA); !mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
		t.Fatalf("workspace B failed workspace A operation: %v", err)
	}
	if _, err := repository.CompleteOperation(t.Context(), "workspace-a", completionA); err != nil {
		t.Fatalf("complete workspace A operation: %v", err)
	}
	currentB, err := repository.findOperationByID(t.Context(), "workspace-b", claimB.Execution.ID)
	if err != nil || currentB.Status != string(idempotency.StatusProcessing) {
		t.Fatalf("workspace B operation changed after workspace A completion: %#v err=%v", currentB, err)
	}
	if _, err := repository.TryBeginOperation(t.Context(), "", changeplanmodel.ChangePlanOperationClaimRequest{}); err == nil {
		t.Fatal("missing operation workspace was accepted")
	}
	if _, err := repository.TryBeginOperation(t.Context(), "workspace-b", requestA); err == nil {
		t.Fatal("mismatched operation workspace was accepted")
	}
	if _, err := repository.CompleteOperation(t.Context(), "", completionA); err == nil {
		t.Fatal("missing completion workspace was accepted")
	}
	if _, err := repository.FailOperation(t.Context(), "", failureA); err == nil {
		t.Fatal("missing failure workspace was accepted")
	}
}

func TestBusinessChangePlanOperationClaimAllowsOneOfOneHundredConcurrentOwners(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewBusinessChangePlanStore(store)
	base := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	start := make(chan struct{})
	results := make(chan changeplanmodel.ChangePlanOperationClaimResult, 100)
	errorsFound := make(chan error, 100)
	var wait sync.WaitGroup
	for index := 0; index < 100; index++ {
		wait.Add(1)
		go func(owner int) {
			defer wait.Done()
			<-start
			result, err := repository.TryBeginOperation(t.Context(), "workspace-concurrent", changeplanmodel.ChangePlanOperationClaimRequest{
				Execution:          changeplanmodel.ChangePlanOperationExecution{WorkspaceID: "workspace-concurrent", PlanID: "plan-concurrent", PlanRevision: 7, Operation: "apply", IdempotencyKey: "same-key", ActorID: "admin"},
				RequestFingerprint: "same-fingerprint", LeaseOwner: fmt.Sprintf("worker-%d", owner), LeaseTTL: time.Minute, Now: base,
			})
			if err != nil {
				errorsFound <- err
				return
			}
			results <- result
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("concurrent claim: %v", err)
	}
	acquired, inProgress := 0, 0
	for result := range results {
		switch result.Decision {
		case idempotency.DecisionAcquired:
			acquired++
		case idempotency.DecisionInProgress:
			inProgress++
		default:
			t.Errorf("unexpected decision: %s", result.Decision)
		}
	}
	if acquired != 1 || inProgress != 99 {
		t.Fatalf("acquired=%d in_progress=%d", acquired, inProgress)
	}
}
