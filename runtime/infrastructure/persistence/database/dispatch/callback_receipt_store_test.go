package dispatch

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
	dispatchmodel "github.com/domainry/domainry-runtime/runtime/domain/dispatch/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestCallbackReceiptStoreReplaysConflictsAndFencesCompletion(t *testing.T) {
	repository := openCallbackReceiptStore(t)
	now := time.Date(2026, time.September, 7, 1, 2, 3, 0, time.UTC)
	request := callbackClaimRequest("key-1", callbackBodyHash(`{"value":1}`), "worker-1", now)

	first, err := repository.TryBeginCallback(t.Context(), request)
	if err != nil || first.Decision != idempotency.DecisionAcquired || first.Receipt.FencingToken != 1 {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	var sharedRows int
	if err := repository.store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _operations WHERE owner = 'dispatch' AND kind = 'dispatch.callback'`).Scan(&sharedRows); err != nil || sharedRows != 1 {
		t.Fatalf("shared operation rows=%d err=%v", sharedRows, err)
	}
	var legacyTables int
	if err := repository.store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = '_dispatch_callback_receipts'`).Scan(&legacyTables); err != nil || legacyTables != 0 {
		t.Fatalf("legacy callback receipt tables=%d err=%v", legacyTables, err)
	}
	active, err := repository.TryBeginCallback(t.Context(), request)
	if err != nil || active.Decision != idempotency.DecisionInProgress {
		t.Fatalf("active=%#v err=%v", active, err)
	}
	conflicting := request
	conflicting.Receipt.BodySHA256 = callbackBodyHash(`{"value":2}`)
	conflict, err := repository.TryBeginCallback(t.Context(), conflicting)
	if err != nil || conflict.Decision != idempotency.DecisionFingerprintConflict {
		t.Fatalf("conflict=%#v err=%v", conflict, err)
	}

	wrongFence := dispatchmodel.CallbackCompletion{
		WorkspaceID: first.Receipt.WorkspaceID, ReceiptID: first.Receipt.ID,
		DownstreamID: "receipt-1", DownstreamOwner: "workflow", DownstreamStatus: "accepted",
		LeaseOwner: first.Receipt.LeaseOwner, FencingToken: first.Receipt.FencingToken + 1,
		Now: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := repository.CompleteCallback(t.Context(), wrongFence); !mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
		t.Fatalf("wrong fence error=%v", err)
	}
	wrongFence.FencingToken = first.Receipt.FencingToken
	if err := repository.CompleteCallback(t.Context(), wrongFence); err != nil {
		t.Fatal(err)
	}
	replay, err := repository.TryBeginCallback(t.Context(), request)
	if err != nil || replay.Decision != idempotency.DecisionReplay || replay.Receipt.ExecutionID != "execution-1" || replay.Receipt.DownstreamID != "receipt-1" || replay.Receipt.DownstreamOwner != "workflow" || replay.Receipt.DownstreamStatus != "accepted" {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
}

func TestCallbackReceiptStoreReclaimsRetryableAndExpiredClaimsWithFencing(t *testing.T) {
	repository := openCallbackReceiptStore(t)
	now := time.Date(2026, time.September, 7, 2, 3, 4, 0, time.UTC)

	retryableRequest := callbackClaimRequest("retryable", callbackBodyHash(`{"retry":true}`), "worker-1", now)
	retryable, err := repository.TryBeginCallback(t.Context(), retryableRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.FailCallbackRetryable(t.Context(), dispatchmodel.CallbackFailure{
		WorkspaceID: retryable.Receipt.WorkspaceID, ReceiptID: retryable.Receipt.ID,
		LeaseOwner: retryable.Receipt.LeaseOwner, FencingToken: retryable.Receipt.FencingToken,
		Now: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	retryableRequest.LeaseOwner = "worker-2"
	reclaimedRetryable, err := repository.TryBeginCallback(t.Context(), retryableRequest)
	if err != nil || reclaimedRetryable.Decision != idempotency.DecisionAcquired || reclaimedRetryable.Receipt.FencingToken != 2 || reclaimedRetryable.Receipt.LeaseOwner != "worker-2" {
		t.Fatalf("retryable reclaim=%#v err=%v", reclaimedRetryable, err)
	}

	expiredRequest := callbackClaimRequest("expired", callbackBodyHash(`{"expired":true}`), "worker-1", now)
	expired, err := repository.TryBeginCallback(t.Context(), expiredRequest)
	if err != nil {
		t.Fatal(err)
	}
	expiredRequest.LeaseOwner, expiredRequest.Now = "worker-3", now.Add(2*time.Minute)
	reclaimedExpired, err := repository.TryBeginCallback(t.Context(), expiredRequest)
	if err != nil || reclaimedExpired.Decision != idempotency.DecisionAcquired || reclaimedExpired.Receipt.FencingToken != 2 || reclaimedExpired.Receipt.LeaseOwner != "worker-3" {
		t.Fatalf("expired reclaim=%#v err=%v", reclaimedExpired, err)
	}
	stale := dispatchmodel.CallbackCompletion{
		WorkspaceID: expired.Receipt.WorkspaceID, ReceiptID: expired.Receipt.ID,
		DownstreamID: "stale", DownstreamStatus: "accepted", LeaseOwner: expired.Receipt.LeaseOwner,
		FencingToken: expired.Receipt.FencingToken, Now: expiredRequest.Now, ExpiresAt: expiredRequest.Now.Add(time.Hour),
	}
	if err := repository.CompleteCallback(t.Context(), stale); !mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
		t.Fatalf("stale completion error=%v", err)
	}
	alive, err := repository.HeartbeatCallback(t.Context(), dispatchmodel.CallbackHeartbeat{
		WorkspaceID: reclaimedExpired.Receipt.WorkspaceID, ReceiptID: reclaimedExpired.Receipt.ID,
		LeaseOwner: reclaimedExpired.Receipt.LeaseOwner, FencingToken: reclaimedExpired.Receipt.FencingToken,
		LeaseTTL: time.Minute, Now: expiredRequest.Now.Add(30 * time.Second),
	})
	if err != nil || !alive {
		t.Fatalf("heartbeat alive=%t err=%v", alive, err)
	}
}

func openCallbackReceiptStore(t *testing.T) *CallbackReceiptStore {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "callback.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	return NewCallbackReceiptStore(store)
}

func callbackClaimRequest(key, fingerprint, owner string, now time.Time) dispatchmodel.CallbackClaimRequest {
	return dispatchmodel.CallbackClaimRequest{
		Receipt: dispatchmodel.CallbackReceipt{
			WorkspaceID: "workspace-primary", RuntimeID: "runtime-a", Method: "POST", Path: "/dispatch/executions",
			IdempotencyKey: key, BodySHA256: fingerprint, ExecutionID: "execution-1",
		},
		LeaseOwner: owner, LeaseTTL: time.Minute, Now: now,
	}
}

func callbackBodyHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}
