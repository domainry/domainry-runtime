package notificationpublication

import (
	"database/sql"
	"path/filepath"
	"testing"

	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestInsertIntentParticipatesInCallerTransaction(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "runtime.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.BindNotificationSaaSPublications(database.NotificationSaaSPublicationScope{TenantID: "tenant-a", WorkspaceID: "workspace-a", ApplicationKey: "runtime-a"}); err != nil {
		t.Fatal(err)
	}
	intent := notificationmodel.NotificationIntent{ID: "request-a", WorkspaceID: "workspace-a", SourceEventID: "record-a:created", EventType: "record.created", Surface: "business_workspace", OccurredAt: "2026-08-28T00:00:00Z", Variables: map[string]any{"name": "A"}}
	publication := NewStore(store)

	tx, err := store.DB().BeginTx(t.Context(), &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		t.Fatal(err)
	}
	if err := publication.InsertIntentTx(t.Context(), tx, intent); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertPublicationCount(t, store, 0)

	tx, err = store.DB().BeginTx(t.Context(), &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		t.Fatal(err)
	}
	if err := publication.InsertIntentTx(t.Context(), tx, intent); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	assertPublicationCount(t, store, 1)
	var tenant, workspace, application, source, payload, fingerprint, status string
	if err := store.DB().QueryRowContext(t.Context(), "SELECT tenant_id,workspace_id,application_key,source_event_id,intent_json,request_fingerprint,status FROM notification_publication_outbox WHERE id=?", intent.ID).Scan(&tenant, &workspace, &application, &source, &payload, &fingerprint, &status); err != nil {
		t.Fatal(err)
	}
	if tenant != "tenant-a" || workspace != intent.WorkspaceID || application != "runtime-a" || source != intent.SourceEventID || fingerprint == "" || status != "queued" || payload == "" {
		t.Fatalf("stored scope/payload is incomplete: %q %q %q %q %q %q", tenant, workspace, application, source, fingerprint, status)
	}
}

func assertPublicationCount(t *testing.T, store *database.RuntimeStore, want int) {
	t.Helper()
	var count int
	if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM notification_publication_outbox").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("publication count=%d want=%d", count, want)
	}
}
