package integration

import (
	"context"
	"fmt"
	"strings"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// emitSuccessfulGmailDeliveryEvent turns the provider receipt into a durable,
// replay-safe integration fact after the outbox row has been fenced to sent.
// Project code can therefore update its communication history without reading
// credentials or inspecting Runtime-owned invocation evidence.
func (s *IntegrationApplicationService) emitSuccessfulGmailDeliveryEvent(ctx context.Context, message integrationmodel.IntegrationOutboxMessage, result OutboxSendResult, principal principalmodel.Principal) error {
	if strings.TrimSpace(message.ConnectorKey) != "google_workspace" || strings.TrimSpace(message.Operation) != "gmail_send_message" {
		return nil
	}
	providerMessageID := gmailString(result.Response, "id")
	threadID := gmailString(result.Response, "threadId")
	rfcMessageID := gmailString(message.Payload, "rfc_message_id")
	if providerMessageID == "" || threadID == "" || rfcMessageID == "" {
		return fmt.Errorf("backend.integration.gmail_send.delivery_receipt_invalid")
	}
	provider := strings.TrimSpace(result.Provider)
	if provider == "" {
		provider = "google"
	}
	payload := map[string]any{
		"connection_key": message.ConnectionKey,
		"outbox_id":      message.ID,
		"rfc_message_id": rfcMessageID,
		"message_id":     providerMessageID,
		"thread_id":      threadID,
		"response_ref":   strings.TrimSpace(result.ResponseRef),
	}
	_, _, err := s.RecordIntegrationEvent(ctx, integrationmodel.IntegrationEventRecordRequest{
		Provider: provider, EventType: "gmail.message.sent",
		ExternalID: "gmail:" + strings.TrimSpace(message.ConnectionKey) + ":sent:" + strings.TrimSpace(message.ID),
		Status:     "received", Payload: payload,
	}, principal)
	return err
}

// emitFailedGmailDeliveryEvent projects a terminal Gmail outbox failure into a
// durable fact. Project-owned communication records can then leave queued state
// without reading Runtime-owned outbox or credential data.
func (s *IntegrationApplicationService) emitFailedGmailDeliveryEvent(ctx context.Context, message integrationmodel.IntegrationOutboxMessage, errorCode string, principal principalmodel.Principal) error {
	if strings.TrimSpace(message.ConnectorKey) != "google_workspace" || strings.TrimSpace(message.Operation) != "gmail_send_message" {
		return nil
	}
	errorCode = strings.TrimSpace(errorCode)
	if errorCode == "" {
		return fmt.Errorf("backend.integration.gmail_send.failure_receipt_invalid")
	}
	payload := map[string]any{
		"connection_key": message.ConnectionKey,
		"outbox_id":      message.ID,
		"rfc_message_id": gmailString(message.Payload, "rfc_message_id"),
		"error_code":     errorCode,
	}
	_, _, err := s.RecordIntegrationEvent(ctx, integrationmodel.IntegrationEventRecordRequest{
		Provider: "google", EventType: "gmail.message.failed",
		ExternalID: "gmail:" + strings.TrimSpace(message.ConnectionKey) + ":failed:" + strings.TrimSpace(message.ID),
		Status:     "received", Payload: payload,
	}, principal)
	return err
}
