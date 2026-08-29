package transaction

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/mutation"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestBoundaryIntentReconciliationAndCompensationStateMachine(t *testing.T) {
	store := openBoundaryIntentStore(t)
	intent, duplicate, err := store.CreateBoundaryIntent(t.Context(), transactionmodel.BoundaryIntent{WorkspaceID: "workspace-a", Owner: "integration", Operation: "provider_reserve", ResourceID: "order-1", IdempotencyKey: "reserve-1", Payload: map[string]any{"order_id": "order-1"}, CompensationPayload: map[string]any{"operation": "release"}})
	if err != nil || duplicate || intent.Status != transactionmodel.BoundaryIntentPending {
		t.Fatalf("create intent=%+v duplicate=%v err=%v", intent, duplicate, err)
	}
	if replay, duplicate, err := store.CreateBoundaryIntent(t.Context(), transactionmodel.BoundaryIntent{WorkspaceID: intent.WorkspaceID, Owner: "integration", Operation: "provider_reserve", IdempotencyKey: "reserve-1"}); err != nil || !duplicate || replay.ID != intent.ID {
		t.Fatalf("replay=%+v duplicate=%v err=%v", replay, duplicate, err)
	}
	claimed, ok, err := store.ClaimBoundaryIntent(t.Context(), intent.WorkspaceID, intent.ID, "worker-1", time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil || !ok || claimed.Status != transactionmodel.BoundaryIntentExecuting || claimed.FencingToken != 1 {
		t.Fatalf("claim=%+v ok=%v err=%v", claimed, ok, err)
	}
	reconcile, err := store.TransitionBoundaryIntent(t.Context(), intent.WorkspaceID, intent.ID, claimed.LeaseOwner, claimed.FencingToken, transactionmodel.BoundaryIntentReconciliationRequired, "provider outcome unknown", "")
	if err != nil || reconcile.Status != transactionmodel.BoundaryIntentReconciliationRequired || reconcile.AttemptCount != 1 {
		t.Fatalf("reconcile=%+v err=%v", reconcile, err)
	}
	compensating, err := store.TransitionBoundaryIntent(t.Context(), intent.WorkspaceID, intent.ID, "", reconcile.FencingToken, transactionmodel.BoundaryIntentCompensating, "", "")
	if err != nil || compensating.Status != transactionmodel.BoundaryIntentCompensating {
		t.Fatalf("compensating=%+v err=%v", compensating, err)
	}
	compensated, err := store.TransitionBoundaryIntent(t.Context(), intent.WorkspaceID, intent.ID, "", compensating.FencingToken, transactionmodel.BoundaryIntentCompensated, "", "")
	if err != nil || compensated.Status != transactionmodel.BoundaryIntentCompensated {
		t.Fatalf("compensated=%+v err=%v", compensated, err)
	}
}

func TestBoundaryIntentInputAndStorageBoundaries(t *testing.T) {
	store := openBoundaryIntentStore(t)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := store.CreateBoundaryIntent(cancelled, transactionmodel.BoundaryIntent{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled create=%v", err)
	}
	for _, intent := range []transactionmodel.BoundaryIntent{
		{},
		{Owner: "owner"},
		{Owner: "owner", Operation: "operation"},
	} {
		if _, _, err := store.CreateBoundaryIntent(t.Context(), intent); err == nil {
			t.Fatalf("incomplete intent accepted: %+v", intent)
		}
	}
	explicit, duplicate, err := store.CreateBoundaryIntent(t.Context(), transactionmodel.BoundaryIntent{ID: "explicit", WorkspaceID: "workspace-a", Owner: "owner", Operation: "operation", IdempotencyKey: "key", Status: transactionmodel.BoundaryIntentPending})
	if err != nil || duplicate || explicit.ID != "explicit" || explicit.WorkspaceID != "workspace-a" || explicit.Status != transactionmodel.BoundaryIntentPending {
		t.Fatalf("explicit intent=%+v duplicate=%v err=%v", explicit, duplicate, err)
	}
	if _, _, err := store.CreateBoundaryIntent(t.Context(), transactionmodel.BoundaryIntent{Owner: "owner", Operation: "payload", IdempotencyKey: "key", Payload: map[string]any{"bad": make(chan int)}}); err == nil {
		t.Fatal("invalid payload accepted")
	}
	if _, _, err := store.CreateBoundaryIntent(t.Context(), transactionmodel.BoundaryIntent{Owner: "owner", Operation: "compensation", IdempotencyKey: "key", CompensationPayload: map[string]any{"bad": make(chan int)}}); err == nil {
		t.Fatal("invalid compensation accepted")
	}

	for _, claim := range []struct{ workspaceID, id, owner string }{{workspaceID: "workspace-a", id: "", owner: "worker"}, {workspaceID: "workspace-a", id: "explicit", owner: ""}, {id: "explicit", owner: "worker"}} {
		if _, _, err := store.ClaimBoundaryIntent(t.Context(), claim.workspaceID, claim.id, claim.owner, time.Now().UTC().Format(time.RFC3339)); err == nil {
			t.Fatalf("invalid claim accepted: %+v", claim)
		}
	}
	if _, _, err := store.ClaimBoundaryIntent(t.Context(), explicit.WorkspaceID, "explicit", "worker", "invalid"); err == nil {
		t.Fatal("invalid claim time accepted")
	}
	if intent, claimed, err := store.ClaimBoundaryIntent(t.Context(), explicit.WorkspaceID, "missing", "worker", time.Now().UTC().Format(time.RFC3339)); err != nil || claimed || intent.ID != "" {
		t.Fatalf("missing claim=%+v claimed=%v err=%v", intent, claimed, err)
	}
	if _, found, err := store.GetBoundaryIntent(t.Context(), explicit.WorkspaceID, "missing"); err != nil || found {
		t.Fatalf("missing get found=%v err=%v", found, err)
	}
	if _, err := store.TransitionBoundaryIntent(t.Context(), explicit.WorkspaceID, "missing", "worker", 1, transactionmodel.BoundaryIntentSucceeded, "", ""); err == nil {
		t.Fatal("missing transition accepted")
	}

	claimed, ok, err := store.ClaimBoundaryIntent(t.Context(), explicit.WorkspaceID, explicit.ID, "worker", time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil || !ok {
		t.Fatalf("explicit claim=%+v ok=%v err=%v", claimed, ok, err)
	}
	reconcile, err := store.TransitionBoundaryIntent(t.Context(), explicit.WorkspaceID, explicit.ID, claimed.LeaseOwner, claimed.FencingToken, transactionmodel.BoundaryIntentReconciliationRequired, "uncertain", "")
	if err != nil {
		t.Fatalf("reconciliation=%+v err=%v", reconcile, err)
	}
	manual, err := store.TransitionBoundaryIntent(t.Context(), explicit.WorkspaceID, explicit.ID, "", reconcile.FencingToken, transactionmodel.BoundaryIntentManualReview, "review", "")
	if err != nil || manual.AttemptCount != 2 {
		t.Fatalf("manual review=%+v err=%v", manual, err)
	}
}

func TestBoundaryIntentDatabaseFailures(t *testing.T) {
	store := openBoundaryIntentStore(t)
	if err := store.store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateBoundaryIntent(t.Context(), transactionmodel.BoundaryIntent{WorkspaceID: "workspace-a", Owner: "owner", Operation: "operation", IdempotencyKey: "key"}); err == nil || !strings.Contains(err.Error(), "insert boundary intent") {
		t.Fatalf("create database error=%v", err)
	}
	if _, _, err := store.ClaimBoundaryIntent(t.Context(), "workspace-a", "id", "worker", time.Now().UTC().Format(time.RFC3339)); err == nil || !strings.Contains(err.Error(), "claim boundary intent") {
		t.Fatalf("claim database error=%v", err)
	}
	if _, _, err := store.GetBoundaryIntent(t.Context(), "workspace-a", "id"); err == nil {
		t.Fatal("closed database get succeeded")
	}
}

func TestBoundaryIntentInsertFailureWithoutReplay(t *testing.T) {
	store := openBoundaryIntentStore(t)
	_, err := store.store.DB().ExecContext(t.Context(), `CREATE TRIGGER fail_boundary_intent_insert BEFORE INSERT ON transaction_boundary_intents BEGIN SELECT RAISE(FAIL, 'injected insert failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if _, duplicate, err := store.CreateBoundaryIntent(t.Context(), transactionmodel.BoundaryIntent{WorkspaceID: "workspace-a", Owner: "owner", Operation: "operation", IdempotencyKey: "key"}); err == nil || duplicate || !strings.Contains(err.Error(), "insert boundary intent") {
		t.Fatalf("insert failure duplicate=%v err=%v", duplicate, err)
	}
}

func TestBoundaryIntentRejectsStaleFencingAndIllegalTransition(t *testing.T) {
	store := openBoundaryIntentStore(t)
	intent, _, err := store.CreateBoundaryIntent(t.Context(), transactionmodel.BoundaryIntent{WorkspaceID: "workspace-a", Owner: "metadata", Operation: "runtime_refresh", ResourceID: "object:account", IdempotencyKey: "hash-1"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := store.ClaimBoundaryIntent(t.Context(), intent.WorkspaceID, intent.ID, "worker-1", time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil || !ok {
		t.Fatalf("claim=%+v ok=%v err=%v", claimed, ok, err)
	}
	if _, err := store.TransitionBoundaryIntent(t.Context(), intent.WorkspaceID, intent.ID, "worker-1", 0, transactionmodel.BoundaryIntentSucceeded, "", ""); !mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
		t.Fatalf("stale fencing error=%v", err)
	}
	if _, err := store.TransitionBoundaryIntent(t.Context(), intent.WorkspaceID, intent.ID, "worker-1", claimed.FencingToken, transactionmodel.BoundaryIntentCompensated, "", ""); err == nil {
		t.Fatal("expected illegal transition error")
	}
}

func openBoundaryIntentStore(t *testing.T) BoundaryIntentStore {
	t.Helper()
	runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "boundary-intent.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeStore.Close() })
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	return NewBoundaryIntentStore(runtimeStore)
}
