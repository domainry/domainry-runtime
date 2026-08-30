package publicationhandoff

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/mutation"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestStoreReadsOnlyIntegrationConnectorPublications(t *testing.T) {
	runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "handoff.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer runtimeStore.Close()
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO runtime_publication_outbox (id,publication_type,workspace_id,connector_key,operation,status,payload_json,dedup_key,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`
	for _, row := range [][]any{
		{"connector-1", "integration.connector", "workspace-a", "crm", "upsert", "queued", `{"id":"1"}`, "dedup-1", "2026-08-30T00:00:00Z", "2026-08-30T00:00:00Z"},
		{"notification-1", "notification.saas", "workspace-a", "notification", "publish_intent", "queued", `{}`, "dedup-2", "2026-08-30T00:00:00Z", "2026-08-30T00:00:00Z"},
	} {
		if _, err := runtimeStore.DB().ExecContext(t.Context(), insert, row...); err != nil {
			t.Fatal(err)
		}
	}
	store := NewStore(runtimeStore)
	values, err := store.ListOutbox(t.Context(), "workspace-a", "", "", 10)
	if err != nil || len(values) != 1 || values[0].ID != "connector-1" || values[0].Payload["id"] != "1" {
		t.Fatalf("values=%#v err=%v", values, err)
	}
	if _, found, err := store.GetOutbox(t.Context(), "workspace-a", "notification-1"); err != nil || found {
		t.Fatalf("notification publication leaked through Integration handoff: found=%v err=%v", found, err)
	}
}

func TestWorkerStoreOwnsClaimFencingAndCompletion(t *testing.T) {
	runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "worker.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer runtimeStore.Close()
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	ledger := NewStore(runtimeStore)
	message, err := ledger.InsertOutbox(t.Context(), "workspace-a", integrationmodel.IntegrationOutboxMessage{WorkspaceID: "workspace-a", ConnectorKey: "crm", Operation: "upsert", RequestRef: "record:worker", DedupKey: "record:worker", Payload: map[string]any{"id": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	worker := NewWorkerStore(runtimeStore)
	now := time.Now().UTC().Format(time.RFC3339)
	due, err := worker.ListDueOutbox(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test Runtime publication worker"), 10, now)
	if err != nil || len(due) != 1 || due[0].ID != message.ID {
		t.Fatalf("due=%#v err=%v", due, err)
	}
	claimed, changed, err := worker.ClaimOutbox(t.Context(), message.WorkspaceID, message.ID, "worker-a", now)
	if err != nil || !changed || claimed.Status != "sending" || claimed.FencingToken != 1 {
		t.Fatalf("claimed=%#v changed=%v err=%v", claimed, changed, err)
	}
	if _, err := worker.HeartbeatOutbox(t.Context(), message.WorkspaceID, message.ID, "worker-a", claimed.FencingToken+1, now); !mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
		t.Fatalf("stale fencing heartbeat=%v", err)
	}
	completed, err := worker.UpdateOutboxStatus(t.Context(), message.WorkspaceID, message.ID, "worker-a", claimed.FencingToken, "sent", "invocation-1", "", "", now)
	if err != nil || completed.Status != "sent" || completed.LeaseOwner != "" || completed.ResponseRef != "invocation-1" {
		t.Fatalf("completed=%#v err=%v", completed, err)
	}
}

func TestStoreOwnsIdempotentPublicationMutation(t *testing.T) {
	runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "mutation.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer runtimeStore.Close()
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := NewStore(runtimeStore)
	message := integrationmodel.IntegrationOutboxMessage{WorkspaceID: "workspace-a", ConnectorKey: "crm", ConnectionKey: "primary", Operation: "upsert", RequestRef: "record:1", DedupKey: "record:1", Payload: map[string]any{"id": "1"}}
	first, err := store.InsertOutbox(t.Context(), message.WorkspaceID, message)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.InsertOutbox(t.Context(), message.WorkspaceID, message)
	if err != nil || second.ID != first.ID {
		t.Fatalf("deduplicated publication=%#v err=%v", second, err)
	}
	message.Payload = map[string]any{"id": "different"}
	if _, err := store.InsertOutbox(t.Context(), message.WorkspaceID, message); !mutation.IsMutationConflict(err, mutation.MutationConflictIdempotency) {
		t.Fatalf("fingerprint conflict=%v", err)
	}
	updated, err := store.UpdateOutboxStatus(t.Context(), message.WorkspaceID, first.ID, "sent", "invocation-1", "")
	if err != nil || updated.Status != "sent" || updated.ResponseRef != "invocation-1" {
		t.Fatalf("updated=%#v err=%v", updated, err)
	}
	retried, err := store.ScheduleOutboxRetry(t.Context(), message.WorkspaceID, first.ID, 1, "retry")
	if err != nil || retried.Status != "queued" || retried.AttemptCount != 1 || retried.NextAttemptAt == "" {
		t.Fatalf("retried=%#v err=%v", retried, err)
	}
}
