package notificationpublication

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	sdkcontract "github.com/domainry/domainry-notification-sdk/contract"
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
	intent := notificationmodel.NotificationIntent{ID: "request-a", WorkspaceID: "workspace-a", SourceEventID: "record-a:created", EventType: "record.created", OccurredAt: "2026-08-28T00:00:00Z", Variables: map[string]any{"name": "A"}}
	publication := NewPublicationOutboxStore(store)

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
	if err := store.DB().QueryRowContext(t.Context(), "SELECT tenant_id,workspace_id,application_key,source_event_id,intent_json,request_fingerprint,status FROM _publication_outbox WHERE id=?", intent.ID).Scan(&tenant, &workspace, &application, &source, &payload, &fingerprint, &status); err != nil {
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

type blockingPublisher struct {
	entered chan struct{}
	release chan struct{}
	calls   int
}

func (p *blockingPublisher) PublishIntent(ctx context.Context, intent sdkcontract.NotificationIntent) (sdkcontract.NotificationEvent, bool, error) {
	p.calls++
	close(p.entered)
	select {
	case <-p.release:
		return sdkcontract.NotificationEvent{ID: "remote-" + intent.ID}, true, nil
	case <-ctx.Done():
		return sdkcontract.NotificationEvent{}, false, ctx.Err()
	}
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
	if err := store.DB().QueryRowContext(t.Context(), "SELECT status,next_attempt_at,attempt_count FROM _publication_outbox WHERE id=?", intent.ID).Scan(&status, &next, &attempts); err != nil {
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
	if err := store.DB().QueryRowContext(t.Context(), "SELECT status,remote_event_id,attempt_count FROM _publication_outbox WHERE id=?", intent.ID).Scan(&status, &remote, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "delivered" || remote != "remote-"+intent.ID || attempts != 2 || len(publisher.calls) != 2 || publisher.calls[0] != intent.ID || publisher.calls[1] != intent.ID {
		t.Fatalf("delivery state=%q remote=%q attempts=%d calls=%v", status, remote, attempts, publisher.calls)
	}
}

func TestRelaySchedulesTimeoutAsUnknownOutcome(t *testing.T) {
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
	relay, err := NewRelay(publication, &publisherStub{errs: []error{context.DeadlineExceeded}}, "runtime-a", clock)
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := relay.Process(t.Context(), workerplatform.DurableTaskLocator{WorkspaceID: intent.WorkspaceID, TaskID: intent.ID}); err != nil || !worked {
		t.Fatalf("timeout worked=%v err=%v", worked, err)
	}
	var status, code, next string
	if err := store.DB().QueryRowContext(t.Context(), "SELECT status,last_error_code,next_attempt_at FROM _publication_outbox WHERE id=?", intent.ID).Scan(&status, &code, &next); err != nil {
		t.Fatal(err)
	}
	if status != "queued" || code != "notification.remote_outcome_unknown" || next == "" {
		t.Fatalf("timeout status=%q code=%q next=%q", status, code, next)
	}
}

func TestRelayFencesConcurrentDuplicateWorkers(t *testing.T) {
	_, publication, intent := openPublicationStore(t)
	tx, err := publication.runtime.DB().BeginTx(t.Context(), &sql.TxOptions{Isolation: sql.LevelSerializable})
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
	publisher := &blockingPublisher{entered: make(chan struct{}), release: make(chan struct{})}
	first, _ := NewRelay(publication, publisher, "runtime-a", clock)
	second, _ := NewRelay(publication, publisher, "runtime-b", clock)
	locator := workerplatform.DurableTaskLocator{WorkspaceID: intent.WorkspaceID, TaskID: intent.ID}
	firstResult := make(chan error, 1)
	go func() {
		worked, processErr := first.Process(t.Context(), locator)
		if processErr == nil && !worked {
			processErr = errors.New("first worker did no work")
		}
		firstResult <- processErr
	}()
	<-publisher.entered
	if worked, err := second.Process(t.Context(), locator); err != nil || worked {
		t.Fatalf("duplicate worker worked=%v err=%v", worked, err)
	}
	close(publisher.release)
	if err := <-firstResult; err != nil {
		t.Fatal(err)
	}
	if publisher.calls != 1 {
		t.Fatalf("remote publish calls=%d", publisher.calls)
	}
}

func TestRelayRejectsLateCompletionFromExpiredLease(t *testing.T) {
	_, publication, intent := openPublicationStore(t)
	tx, err := publication.runtime.DB().BeginTx(t.Context(), &sql.TxOptions{Isolation: sql.LevelSerializable})
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
	first, _ := NewRelay(publication, &publisherStub{}, "runtime-old", clock)
	locator := workerplatform.DurableTaskLocator{WorkspaceID: intent.WorkspaceID, TaskID: intent.ID}
	stale, ok, err := first.claim(t.Context(), locator)
	if err != nil || !ok {
		t.Fatalf("old claim ok=%v err=%v", ok, err)
	}
	clock.now = clock.now.Add(publicationLeaseTTL + time.Second)
	second, _ := NewRelay(publication, &publisherStub{}, "runtime-new", clock)
	if worked, err := second.Process(t.Context(), locator); err != nil || !worked {
		t.Fatalf("replacement worker worked=%v err=%v", worked, err)
	}
	if err := first.complete(t.Context(), stale, "stale-remote-event"); err == nil {
		t.Fatal("expired lease completion was accepted")
	}
	var remote string
	var fencing int64
	if err := publication.runtime.DB().QueryRowContext(t.Context(), "SELECT remote_event_id,fencing_token FROM _publication_outbox WHERE id=?", intent.ID).Scan(&remote, &fencing); err != nil {
		t.Fatal(err)
	}
	if remote != "remote-"+intent.ID || fencing != stale.FencingToken+1 {
		t.Fatalf("winning completion remote=%q fencing=%d stale=%d", remote, fencing, stale.FencingToken)
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
	if err := store.DB().QueryRowContext(t.Context(), "SELECT status,last_error_code,terminal_at FROM _publication_outbox WHERE id=?", intent.ID).Scan(&status, &code, &terminal); err != nil {
		t.Fatal(err)
	}
	if status != "dead_letter" || code != "notification.intent_invalid" || terminal == "" {
		t.Fatalf("terminal state=%q code=%q terminal=%q", status, code, terminal)
	}
}

func TestRelayRecoversExpiredLeaseAfterProcessRestart(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "restart.db")
	open := func() *database.RuntimeStore {
		store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: databasePath})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
			_ = store.Close()
			t.Fatal(err)
		}
		if err := store.BindNotificationSaaSPublications(database.NotificationSaaSPublicationScope{TenantID: "tenant-a", WorkspaceID: "workspace-a", ApplicationKey: "runtime-a"}); err != nil {
			_ = store.Close()
			t.Fatal(err)
		}
		return store
	}

	store := open()
	intent := notificationmodel.NotificationIntent{ID: "restart-request", WorkspaceID: "workspace-a", SourceEventID: "record-restart:created", EventType: "record.created", OccurredAt: "2026-08-28T00:00:00Z"}
	publication := NewPublicationOutboxStore(store)
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
	started := time.Date(2026, 8, 28, 1, 0, 0, 0, time.UTC)
	first, err := NewRelay(publication, &publisherStub{}, "runtime-before-restart", &relayClock{now: started})
	if err != nil {
		t.Fatal(err)
	}
	locator := workerplatform.DurableTaskLocator{QueueKind: "notification_publication", WorkspaceID: intent.WorkspaceID, TaskID: intent.ID}
	claimed, ok, err := first.claim(t.Context(), locator)
	if err != nil || !ok || claimed.FencingToken != 1 {
		t.Fatalf("initial claim=%+v ok=%v err=%v", claimed, ok, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	restarted := open()
	t.Cleanup(func() { _ = restarted.Close() })
	publisher := &publisherStub{}
	second, err := NewRelay(NewPublicationOutboxStore(restarted), publisher, "runtime-after-restart", &relayClock{now: started.Add(publicationLeaseTTL + time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	processed, err := second.ProcessDue(t.Context(), 25)
	if err != nil || processed != 1 {
		t.Fatalf("restart recovery processed=%d err=%v", processed, err)
	}
	var status, owner, remote string
	var fencing int64
	if err := restarted.DB().QueryRowContext(t.Context(), "SELECT status,lease_owner,remote_event_id,fencing_token FROM _publication_outbox WHERE id=?", intent.ID).Scan(&status, &owner, &remote, &fencing); err != nil {
		t.Fatal(err)
	}
	if status != "delivered" || owner != "" || remote != "remote-"+intent.ID || fencing != 2 || len(publisher.calls) != 1 {
		t.Fatalf("restart state=%q owner=%q remote=%q fencing=%d calls=%v", status, owner, remote, fencing, publisher.calls)
	}
}

func TestRelayDeadLettersAfterBoundedUnknownOutcomeRetries(t *testing.T) {
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
	if _, err := store.DB().ExecContext(t.Context(), "UPDATE _publication_outbox SET attempt_count=?,next_attempt_at='' WHERE id=?", publicationMaxAttempt-1, intent.ID); err != nil {
		t.Fatal(err)
	}
	clock := &relayClock{now: time.Date(2026, 8, 28, 1, 0, 0, 0, time.UTC)}
	relay, err := NewRelay(publication, &publisherStub{errs: []error{errors.New("response lost")}}, "runtime-a", clock)
	if err != nil {
		t.Fatal(err)
	}
	if worked, err := relay.Process(t.Context(), workerplatform.DurableTaskLocator{WorkspaceID: intent.WorkspaceID, TaskID: intent.ID}); err != nil || !worked {
		t.Fatalf("last retry worked=%v err=%v", worked, err)
	}
	var status, code, terminal string
	var attempts int
	if err := store.DB().QueryRowContext(t.Context(), "SELECT status,last_error_code,terminal_at,attempt_count FROM _publication_outbox WHERE id=?", intent.ID).Scan(&status, &code, &terminal, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "dead_letter" || code != "notification.remote_outcome_unknown" || terminal == "" || attempts != publicationMaxAttempt {
		t.Fatalf("bounded retry status=%q code=%q terminal=%q attempts=%d", status, code, terminal, attempts)
	}
}

func openPublicationStore(t *testing.T) (*database.RuntimeStore, PublicationOutboxStore, notificationmodel.NotificationIntent) {
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
	return store, NewPublicationOutboxStore(store), notificationmodel.NotificationIntent{ID: "request-a", WorkspaceID: "workspace-a", SourceEventID: "record-a:created", EventType: "record.created", OccurredAt: "2026-08-28T00:00:00Z"}
}

func assertPublicationCount(t *testing.T, store *database.RuntimeStore, want int) {
	t.Helper()
	var count int
	if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM _publication_outbox").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("publication count=%d want=%d", count, want)
	}
}
