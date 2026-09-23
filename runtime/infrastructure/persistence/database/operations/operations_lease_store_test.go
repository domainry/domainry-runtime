package operations

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestOperationsLeaseSnapshotReportsOnlyTargetInstance(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "lease-snapshot.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	insert := store.InsertStatement("_worker_scopes", []string{"id", "owner", "scope_key", "lease_owner", "lease_expires_at", "fencing_token", "last_started_at", "last_completed_at", "checkpoint", "last_error", "updated_at"})
	for _, row := range [][]any{
		{"live", "idempotency_cleanup", "live", "instance-a", now.Add(time.Minute).Format(time.RFC3339Nano), 1, "", "", 0, "", now.Format(time.RFC3339Nano)},
		{"expired", "idempotency_cleanup", "expired", "instance-a:cleanup", now.Add(-time.Minute).Format(time.RFC3339Nano), 2, "", "", 0, "", now.Format(time.RFC3339Nano)},
		{"other", "idempotency_cleanup", "other", "instance-b", now.Add(time.Minute).Format(time.RFC3339Nano), 1, "", "", 0, "", now.Format(time.RFC3339Nano)},
		{"notification", "notification_inbox", "workspace-a", "instance-a", now.Add(time.Minute).Format(time.RFC3339Nano), 1, "", "", 0, "", now.Format(time.RFC3339Nano)},
	} {
		if _, err := store.DB().ExecContext(t.Context(), insert, row...); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := NewOperationsStore(store).OperationsLeaseSnapshot(t.Context(), "instance-a", now)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Live != 1 || snapshot.Expired != 1 || len(snapshot.Owners) != 1 || snapshot.Owners[0].Owner != "idempotency_cleanup" {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "table") || strings.Contains(string(raw), "_worker_scopes") {
		t.Fatalf("lease recovery response leaked physical storage: %s", raw)
	}
	operations := NewOperationsStore(store)
	released, changed, err := operations.ForceReleaseOperationsLease(t.Context(), operationsmodel.OperationsLeaseReleaseRequest{Owner: "idempotency_cleanup", ResourceID: "expired", ExpectedLeaseOwner: "instance-a:cleanup", ExpectedFencingToken: 2, Now: now})
	if err != nil || !changed || released.Eligibility != "expired" || released.NextFencingToken != 3 {
		t.Fatalf("expired release=%#v changed=%v err=%v", released, changed, err)
	}
	if _, changed, err := operations.ForceReleaseOperationsLease(t.Context(), operationsmodel.OperationsLeaseReleaseRequest{Owner: "idempotency_cleanup", ResourceID: "live", ExpectedLeaseOwner: "instance-a", ExpectedFencingToken: 1, Now: now}); err != nil || changed {
		t.Fatalf("live lease released without verification changed=%v err=%v", changed, err)
	}
	released, changed, err = operations.ForceReleaseOperationsLease(t.Context(), operationsmodel.OperationsLeaseReleaseRequest{Owner: "idempotency_cleanup", ResourceID: "live", ExpectedLeaseOwner: "instance-a", ExpectedFencingToken: 1, VerifiedStuck: true, VerificationEvidence: "heartbeat absent and process terminated", Now: now})
	if err != nil || !changed || released.Eligibility != "verified_stuck" || released.NextFencingToken != 2 {
		t.Fatalf("stuck release=%#v changed=%v err=%v", released, changed, err)
	}
	result, err := store.DB().ExecContext(t.Context(), "UPDATE _worker_scopes SET last_error = 'stale' WHERE id = ? AND lease_owner = ? AND fencing_token = ?", "live", "instance-a", 1)
	if err != nil {
		t.Fatal(err)
	}
	if rows, _ := result.RowsAffected(); rows != 0 {
		t.Fatalf("old owner/token mutated released lease rows=%d", rows)
	}
}
