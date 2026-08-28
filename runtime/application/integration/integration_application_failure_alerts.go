package integration

import (
	"context"
	"fmt"
	integrationprojection "github.com/domainry/domainry-runtime/runtime/domain/integration/projection"
	integrationruntime "github.com/domainry/domainry-runtime/runtime/domain/integration/runtime"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/mutation"

	"os"
	"strings"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationpolicy "github.com/domainry/domainry-runtime/runtime/domain/integration/policy"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

func integrationFailureClass(err error) workerplatform.FailureClass {
	switch integrationpolicy.ProviderFailureDisposition(err) {
	case integrationpolicy.FailureDispositionCancelled:
		return workerplatform.FailureCancelled
	case integrationpolicy.FailureDispositionTerminal:
		return workerplatform.FailureTerminal
	case integrationpolicy.FailureDispositionRateLimited:
		return workerplatform.FailureRateLimited
	case integrationpolicy.FailureDispositionDependencyUnavailable:
		return workerplatform.FailureDependencyUnavailable
	default:
		return workerplatform.ClassifyApplicationError(err)
	}
}

func (s *IntegrationApplicationService) failDueEvent(ctx context.Context, event integrationmodel.IntegrationEvent, principal principalmodel.Principal, failure error) (integrationmodel.IntegrationEvent, string) {
	errorText := stableIntegrationFailureCode(failure, "backend.integration.event.processing_failed")
	failureClass := integrationFailureClass(failure)
	if !failureClass.Retryable() || event.AttemptCount >= integrationruntime.IntegrationEventMaxAttempts {
		saved, err := s.workerRepo.UpdateEventStatus(ctx, event.WorkspaceID, event.ID, event.LeaseOwner, event.FencingToken, "dead_letter", errorText, s.worker.Clock.Now().Format(time.RFC3339))
		if err != nil {
			if mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
				return event, "skipped"
			}
			saved = event
		}
		s.audit(ctx, "integration_event_dead_lettered", "integration_event", event.ID, principal, "Dead-lettered integration event "+event.ID, integrationprojection.IntegrationEventAuditShape(event), integrationprojection.IntegrationEventAuditShape(saved), map[string]any{"workspace_id": event.WorkspaceID, "provider": event.Provider, "event_type": event.EventType, "external_id": event.ExternalID, "attempt_count": event.AttemptCount, "error_code": errorText, "failure_class": failureClass, "max_attempts": integrationruntime.IntegrationEventMaxAttempts})
		s.enqueueFailureAlert(ctx, "integration_event_dead_letter", map[string]any{"source_type": "integration_event", "source_id": saved.ID, "provider": saved.Provider, "event_type": saved.EventType, "external_id": saved.ExternalID, "status": saved.Status, "attempt_count": saved.AttemptCount, "error_code": errorText, "workspace_id": saved.WorkspaceID}, saved.WorkspaceID, saved.ID, principal)
		return saved, "dead_lettered"
	}
	saved, err := s.workerRepo.ScheduleEventRetry(ctx, event.WorkspaceID, event.ID, event.LeaseOwner, event.FencingToken, s.integrationRetryDelaySeconds(event.AttemptCount), errorText, s.worker.Clock.Now().Format(time.RFC3339))
	if err != nil {
		if mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
			return event, "skipped"
		}
		saved = event
	}
	s.audit(ctx, "integration_event_worker_retry_scheduled", "integration_event", event.ID, principal, "Scheduled integration event retry "+event.ID, integrationprojection.IntegrationEventAuditShape(event), integrationprojection.IntegrationEventAuditShape(saved), map[string]any{"workspace_id": event.WorkspaceID, "provider": event.Provider, "event_type": event.EventType, "external_id": event.ExternalID, "attempt_count": saved.AttemptCount, "next_retry_at_set": strings.TrimSpace(saved.NextRetryAt) != "", "error_code": errorText})
	return saved, "retried"
}

func (s *IntegrationApplicationService) failDueOutboxMessage(ctx context.Context, message integrationmodel.IntegrationOutboxMessage, principal principalmodel.Principal, failure error, responseRefs ...string) (integrationmodel.IntegrationOutboxMessage, string) {
	errorText := stableIntegrationFailureCode(failure, "backend.integration.outbox.send_failed")
	responseRef := ""
	if len(responseRefs) > 0 {
		responseRef = strings.TrimSpace(responseRefs[0])
	}
	if integrationpolicy.IntegrationProviderOutcomeUncertain(failure, responseRef) {
		errorText = "backend.integration.outbox.outcome_uncertain"
		saved, err := s.workerRepo.UpdateOutboxStatus(ctx, message.WorkspaceID, message.ID, message.LeaseOwner, message.FencingToken, "quarantined", responseRef, errorText, "", s.worker.Clock.Now().Format(time.RFC3339))
		if err != nil {
			if mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
				return message, "skipped"
			}
			saved = message
		}
		s.audit(ctx, "integration_outbox_reconciliation_required", "integration_outbox", message.ID, principal, "Provider outcome requires reconciliation before retry", integrationprojection.IntegrationOutboxAuditShape(message), integrationprojection.IntegrationOutboxAuditShape(saved), map[string]any{"workspace_id": message.WorkspaceID, "connector_key": message.ConnectorKey, "connection_key": message.ConnectionKey, "operation": message.Operation, "request_ref": message.RequestRef, "response_ref": responseRef, "attempt_count": message.AttemptCount, "error_code": errorText})
		_ = s.emitFailedAppointmentBookingEvent(ctx, saved, errorText, principal)
		return saved, "reconciliation_required"
	}
	failureClass := integrationFailureClass(failure)
	if !failureClass.Retryable() || message.AttemptCount >= integrationruntime.IntegrationOutboxMaxAttempts {
		if err := s.enqueueNotificationFallback(ctx, message, principal); err != nil {
			fallbackCode := stableIntegrationFailureCode(err, "backend.notification.fallback_enqueue_failed")
			saved, retryErr := s.workerRepo.ScheduleOutboxRetry(ctx, message.WorkspaceID, message.ID, message.LeaseOwner, message.FencingToken, s.integrationRetryDelaySeconds(message.AttemptCount), fallbackCode, s.worker.Clock.Now().Format(time.RFC3339))
			if retryErr == nil {
				s.audit(ctx, "notification_fallback_enqueue_retried", "integration_outbox", message.ID, principal, "Retrying dead-letter transition because notification fallback could not be persisted", integrationprojection.IntegrationOutboxAuditShape(message), integrationprojection.IntegrationOutboxAuditShape(saved), map[string]any{"error_code": fallbackCode})
				return saved, "retried"
			}
		}
		saved, err := s.workerRepo.UpdateOutboxStatus(ctx, message.WorkspaceID, message.ID, message.LeaseOwner, message.FencingToken, "dead_letter", "", errorText, "", s.worker.Clock.Now().Format(time.RFC3339))
		if err != nil {
			if mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
				return message, "skipped"
			}
			saved = message
		}
		s.audit(ctx, "integration_outbox_dead_lettered", "integration_outbox", message.ID, principal, "Dead-lettered integration outbox message "+message.ID, integrationprojection.IntegrationOutboxAuditShape(message), integrationprojection.IntegrationOutboxAuditShape(saved), map[string]any{"workspace_id": message.WorkspaceID, "connector_key": message.ConnectorKey, "operation": message.Operation, "attempt_count": message.AttemptCount, "error_code": errorText, "failure_class": failureClass, "max_attempts": integrationruntime.IntegrationOutboxMaxAttempts})
		if !outboxMessageIsAlert(saved) {
			s.enqueueFailureAlert(ctx, "integration_outbox_dead_letter", map[string]any{"source_type": "integration_outbox", "source_id": saved.ID, "connector_key": saved.ConnectorKey, "connection_key": saved.ConnectionKey, "operation": saved.Operation, "event_id": saved.EventID, "status": saved.Status, "attempt_count": saved.AttemptCount, "error_code": errorText, "workspace_id": saved.WorkspaceID}, saved.WorkspaceID, saved.ID, principal)
		}
		_ = s.emitFailedGmailDeliveryEvent(ctx, saved, errorText, principal)
		_ = s.emitFailedAppointmentBookingEvent(ctx, saved, errorText, principal)
		return saved, "dead_lettered"
	}
	saved, err := s.workerRepo.ScheduleOutboxRetry(ctx, message.WorkspaceID, message.ID, message.LeaseOwner, message.FencingToken, s.integrationRetryDelaySeconds(message.AttemptCount), errorText, s.worker.Clock.Now().Format(time.RFC3339))
	if err != nil {
		if mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
			return message, "skipped"
		}
		saved = message
	}
	s.audit(ctx, "integration_outbox_worker_retry_scheduled", "integration_outbox", message.ID, principal, "Scheduled integration outbox retry "+message.ID, integrationprojection.IntegrationOutboxAuditShape(message), integrationprojection.IntegrationOutboxAuditShape(saved), map[string]any{"workspace_id": message.WorkspaceID, "connector_key": message.ConnectorKey, "operation": message.Operation, "attempt_count": saved.AttemptCount, "next_attempt_at_set": strings.TrimSpace(saved.NextAttemptAt) != "", "error_code": errorText})
	return saved, "retried"
}

func (s *IntegrationApplicationService) integrationRetryDelaySeconds(attemptCount int) int {
	base := integrationruntime.IntegrationRetryDelaySeconds(attemptCount)
	spread := max(1, base/5)
	offset := int(s.worker.Jitter.Duration(time.Duration(spread*2+1)*time.Second)/time.Second) - spread
	delay := base + offset
	if delay < 1 {
		return 1
	}
	return min(delay, 3600)
}

func (s *IntegrationApplicationService) enqueueFailureAlert(ctx context.Context, alertKind string, details map[string]any, workspaceID, sourceID string, principal principalmodel.Principal) {
	config := failureAlertConfig()
	workspaceID = valueOrDefault(strings.TrimSpace(workspaceID), principalWorkspaceID(principal))
	if !config.Enabled {
		s.skipFailureAlert(ctx, alertKind, workspaceID, sourceID, "disabled", "", "", principal)
		return
	}
	connectionKey := config.ConnectionKey
	connectorKey := config.ConnectorKey
	if connection, ok := s.LookupConnection(ctx, connectionKey, workspaceID); ok && connectorKey == "" {
		connectorKey = strings.TrimSpace(connection.ConnectorKey)
	}
	connectorKey = strings.TrimSpace(connectorKey)
	if connectorKey == "" || !s.ConnectorExists(connectorKey) {
		s.skipFailureAlert(ctx, alertKind, workspaceID, sourceID, "connector_missing", connectorKey, connectionKey, principal)
		return
	}
	operation := valueOrDefault(config.Operation, "integration.alert")
	payload := failureAlertPayload(alertKind, details, config, connectorKey, s.worker.Clock.Now())
	if connectorKey == "email" {
		if strings.TrimSpace(config.Recipient) == "" {
			s.skipFailureAlert(ctx, alertKind, workspaceID, sourceID, "email_recipient_missing", connectorKey, connectionKey, principal)
			return
		}
		operation = valueOrDefault(strings.TrimSpace(config.Operation), "email.send")
	}
	message, err := s.EnqueueIntegrationOutboxMessage(ctx, integrationmodel.IntegrationOutboxEnqueueRequest{ConnectorKey: connectorKey, ConnectionKey: connectionKey, Operation: operation, RequestRef: "integration-alert:" + strings.TrimSpace(sourceID), Payload: payload}, integrationruntime.IntegrationWorkerPrincipal(workspaceID))
	if err != nil {
		s.audit(ctx, "integration_alert_enqueue_failed", "integration_alert", strings.TrimSpace(sourceID), principal, "Failed to enqueue integration alert "+strings.TrimSpace(sourceID), nil, nil, map[string]any{"workspace_id": workspaceID, "connector_key": connectorKey, "connection_key": connectionKey, "alert_kind": alertKind, "error_code": stableIntegrationFailureCode(err, "backend.integration.alert_enqueue_failed")})
		return
	}
	s.audit(ctx, "integration_alert_enqueued", "integration_alert", message.ID, principal, "Enqueued integration alert "+message.ID, nil, integrationprojection.IntegrationOutboxAuditShape(message), map[string]any{"workspace_id": workspaceID, "connector_key": connectorKey, "connection_key": connectionKey, "alert_kind": alertKind, "source_id": strings.TrimSpace(sourceID)})
}

func stableIntegrationFailureCode(err error, fallback string) string {
	if code := integrationErrorCode(err); apperror.IsI18nCode(code) {
		return code
	}
	if err != nil {
		if code := strings.TrimSpace(err.Error()); apperror.IsI18nCode(code) {
			return code
		}
	}
	return fallback
}

func (s *IntegrationApplicationService) skipFailureAlert(ctx context.Context, alertKind, workspaceID, sourceID, reason, connectorKey, connectionKey string, principal principalmodel.Principal) {
	s.audit(ctx, "integration_alert_skipped", "integration_alert", strings.TrimSpace(sourceID), principal, "Skipped integration alert "+strings.TrimSpace(sourceID), nil, nil, map[string]any{"workspace_id": valueOrDefault(strings.TrimSpace(workspaceID), principalWorkspaceID(principal)), "connector_key": strings.TrimSpace(connectorKey), "connection_key": strings.TrimSpace(connectionKey), "alert_kind": strings.TrimSpace(alertKind), "source_id": strings.TrimSpace(sourceID), "skip_reason": strings.TrimSpace(reason), "alerts_enabled": strings.TrimSpace(os.Getenv("INTEGRATION_ALERT_CONNECTION_KEY")) != "", "config_disabled": strings.TrimSpace(os.Getenv("INTEGRATION_ALERT_DISABLED")) != ""})
}

type failureAlertOptions struct {
	Enabled       bool
	ConnectorKey  string
	ConnectionKey string
	Operation     string
	Recipient     string
}

func failureAlertConfig() failureAlertOptions {
	disabled := strings.ToLower(strings.TrimSpace(os.Getenv("INTEGRATION_ALERT_DISABLED")))
	if disabled == "1" || disabled == "true" || disabled == "yes" || disabled == "on" {
		return failureAlertOptions{}
	}
	connectionKey := strings.TrimSpace(os.Getenv("INTEGRATION_ALERT_CONNECTION_KEY"))
	if connectionKey == "" {
		return failureAlertOptions{}
	}
	return failureAlertOptions{Enabled: true, ConnectorKey: strings.TrimSpace(os.Getenv("INTEGRATION_ALERT_CONNECTOR_KEY")), ConnectionKey: connectionKey, Operation: strings.TrimSpace(os.Getenv("INTEGRATION_ALERT_OPERATION")), Recipient: strings.TrimSpace(os.Getenv("INTEGRATION_ALERT_RECIPIENT"))}
}

func failureAlertPayload(alertKind string, details map[string]any, config failureAlertOptions, connectorKey string, now time.Time) map[string]any {
	payload := integrationpolicy.RedactSensitiveMap(cloneMap(details))
	if payload == nil {
		payload = map[string]any{}
	}
	payload["alert_kind"], payload["severity"], payload["occurred_at"] = strings.TrimSpace(alertKind), "critical", now.UTC().Format(time.RFC3339)
	if connectorKey == "email" {
		payload["to"] = []string{strings.TrimSpace(config.Recipient)}
		payload["subject"] = "Integration alert: " + strings.TrimSpace(alertKind)
		payload["text"] = failureAlertText(payload)
	}
	return payload
}

func failureAlertText(payload map[string]any) string {
	return strings.Join([]string{"Integration alert", "Kind: " + strings.TrimSpace(fmt.Sprint(payload["alert_kind"])), "Severity: " + strings.TrimSpace(fmt.Sprint(payload["severity"])), "Source: " + strings.TrimSpace(fmt.Sprint(payload["source_type"])) + " " + strings.TrimSpace(fmt.Sprint(payload["source_id"])), "Status: " + strings.TrimSpace(fmt.Sprint(payload["status"])), "Attempts: " + strings.TrimSpace(fmt.Sprint(payload["attempt_count"])), "Error: " + strings.TrimSpace(fmt.Sprint(payload["error"])), "Occurred at: " + strings.TrimSpace(fmt.Sprint(payload["occurred_at"]))}, "\n")
}

func outboxMessageIsAlert(message integrationmodel.IntegrationOutboxMessage) bool {
	if strings.HasPrefix(strings.TrimSpace(message.Operation), "integration.alert") {
		return true
	}
	alertKind, ok := message.Payload["alert_kind"]
	return ok && alertKind != nil && strings.TrimSpace(fmt.Sprint(alertKind)) != ""
}
