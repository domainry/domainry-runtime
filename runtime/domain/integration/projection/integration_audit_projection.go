package projection

import (
	"fmt"
	"strings"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func IntegrationEventAuditShape(event integrationmodel.IntegrationEvent) map[string]any {
	return map[string]any{
		"id": event.ID, "workspace_id": event.WorkspaceID, "provider": event.Provider, "event_type": event.EventType,
		"external_id": event.ExternalID, "status": event.Status, "payload_set": len(event.Payload) > 0,
		"error_set": strings.TrimSpace(event.Error) != "", "attempt_count": event.AttemptCount,
		"retry_set": strings.TrimSpace(event.NextRetryAt) != "",
	}
}

func IntegrationInvocationAuditShape(invocation integrationmodel.IntegrationInvocation) map[string]any {
	retryable, _ := invocation.Metadata["retryable"].(bool)
	return map[string]any{
		"id": invocation.ID, "workspace_id": invocation.WorkspaceID, "connector_key": invocation.ConnectorKey,
		"provider_key": invocation.ProviderKey, "connection_key": invocation.ConnectionKey, "operation": invocation.Operation,
		"status": invocation.Status, "duration_ms": invocation.DurationMS, "request_ref": invocation.RequestRef,
		"response_ref": invocation.ResponseRef, "error_set": strings.TrimSpace(invocation.Error) != "",
		"retryable": retryable, "retry_reason": strings.TrimSpace(fmt.Sprint(invocation.Metadata["retry_reason"])),
		"provider_status": strings.TrimSpace(fmt.Sprint(invocation.Metadata["provider_status"])), "event_id": invocation.EventID,
		"object_key": invocation.ObjectKey, "record_id": invocation.RecordID,
	}
}

func IntegrationOutboxAuditShape(message integrationmodel.IntegrationOutboxMessage) map[string]any {
	return map[string]any{
		"id": message.ID, "workspace_id": message.WorkspaceID, "connector_key": message.ConnectorKey,
		"connection_key": message.ConnectionKey, "operation": message.Operation, "status": message.Status,
		"payload_set": len(message.Payload) > 0, "event_id": message.EventID, "request_ref": message.RequestRef,
		"response_ref": message.ResponseRef, "error_set": strings.TrimSpace(message.Error) != "", "attempt_count": message.AttemptCount,
		"next_attempt_at_set": strings.TrimSpace(message.NextAttemptAt) != "", "ack_deadline_at_set": strings.TrimSpace(message.AckDeadlineAt) != "",
	}
}
