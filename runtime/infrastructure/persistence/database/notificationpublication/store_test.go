package notificationpublication

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	notificationsdk "github.com/domainry/domainry-notification-sdk"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	sdkcontract "github.com/domainry/domainry-notification-sdk/contract"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
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

type relayClock struct{ now time.Time }

func (c *relayClock) Now() time.Time { return c.now }

type publisherStub struct {
	calls []string
	errs  []error
}

func (p *publisherStub) PublishIntent(_ context.Context, intent sdkcontract.NotificationIntent) (sdkcontract.NotificationEvent, bool, error) {
	p.calls = append(p.calls, intent.ID)
	if len(p.errs) > 0 {
		err := p.errs[0]
		p.errs = p.errs[1:]
		if err != nil {
			return sdkcontract.NotificationEvent{}, false, err
		}
	}
	return sdkcontract.NotificationEvent{ID: "remote-" + intent.ID}, true, nil
}

func TestRelayRetriesUnknownOutcomeWithStableIntentIdentity(t *testing.T) {
	store, publication, intent := openPublicationStore(t)
	tx, err := store.DB().BeginTx(t.Context(), &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		t.Fatal(err)
	}
	if err := publication.InsertIntentTx(t.Context(), tx, intent); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	clock := &relayClock{now: time.Date(2026, 8, 28, 1, 0, 0, 0, time.UTC)}
	publisher := &publisherStub{errs: []error{errors.New("connection ended after send"), nil}}
	relay, err := NewRelay(publication, publisher, "runtime-a", clock)
	if err != nil {
		t.Fatal(err)
	}
	locator := workerplatform.DurableTaskLocator{QueueKind: "notification_publication", WorkspaceID: intent.WorkspaceID, TaskID: intent.ID}
	if worked, err := relay.Process(t.Context(), locator); err != nil || !worked {
		t.Fatalf("first relay worked=%v err=%v", worked, err)
	}
	var status, next string
	var attempts int
	if err := store.DB().QueryRowContext(t.Context(), "SELECT status,next_attempt_at,attempt_count FROM notification_publication_outbox WHERE id=?", intent.ID).Scan(&status, &next, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || next == "" || attempts != 1 {
		t.Fatalf("retry state=%q next=%q attempts=%d", status, next, attempts)
	}
	clock.now = clock.now.Add(2 * time.Second)
	if worked, err := relay.Process(t.Context(), locator); err != nil || !worked {
		t.Fatalf("second relay worked=%v err=%v", worked, err)
	}
	var remote string
	if err := store.DB().QueryRowContext(t.Context(), "SELECT status,remote_event_id,attempt_count FROM notification_publication_outbox WHERE id=?", intent.ID).Scan(&status, &remote, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "delivered" || remote != "remote-"+intent.ID || attempts != 2 || len(publisher.calls) != 2 || publisher.calls[0] != intent.ID || publisher.calls[1] != intent.ID {
		t.Fatalf("delivery state=%q remote=%q attempts=%d calls=%v", status, remote, attempts, publisher.calls)
	}
}

func TestRelayDeadLettersNonRetryableRemoteRejection(t *testing.T) {
	store, publication, intent := openPublicationStore(t)
	tx, _ := store.DB().BeginTx(t.Context(), &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err := publication.InsertIntentTx(t.Context(), tx, intent); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	clock := &relayClock{now: time.Date(2026, 8, 28, 1, 0, 0, 0, time.UTC)}
	publisher := &publisherStub{errs: []error{&notificationsdk.Error{Code: "notification.intent_invalid", Retryable: false}}}
	relay, _ := NewRelay(publication, publisher, "runtime-a", clock)
	if _, err := relay.Process(t.Context(), workerplatform.DurableTaskLocator{WorkspaceID: intent.WorkspaceID, TaskID: intent.ID}); err != nil {
		t.Fatal(err)
	}
	var status, code, terminal string
	if err := store.DB().QueryRowContext(t.Context(), "SELECT status,last_error_code,terminal_at FROM notification_publication_outbox WHERE id=?", intent.ID).Scan(&status, &code, &terminal); err != nil {
		t.Fatal(err)
	}
	if status != "dead_letter" || code != "notification.intent_invalid" || terminal == "" {
		t.Fatalf("terminal state=%q code=%q terminal=%q", status, code, terminal)
	}
}

func openPublicationStore(t *testing.T) (*database.RuntimeStore, Store, notificationmodel.NotificationIntent) {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "relay.db")})
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
	return store, NewStore(store), notificationmodel.NotificationIntent{ID: "request-a", WorkspaceID: "workspace-a", SourceEventID: "record-a:created", EventType: "record.created", Surface: "business_workspace", OccurredAt: "2026-08-28T00:00:00Z"}
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
