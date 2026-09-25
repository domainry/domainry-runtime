package publicationhandoff

import (
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/mutation"
	"github.com/domainry/domainry-foundation/requestcontext"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestPublicationOutboxFreshSchemaRetainsHandoffContract(t *testing.T) {
	runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "handoff-schema.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer runtimeStore.Close()
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	rows, err := runtimeStore.DB().QueryContext(t.Context(), `PRAGMA table_info('_publication_outbox')`)
	if err != nil {
		t.Fatal(err)
	}
	columns := map[string]bool{}
	for rows.Next() {
		var sequence, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&sequence, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		columns[name] = true
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"publication_type", "workspace_id", "connector_key", "connection_key", "operation",
		"payload_json", "request_ref", "dedup_key", "request_fingerprint", "status",
		"attempt_count", "next_attempt_at", "last_attempt_at", "lease_owner", "lease_expires_at", "fencing_token",
	} {
		if !columns[required] {
			t.Errorf("publication handoff column %q is missing", required)
		}
	}
	assertPublicationIndexColumns(t, runtimeStore, "uniq_runtime_publication_dedup", []string{"workspace_id", "publication_type", "connector_key", "connection_key", "operation", "dedup_key"})
	assertPublicationIndexColumns(t, runtimeStore, "idx_runtime_publication_due", []string{"publication_type", "status", "next_attempt_at", "lease_expires_at", "created_at"})
}

func assertPublicationIndexColumns(t *testing.T, runtimeStore *database.RuntimeStore, index string, want []string) {
	t.Helper()
	rows, err := runtimeStore.DB().QueryContext(t.Context(), `PRAGMA index_info(`+runtimeStore.Identifier(index)+`)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var sequence, columnSequence int
		var column string
		if err := rows.Scan(&sequence, &columnSequence, &column); err != nil {
			t.Fatal(err)
		}
		got = append(got, column)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("publication index %s columns=%v want=%v", index, got, want)
	}
}

func TestStoreReadsOnlyIntegrationConnectorPublications(t *testing.T) {
	runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "handoff.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer runtimeStore.Close()
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	insert := `INSERT INTO _publication_outbox (id,publication_type,workspace_id,connector_key,operation,status,payload_json,dedup_key,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)`
	createdAt := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC).UnixMilli()
	for _, row := range [][]any{
		{"connector-1", "integration.connector", "workspace-a", "crm", "upsert", "queued", `{"id":"1"}`, "dedup-1", createdAt, createdAt},
		{"notification-1", "notification.saas", "workspace-a", "notification", "publish_intent", "queued", `{}`, "dedup-2", createdAt, createdAt},
	} {
		if _, err := runtimeStore.DB().ExecContext(t.Context(), insert, row...); err != nil {
			t.Fatal(err)
		}
	}
	store := NewPublicationStore(runtimeStore)
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
	ledger := NewPublicationStore(runtimeStore)
	message, err := ledger.InsertOutbox(t.Context(), "workspace-a", publicationmodel.Message{WorkspaceID: "workspace-a", ConnectorKey: "crm", Operation: "upsert", RequestRef: "record:worker", DedupKey: "record:worker", Payload: map[string]any{"id": "1"}})
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
	completed, err := worker.UpdateOutboxStatus(t.Context(), message.WorkspaceID, message.ID, "worker-a", claimed.FencingToken, "accepted", "invocation-1", "", now)
	if err != nil || completed.Status != "accepted" || completed.LeaseOwner != "" || completed.ResponseRef != "invocation-1" {
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
	store := NewPublicationStore(runtimeStore)
	if _, err := store.InsertOutbox(t.Context(), "workspace-a", publicationmodel.Message{WorkspaceID: "workspace-a", ConnectorKey: "crm", Operation: "upsert"}); err == nil {
		t.Fatal("publication without stable deduplication identity accepted")
	}
	message := publicationmodel.Message{WorkspaceID: "workspace-a", ConnectorKey: "crm", ConnectionKey: "primary", Operation: "upsert", RequestRef: "record:1", DedupKey: "record:1", Payload: map[string]any{"id": "1"}}
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
	statusContext := requestcontext.WithOwnerExecutionID(t.Context(), "operation-status")
	updated, err := store.UpdateOutboxStatus(statusContext, message.WorkspaceID, first.ID, "accepted", "invocation-1", "")
	if err != nil || updated.OperationID != "operation-status" || updated.Status != "accepted" || updated.ResponseRef != "invocation-1" {
		t.Fatalf("updated=%#v err=%v", updated, err)
	}
	retryContext := requestcontext.WithOwnerExecutionID(t.Context(), "operation-retry")
	retried, err := store.ScheduleOutboxRetry(retryContext, message.WorkspaceID, first.ID, 1, "retry")
	if err != nil || retried.OperationID != "operation-retry" || retried.Status != "queued" || retried.AttemptCount != 1 || retried.NextAttemptAt == "" {
		t.Fatalf("retried=%#v err=%v", retried, err)
	}
}

func TestOutboxCrashAfterCommitIsRecoveredByReopenedWorkerProcess(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "crash-after-commit.db")
	open := func() *database.RuntimeStore {
		store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: databasePath})
		if err != nil {
			t.Fatal(err)
		}
		return store
	}
	producerStore := open()
	if err := producerStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	committed, err := NewPublicationStore(producerStore).InsertOutbox(t.Context(), "workspace-a", publicationmodel.Message{
		ID: "outbox-crash-after-commit", WorkspaceID: "workspace-a", ConnectorKey: "crm", Operation: "upsert", DedupKey: "record:1", Payload: map[string]any{"id": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Closing the producer connection before any wakeup/claim models the
	// process disappearing immediately after the database commit.
	if err := producerStore.Close(); err != nil {
		t.Fatal(err)
	}

	recoveredStore := open()
	defer recoveredStore.Close()
	worker := NewWorkerStore(recoveredStore)
	pollAt := time.Now().UTC().Add(time.Second).Format(time.RFC3339)
	due, err := worker.ListDueOutbox(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "recover Runtime publication after restart"), 10, pollAt)
	if err != nil || len(due) != 1 || due[0].ID != committed.ID {
		t.Fatalf("reopened worker did not recover committed Outbox: due=%#v err=%v", due, err)
	}
	claimed, ok, err := worker.ClaimOutbox(t.Context(), committed.WorkspaceID, committed.ID, "worker-b", pollAt)
	if err != nil || !ok || claimed.Status != "sending" {
		t.Fatalf("reopened worker could not claim durable Outbox: claimed=%#v ok=%v err=%v", claimed, ok, err)
	}
}
