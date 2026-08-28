package integration

import (
	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestEmitSuccessfulGmailDeliveryEvent(t *testing.T) {
	events := &integrationManagementEventRepo{}
	service := NewIntegrationApplicationService(ApplicationDependencies{EventRepository: events})
	principal := integrationManagementPrincipal(PermissionInvoke)
	message := integrationmodel.IntegrationOutboxMessage{
		ID: "outbox-one", WorkspaceID: "workspace", ConnectorKey: "google_workspace",
		ConnectionKey: "gmail_primary", Operation: "gmail_send_message",
		Payload: map[string]any{"rfc_message_id": "<outbox-one@example.invalid>"},
	}
	result := OutboxSendResult{Provider: "google", ResponseRef: "gmail:provider-one", Response: map[string]any{"id": "provider-one", "threadId": "thread-one"}}
	if err := service.emitSuccessfulGmailDeliveryEvent(t.Context(), message, result, principal); err != nil {
		t.Fatalf("emit sent event: %v", err)
	}
	if events.event.Provider != "google" || events.event.EventType != "gmail.message.sent" || events.event.ExternalID != "gmail:gmail_primary:sent:outbox-one" {
		t.Fatalf("sent event identity=%#v", events.event)
	}
	if events.event.Payload["outbox_id"] != "outbox-one" || events.event.Payload["message_id"] != "provider-one" || events.event.Payload["thread_id"] != "thread-one" || events.event.Payload["rfc_message_id"] != "<outbox-one@example.invalid>" {
		t.Fatalf("sent event payload=%#v", events.event.Payload)
	}
}

func TestEmitSuccessfulGmailDeliveryEventEdges(t *testing.T) {
	service := NewIntegrationApplicationService(ApplicationDependencies{EventRepository: &integrationManagementEventRepo{}})
	principal := integrationManagementPrincipal(PermissionInvoke)
	if err := service.emitSuccessfulGmailDeliveryEvent(t.Context(), integrationmodel.IntegrationOutboxMessage{ConnectorKey: "webhook", Operation: "send"}, OutboxSendResult{}, principal); err != nil {
		t.Fatalf("non-gmail event=%v", err)
	}
	if err := service.emitSuccessfulGmailDeliveryEvent(t.Context(), integrationmodel.IntegrationOutboxMessage{ConnectorKey: "google_workspace", Operation: "other"}, OutboxSendResult{}, principal); err != nil {
		t.Fatalf("unrelated Gmail operation=%v", err)
	}
	message := integrationmodel.IntegrationOutboxMessage{ID: "outbox", WorkspaceID: "workspace", ConnectorKey: "google_workspace", ConnectionKey: "gmail_primary", Operation: "gmail_send_message", Payload: map[string]any{"rfc_message_id": "<outbox@example.invalid>"}}
	if err := service.emitSuccessfulGmailDeliveryEvent(t.Context(), message, OutboxSendResult{Response: map[string]any{"id": "provider"}}, principal); err == nil {
		t.Fatal("expected incomplete Gmail receipt to be rejected")
	}
	if err := service.emitSuccessfulGmailDeliveryEvent(t.Context(), message, OutboxSendResult{Response: map[string]any{"id": "provider", "threadId": "thread"}}, principal); err != nil {
		t.Fatalf("default provider receipt err=%v", err)
	}
	withoutRFCMessageID := message
	withoutRFCMessageID.Payload = map[string]any{}
	if err := service.emitSuccessfulGmailDeliveryEvent(t.Context(), withoutRFCMessageID, OutboxSendResult{Response: map[string]any{"id": "provider", "threadId": "thread"}}, principal); err == nil {
		t.Fatal("missing RFC message id accepted")
	}
}

func TestEmitFailedGmailDeliveryEvent(t *testing.T) {
	events := &integrationManagementEventRepo{}
	service := NewIntegrationApplicationService(ApplicationDependencies{EventRepository: events})
	principal := integrationManagementPrincipal(PermissionInvoke)
	message := integrationmodel.IntegrationOutboxMessage{
		ID: "outbox-failed", WorkspaceID: "workspace", ConnectorKey: "google_workspace",
		ConnectionKey: "gmail_primary", Operation: "gmail_send_message",
		Payload: map[string]any{"rfc_message_id": "<outbox-failed@example.invalid>"},
	}
	if err := service.emitFailedGmailDeliveryEvent(t.Context(), message, "backend.integration.google.http_status_429", principal); err != nil {
		t.Fatalf("emit failed event: %v", err)
	}
	if events.event.Provider != "google" || events.event.EventType != "gmail.message.failed" || events.event.ExternalID != "gmail:gmail_primary:failed:outbox-failed" {
		t.Fatalf("failed event identity=%#v", events.event)
	}
	if events.event.Payload["outbox_id"] != "outbox-failed" || events.event.Payload["rfc_message_id"] != "<outbox-failed@example.invalid>" || events.event.Payload["error_code"] != "backend.integration.google.http_status_429" {
		t.Fatalf("failed event payload=%#v", events.event.Payload)
	}
	if err := service.emitFailedGmailDeliveryEvent(t.Context(), integrationmodel.IntegrationOutboxMessage{ConnectorKey: "webhook", Operation: "send"}, "failure", principal); err != nil {
		t.Fatalf("non-gmail event=%v", err)
	}
	if err := service.emitFailedGmailDeliveryEvent(t.Context(), integrationmodel.IntegrationOutboxMessage{ConnectorKey: "google_workspace", Operation: "other"}, "failure", principal); err != nil {
		t.Fatalf("unrelated Gmail failure operation=%v", err)
	}
	if err := service.emitFailedGmailDeliveryEvent(t.Context(), message, "", principal); err == nil {
		t.Fatal("expected empty failure code to be rejected")
	}
}
