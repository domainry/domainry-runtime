package integration

import (
	"context"
	"errors"
	"github.com/domainry/domainry-foundation/requestcontext"
	"github.com/domainry/domainry-foundation/telemetry"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"go.opentelemetry.io/otel/trace"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func integrationWorkerTestScope() principalmodel.SystemScope {
	return principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test integration worker poll")
}

func TestIntegrationWorkerStoreImplementsContractAndCancelsSQL(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	repository := NewIntegrationWorkerStore(store)
	var _ integrationrepository.IntegrationWorkerRepository = repository
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := repository.ListDueEvents(ctx, integrationWorkerTestScope(), 10, "2026-01-01T00:00:00Z"); !errors.Is(err, context.Canceled) {
		t.Fatalf("event query error=%v, want context.Canceled", err)
	}
	if _, err := repository.ListDueOutbox(ctx, integrationWorkerTestScope(), 10, "2026-01-01T00:00:00Z"); !errors.Is(err, context.Canceled) {
		t.Fatalf("outbox query error=%v, want context.Canceled", err)
	}
}

func TestIntegrationWorkerStoreRejectsMissingTenantAndSystemScopes(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	repository := NewIntegrationWorkerStore(store)
	if _, err := repository.ListDueEvents(t.Context(), principalmodel.SystemScope{}, 1, "now"); err == nil {
		t.Fatal("missing event worker system scope unexpectedly succeeded")
	}
	if _, err := repository.ListDueOutbox(t.Context(), principalmodel.SystemScope{}, 1, "now"); err == nil {
		t.Fatal("missing outbox worker system scope unexpectedly succeeded")
	}
	if _, err := repository.ListDueOutbox(t.Context(), principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "wrong scan authority"), 1, "now"); err == nil {
		t.Fatal("installation scope unexpectedly authorized Runtime-global outbox scan")
	}
	checks := []func() error{
		func() error {
			_, _, err := repository.ClaimEvent(t.Context(), "", "event", "worker-a", "now")
			return err
		},
		func() error {
			_, err := repository.HeartbeatEvent(t.Context(), "", "event", "owner", 1, "now")
			return err
		},
		func() error {
			_, err := repository.UpdateEventStatus(t.Context(), "", "event", "owner", 1, "done", "", "2026-07-19T00:00:00Z")
			return err
		},
		func() error {
			_, err := repository.ScheduleEventRetry(t.Context(), "", "event", "owner", 1, 1, "failed", "2026-07-19T00:00:00Z")
			return err
		},
		func() error {
			_, _, err := repository.ClaimOutbox(t.Context(), "", "message", "worker-a", "now")
			return err
		},
		func() error {
			_, err := repository.HeartbeatOutbox(t.Context(), "", "message", "owner", 1, "now")
			return err
		},
		func() error {
			_, err := repository.UpdateOutboxStatus(t.Context(), "", "message", "owner", 1, "sent", "", "", "", "2026-07-19T00:00:00Z")
			return err
		},
		func() error {
			_, err := repository.ScheduleOutboxRetry(t.Context(), "", "message", "owner", 1, 1, "failed", "2026-07-19T00:00:00Z")
			return err
		},
	}
	for index, check := range checks {
		if err := check(); err == nil {
			t.Fatalf("missing worker workspace check %d unexpectedly succeeded", index)
		}
	}
}

func TestOutboxPollingDiscoversCommittedMessageWithoutWakeup(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	committed, err := NewIntegrationDeliveryStore(store).InsertOutbox(t.Context(), "workspace-primary", integrationmodel.IntegrationOutboxMessage{
		ID: "outbox_poll_recovery", WorkspaceID: "workspace-primary", ConnectorKey: "webhook", Operation: "notify",
		Status: "queued", Payload: map[string]any{"event": "committed"}, CreatedAt: "2026-07-19T00:00:00Z", UpdatedAt: "2026-07-19T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	worker := NewIntegrationWorkerStore(store)
	pollAt := time.Now().UTC().Add(time.Second).Format(time.RFC3339)
	due, err := worker.ListDueOutbox(t.Context(), integrationWorkerTestScope(), 10, pollAt)
	if err != nil || len(due) != 1 || due[0].ID != committed.ID {
		t.Fatalf("poll did not recover committed Outbox: due=%#v err=%v", due, err)
	}
	claimed, ok, err := worker.ClaimOutbox(t.Context(), committed.WorkspaceID, committed.ID, "worker-a", pollAt)
	if err != nil || !ok || claimed.Status != "sending" {
		t.Fatalf("poll-discovered Outbox was not claimable: claimed=%#v ok=%v err=%v", claimed, ok, err)
	}
}

func TestOutboxCrashAfterCommitIsRecoveredByReopenedWorkerProcess(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "crash-after-commit.db")
	open := func() *database.RuntimeStore {
		store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: databasePath, IntegrationSecretKey: "test-integration-secret-key"})
		if err != nil {
			t.Fatal(err)
		}
		return store
	}
	producerStore := open()
	if err := ensureIntegrationTestSchema(t.Context(), producerStore); err != nil {
		t.Fatal(err)
	}
	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	producerContext := trace.ContextWithSpanContext(requestcontext.WithCorrelationID(t.Context(), "correlation-after-restart"), trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled}))
	committed, err := NewIntegrationDeliveryStore(producerStore).InsertOutbox(producerContext, "workspace-primary", integrationmodel.IntegrationOutboxMessage{
		ID: "outbox_crash_after_commit", WorkspaceID: "workspace-primary", ConnectorKey: "webhook", Operation: "notify",
		Status: "queued", Payload: map[string]any{"event": "durable"}, CreatedAt: "2026-07-19T00:00:00Z", UpdatedAt: "2026-07-19T00:00:00Z",
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
	worker := NewIntegrationWorkerStore(recoveredStore)
	pollAt := time.Now().UTC().Add(time.Second).Format(time.RFC3339)
	due, err := worker.ListDueOutbox(t.Context(), integrationWorkerTestScope(), 10, pollAt)
	if err != nil || len(due) != 1 || due[0].ID != committed.ID {
		t.Fatalf("reopened worker did not recover committed Outbox: due=%#v err=%v", due, err)
	}
	claimed, ok, err := worker.ClaimOutbox(t.Context(), committed.WorkspaceID, committed.ID, "worker-b", pollAt)
	if err != nil || !ok || claimed.Status != "sending" {
		t.Fatalf("reopened worker could not claim durable Outbox: claimed=%#v ok=%v err=%v", claimed, ok, err)
	}
	persistedLink, ok := claimed.Payload[telemetry.AsyncPayloadKey].(map[string]any)
	if !ok {
		t.Fatalf("reopened worker lost persisted telemetry link: %#v", due[0].Payload)
	}
	link := telemetry.ParseAsyncLink(persistedLink)
	if link.CorrelationID != "correlation-after-restart" || link.TraceID != traceID.String() || link.SpanID != spanID.String() {
		t.Fatalf("reopened telemetry link=%#v", link)
	}
}

func TestIntegrationEventAndOutboxConcurrentClaimsHaveSingleWinner(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	repository := NewIntegrationWorkerStore(store)
	event, _, err := NewIntegrationEventStore(store).UpsertEvent(t.Context(), "workspace-primary", integrationmodel.IntegrationEvent{ID: "event_concurrent", WorkspaceID: "workspace-primary", Provider: "webhook", EventType: "updated", ExternalID: "external_concurrent", Status: "received", Payload: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewIntegrationDeliveryStore(store).InsertOutbox(t.Context(), "workspace-primary", integrationmodel.IntegrationOutboxMessage{ID: "outbox_concurrent", WorkspaceID: "workspace-primary", ConnectorKey: "webhook", Operation: "notify", Status: "queued", Payload: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	pollAt := time.Now().UTC().Add(time.Second).Format(time.RFC3339)
	for instance := range 2 {
		due, err := NewIntegrationWorkerStore(store).ListDueOutbox(t.Context(), integrationWorkerTestScope(), 10, pollAt)
		if err != nil || len(due) != 1 || due[0].ID != "outbox_concurrent" {
			t.Fatalf("cluster scanner %d candidates=%#v err=%v", instance, due, err)
		}
	}
	assertSingleIntegrationClaimWinner(t, func() (bool, error) {
		_, claimed, err := repository.ClaimEvent(t.Context(), "workspace-primary", event.ID, "worker-concurrent", "2026-01-01T00:00:00Z")
		return claimed, err
	})
	assertSingleIntegrationClaimWinner(t, func() (bool, error) {
		_, claimed, err := repository.ClaimOutbox(t.Context(), "workspace-primary", "outbox_concurrent", "worker-concurrent", "2026-01-01T00:00:00Z")
		return claimed, err
	})
}

func assertSingleIntegrationClaimWinner(t *testing.T, claim func() (bool, error)) {
	t.Helper()
	start := make(chan struct{})
	results := make(chan bool, 100)
	errorsFound := make(chan error, 100)
	var group sync.WaitGroup
	for range 100 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			claimed, err := claim()
			if err != nil {
				errorsFound <- err
				return
			}
			results <- claimed
		}()
	}
	close(start)
	group.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatalf("concurrent claim: %v", err)
	}
	winners := 0
	for claimed := range results {
		if claimed {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("expected one claim winner, got %d", winners)
	}
}

func TestIntegrationWorkersReclaimExpiredLeasesAndRejectStaleCompletion(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	repository := NewIntegrationWorkerStore(store)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	event, _, err := NewIntegrationEventStore(store).UpsertEvent(t.Context(), "workspace-primary", integrationmodel.IntegrationEvent{ID: "event_lease", WorkspaceID: "workspace-primary", Provider: "webhook", EventType: "updated", ExternalID: "external_lease", Status: "received", Payload: map[string]any{}, ReceivedAt: base.Format(time.RFC3339), UpdatedAt: base.Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	firstEvent, claimed, err := repository.ClaimEvent(t.Context(), event.WorkspaceID, event.ID, "worker-a", base.Format(time.RFC3339))
	if err != nil || !claimed {
		t.Fatalf("claim event: claimed=%v err=%v", claimed, err)
	}
	heartbeatAt := base.Add(4 * time.Minute).Format(time.RFC3339)
	firstEvent, err = repository.HeartbeatEvent(t.Context(), event.WorkspaceID, event.ID, firstEvent.LeaseOwner, firstEvent.FencingToken, heartbeatAt)
	if err != nil || firstEvent.UpdatedAt != heartbeatAt || firstEvent.LeaseExpiresAt != base.Add(9*time.Minute).Format(time.RFC3339) {
		t.Fatalf("heartbeat event: %#v err=%v", firstEvent, err)
	}
	if _, claimed, err := repository.ClaimEvent(t.Context(), event.WorkspaceID, event.ID, "worker-b", base.Add(6*time.Minute).Format(time.RFC3339)); err != nil || claimed {
		t.Fatalf("heartbeat must keep event lease alive: claimed=%v err=%v", claimed, err)
	}
	reclaimAt := base.Add(10 * time.Minute).Format(time.RFC3339)
	dueEvents, err := repository.ListDueEvents(t.Context(), integrationWorkerTestScope(), 10, reclaimAt)
	if err != nil || len(dueEvents) != 1 {
		t.Fatalf("expected stale processing event to become due, events=%#v err=%v", dueEvents, err)
	}
	secondEvent, claimed, err := repository.ClaimEvent(t.Context(), event.WorkspaceID, event.ID, "worker-b", reclaimAt)
	if err != nil || !claimed {
		t.Fatalf("reclaim event: claimed=%v err=%v", claimed, err)
	}
	if _, err := repository.UpdateEventStatus(t.Context(), event.WorkspaceID, event.ID, firstEvent.LeaseOwner, firstEvent.FencingToken, "processed", "", reclaimAt); err == nil {
		t.Fatal("expected stale event completion to lose lease")
	}
	if secondEvent.FencingToken != firstEvent.FencingToken+1 {
		t.Fatalf("reclaimed event fencing token=%d want %d", secondEvent.FencingToken, firstEvent.FencingToken+1)
	}
	if _, err := repository.UpdateEventStatus(t.Context(), event.WorkspaceID, event.ID, secondEvent.LeaseOwner, firstEvent.FencingToken, "processed", "", reclaimAt); err == nil {
		t.Fatal("expected stale event fencing token to lose lease")
	}
	if completed, err := repository.UpdateEventStatus(t.Context(), event.WorkspaceID, event.ID, secondEvent.LeaseOwner, secondEvent.FencingToken, "processed", "", reclaimAt); err != nil || completed.Status != "processed" {
		t.Fatalf("complete current event lease: %#v err=%v", completed, err)
	}

	outbox, err := NewIntegrationDeliveryStore(store).InsertOutbox(t.Context(), "workspace-primary", integrationmodel.IntegrationOutboxMessage{ID: "outbox_lease", WorkspaceID: "workspace-primary", ConnectorKey: "webhook", Operation: "notify", Status: "queued", Payload: map[string]any{}, CreatedAt: base.Format(time.RFC3339), UpdatedAt: base.Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	firstOutbox, claimed, err := repository.ClaimOutbox(t.Context(), outbox.WorkspaceID, outbox.ID, "worker-a", base.Format(time.RFC3339))
	if err != nil || !claimed {
		t.Fatalf("claim outbox: claimed=%v err=%v", claimed, err)
	}
	firstOutbox, err = repository.HeartbeatOutbox(t.Context(), outbox.WorkspaceID, outbox.ID, firstOutbox.LeaseOwner, firstOutbox.FencingToken, heartbeatAt)
	if err != nil || firstOutbox.UpdatedAt != heartbeatAt || firstOutbox.LeaseExpiresAt != base.Add(9*time.Minute).Format(time.RFC3339) {
		t.Fatalf("heartbeat outbox: %#v err=%v", firstOutbox, err)
	}
	dueOutbox, err := repository.ListDueOutbox(t.Context(), integrationWorkerTestScope(), 10, reclaimAt)
	if err != nil || len(dueOutbox) != 1 {
		t.Fatalf("expected stale sending outbox to become due, messages=%#v err=%v", dueOutbox, err)
	}
	secondOutbox, claimed, err := repository.ClaimOutbox(t.Context(), outbox.WorkspaceID, outbox.ID, "worker-b", reclaimAt)
	if err != nil || !claimed {
		t.Fatalf("reclaim outbox: claimed=%v err=%v", claimed, err)
	}
	if secondOutbox.FencingToken != firstOutbox.FencingToken+1 {
		t.Fatalf("outbox reclaim fencing=%d want=%d", secondOutbox.FencingToken, firstOutbox.FencingToken+1)
	}
	if _, err := repository.UpdateOutboxStatus(t.Context(), outbox.WorkspaceID, outbox.ID, firstOutbox.LeaseOwner, firstOutbox.FencingToken, "sent", "stale", "", "", reclaimAt); err == nil {
		t.Fatal("expected stale outbox completion to lose lease")
	}
	if completed, err := repository.UpdateOutboxStatus(t.Context(), outbox.WorkspaceID, outbox.ID, secondOutbox.LeaseOwner, secondOutbox.FencingToken, "sent", "owner", "", "", reclaimAt); err != nil || completed.Status != "sent" {
		t.Fatalf("complete current outbox lease: %#v err=%v", completed, err)
	}
}

func TestOutboxRetryPreservesProviderIdempotencyKeyAfterUnknownOutcome(t *testing.T) {
	store := openRuntimeStore(t)
	defer store.Close()
	if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	delivery := NewIntegrationDeliveryStore(store)
	worker := NewIntegrationWorkerStore(store)
	message, err := delivery.InsertOutbox(t.Context(), "workspace-primary", integrationmodel.IntegrationOutboxMessage{
		WorkspaceID: "workspace-primary", ConnectorKey: "payment", ConnectionKey: "primary", Operation: "capture",
		RequestRef: "provider-idempotency:payment:one", DedupKey: "payment:one", Payload: map[string]any{"payment_id": "one"},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, claimed, err := worker.ClaimOutbox(t.Context(), message.WorkspaceID, message.ID, "worker-a", "2026-01-01T00:00:00Z")
	if err != nil || !claimed {
		t.Fatalf("first claim: claimed=%v err=%v", claimed, err)
	}
	queued, err := worker.ScheduleOutboxRetry(t.Context(), first.WorkspaceID, first.ID, first.LeaseOwner, first.FencingToken, 0, "provider_timeout_after_success", "2026-01-01T00:00:00Z")
	if err != nil || queued.RequestRef != message.RequestRef {
		t.Fatalf("retry lost provider key: message=%#v err=%v", queued, err)
	}
	second, claimed, err := worker.ClaimOutbox(t.Context(), queued.WorkspaceID, queued.ID, "worker-b", time.Now().UTC().Add(time.Minute).Format(time.RFC3339))
	if err != nil || !claimed || second.RequestRef != message.RequestRef || second.FencingToken != first.FencingToken+1 || second.Error != "provider_timeout_after_success" {
		t.Fatalf("retry claim=%#v claimed=%v err=%v", second, claimed, err)
	}
}
