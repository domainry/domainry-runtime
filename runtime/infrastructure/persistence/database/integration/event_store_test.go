package integration

import (
	"context"
	"errors"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	"testing"
)

type contextIntegrationEventContract interface {
	ListEvents(context.Context, string, string, string, int) ([]integrationmodel.IntegrationEvent, error)
	GetEvent(context.Context, string, string) (integrationmodel.IntegrationEvent, bool, error)
	UpsertEvent(context.Context, string, integrationmodel.IntegrationEvent) (integrationmodel.IntegrationEvent, bool, error)
	AcceptEvent(context.Context, string, integrationmodel.IntegrationEvent, integrationmodel.IntegrationEventMappingIntent) (integrationmodel.IntegrationEvent, bool, error)
	UpdateEventStatus(context.Context, string, string, string, string) (integrationmodel.IntegrationEvent, error)
	ScheduleEventRetry(context.Context, string, string, int, string) (integrationmodel.IntegrationEvent, error)
	RecordWebhookNonce(context.Context, string, string, string, string, string) (bool, error)
}

var _ contextIntegrationEventContract = IntegrationEventStore{}

func TestIntegrationEventStoreContractAndCancellation(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewIntegrationEventStore(store)
	value, duplicate, err := repository.UpsertEvent(t.Context(), "default", integrationmodel.IntegrationEvent{WorkspaceID: "default", Provider: "crm", EventType: "customer.updated", ExternalID: "evt-1"})
	if err != nil || duplicate {
		t.Fatalf("upsert event=%#v duplicate=%v err=%v", value, duplicate, err)
	}
	second, duplicate, err := repository.UpsertEvent(t.Context(), "default", integrationmodel.IntegrationEvent{WorkspaceID: "default", Provider: "crm", EventType: "customer.updated", ExternalID: "evt-1"})
	if err != nil || !duplicate || second.ID != value.ID {
		t.Fatalf("duplicate event=%#v duplicate=%v err=%v", second, duplicate, err)
	}
	duplicate, err = repository.RecordWebhookNonce(t.Context(), "default", "webhook", "nonce-1", "1700000000", "2099-01-01T00:00:00Z")
	if err != nil || duplicate {
		t.Fatalf("first webhook nonce duplicate=%v err=%v", duplicate, err)
	}
	duplicate, err = repository.RecordWebhookNonce(t.Context(), "default", "webhook", "nonce-1", "1700000000", "2099-01-01T00:00:00Z")
	if err != nil || !duplicate {
		t.Fatalf("replayed webhook nonce duplicate=%v err=%v", duplicate, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.ListEvents(cancelled, "default", "", "", 10); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled list error=%v", err)
	}
	if _, _, err := repository.UpsertEvent(cancelled, "default", integrationmodel.IntegrationEvent{WorkspaceID: "default", Provider: "crm", ExternalID: "never"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled upsert error=%v", err)
	}
}

func TestAcceptIntegrationEventRollsBackEventWhenMappingIntentFails(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`CREATE TRIGGER fail_mapping_intent BEFORE INSERT ON integration_event_mapping_intents BEGIN SELECT RAISE(FAIL, 'injected mapping intent failure'); END`); err != nil {
		t.Fatal(err)
	}
	repository := NewIntegrationEventStore(store)
	event := integrationmodel.IntegrationEvent{WorkspaceID: "default", Provider: "crm", EventType: "customer.updated", ExternalID: "evt-atomic-failure"}
	intent := integrationmodel.IntegrationEventMappingIntent{MappingKey: "customer-updated", TargetType: "workflow", Status: "pending"}
	if _, _, err := repository.AcceptEvent(t.Context(), event.WorkspaceID, event, intent); err == nil {
		t.Fatal("expected injected mapping intent failure")
	}
	var eventCount, intentCount int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM integration_events WHERE external_id = ?`, event.ExternalID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM integration_event_mapping_intents`).Scan(&intentCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 0 || intentCount != 0 {
		t.Fatalf("partial acceptance persisted: events=%d intents=%d", eventCount, intentCount)
	}
}

func TestAcceptIntegrationEventCommitsEventAndMappingIntentTogether(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewIntegrationEventStore(store)
	event := integrationmodel.IntegrationEvent{WorkspaceID: "default", Provider: "crm", EventType: "customer.updated", ExternalID: "evt-atomic-success"}
	intent := integrationmodel.IntegrationEventMappingIntent{MappingKey: "customer-updated", TargetType: "workflow", Status: "pending", Payload: map[string]any{"workflow_key": "sync-customer"}}
	saved, duplicate, err := repository.AcceptEvent(t.Context(), event.WorkspaceID, event, intent)
	if err != nil || duplicate {
		t.Fatalf("accept event=%#v duplicate=%v err=%v", saved, duplicate, err)
	}
	var eventCount, intentCount int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM integration_events WHERE id = ?`, saved.ID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM integration_event_mapping_intents WHERE event_id = ? AND mapping_key = ? AND status = 'pending'`, saved.ID, intent.MappingKey).Scan(&intentCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 || intentCount != 1 {
		t.Fatalf("atomic acceptance missing facts: events=%d intents=%d", eventCount, intentCount)
	}
}

func TestAcceptIntegrationEventQuarantinesExternalIDContentConflict(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewIntegrationEventStore(store)
	event := integrationmodel.IntegrationEvent{WorkspaceID: "default", Provider: "adapter", EventType: "fact.created", ExternalID: "external-immutable-1", Payload: map[string]any{"amount": "10.00", "_integration_security": map[string]any{"nonce": "first"}}}
	intent := integrationmodel.IntegrationEventMappingIntent{MappingKey: "fact-created", TargetType: "workflow", Status: "pending"}
	saved, duplicate, err := repository.AcceptEvent(t.Context(), event.WorkspaceID, event, intent)
	if err != nil || duplicate {
		t.Fatalf("first=%#v duplicate=%v err=%v", saved, duplicate, err)
	}
	replay := event
	replay.Payload = map[string]any{"amount": "10.00", "_integration_security": map[string]any{"nonce": "fresh"}}
	if duplicateEvent, isDuplicate, err := repository.AcceptEvent(t.Context(), event.WorkspaceID, replay, intent); err != nil || !isDuplicate || duplicateEvent.Status != "received" {
		t.Fatalf("exact replay=%#v duplicate=%v err=%v", duplicateEvent, isDuplicate, err)
	}
	conflict := event
	conflict.Payload = map[string]any{"amount": "11.00"}
	quarantined, isDuplicate, err := repository.AcceptEvent(t.Context(), event.WorkspaceID, conflict, intent)
	if err != nil || !isDuplicate || quarantined.Status != "quarantined" || quarantined.Error != "backend.integration.event.external_id_conflict" {
		t.Fatalf("conflict=%#v duplicate=%v err=%v", quarantined, isDuplicate, err)
	}
	stored, found, err := repository.GetEvent(t.Context(), event.WorkspaceID, saved.ID)
	if err != nil || !found || stored.Status != "quarantined" || stored.Payload["amount"] != "10.00" {
		t.Fatalf("stored=%#v found=%v err=%v", stored, found, err)
	}
}

func TestIntegrationEventStoreWorkspaceIsolationContract(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewIntegrationEventStore(store)
	created := map[string]integrationmodel.IntegrationEvent{}
	for _, workspaceID := range []string{"workspace-a", "workspace-b"} {
		event, duplicate, err := repository.UpsertEvent(t.Context(), workspaceID, integrationmodel.IntegrationEvent{
			WorkspaceID: workspaceID, Provider: "webhook", EventType: workspaceID, ExternalID: "shared-external-id",
		})
		if err != nil || duplicate {
			t.Fatalf("upsert %s event=%#v duplicate=%v err=%v", workspaceID, event, duplicate, err)
		}
		created[workspaceID] = event
	}
	listed, err := repository.ListEvents(t.Context(), "workspace-a", "", "", 10)
	if err != nil || len(listed) != 1 || listed[0].EventType != "workspace-a" {
		t.Fatalf("workspace-a events=%#v err=%v", listed, err)
	}
	if _, found, err := repository.GetEvent(t.Context(), "workspace-b", created["workspace-a"].ID); err != nil || found {
		t.Fatalf("cross-workspace event read found=%v err=%v", found, err)
	}

	missingChecks := []func() error{
		func() error { _, err := repository.ListEvents(t.Context(), "", "", "", 1); return err },
		func() error { _, _, err := repository.GetEvent(t.Context(), "", "event"); return err },
		func() error {
			_, _, err := repository.UpsertEvent(t.Context(), "", integrationmodel.IntegrationEvent{})
			return err
		},
		func() error {
			_, _, err := repository.AcceptEvent(t.Context(), "", integrationmodel.IntegrationEvent{}, integrationmodel.IntegrationEventMappingIntent{})
			return err
		},
		func() error {
			_, err := repository.UpdateEventStatus(t.Context(), "", "event", "processed", "")
			return err
		},
		func() error {
			_, err := repository.ScheduleEventRetry(t.Context(), "", "event", 1, "failed")
			return err
		},
		func() error {
			_, err := repository.RecordWebhookNonce(t.Context(), "", "connector", "nonce", "now", "later")
			return err
		},
	}
	for index, check := range missingChecks {
		if err := check(); err == nil {
			t.Fatalf("missing workspace check %d unexpectedly succeeded", index)
		}
	}
	if _, _, err := repository.UpsertEvent(t.Context(), "workspace-a", integrationmodel.IntegrationEvent{WorkspaceID: "workspace-b"}); err == nil {
		t.Fatal("event workspace mismatch unexpectedly succeeded")
	}
	if _, _, err := repository.AcceptEvent(t.Context(), "workspace-a", integrationmodel.IntegrationEvent{WorkspaceID: "workspace-b"}, integrationmodel.IntegrationEventMappingIntent{}); err == nil {
		t.Fatal("accepted event workspace mismatch unexpectedly succeeded")
	}
}
