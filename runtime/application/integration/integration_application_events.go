package integration

import (
	"context"
	integrationprojection "github.com/domainry/domainry-runtime/runtime/domain/integration/projection"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func (s *IntegrationApplicationService) acceptIntegrationEvent(ctx context.Context, event integrationmodel.IntegrationEvent) (integrationmodel.IntegrationEvent, bool, error) {
	intent := integrationmodel.IntegrationEventMappingIntent{
		TargetType: "unmatched",
		Status:     "pending",
		Payload: map[string]any{
			"provider": event.Provider, "event_type": event.EventType, "external_id": event.ExternalID,
		},
	}
	if s.registry != nil {
		if mapping, ok := s.EventMappingForEvent(event); ok {
			intent.MappingKey = strings.TrimSpace(mapping.Key)
			intent.TargetType = strings.TrimSpace(mapping.TargetType)
			intent.Payload["mapping_key"] = intent.MappingKey
		} else if _, ok := s.IntegrationEventHandler(event.Provider); ok {
			intent.MappingKey = "provider:" + strings.TrimSpace(event.Provider)
			intent.TargetType = "provider_handler"
		}
	}
	saved, duplicate, err := s.eventRepo.AcceptEvent(ctx, event.WorkspaceID, event, intent)
	if err != nil {
		return integrationmodel.IntegrationEvent{}, false, err
	}
	if duplicate && saved.Error == "backend.integration.event.external_id_conflict" {
		return saved, true, conflict("backend.integration.event.external_id_conflict")
	}
	return saved, duplicate, nil
}

const eventContextKey = "_integration_context"

func (s *IntegrationApplicationService) RecordIntegrationEvent(ctx context.Context, req integrationmodel.IntegrationEventRecordRequest, principal principalmodel.Principal) (integrationmodel.IntegrationEvent, bool, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return integrationmodel.IntegrationEvent{}, false, err
	}
	if !HasPermission(principal, PermissionInvoke) {
		return integrationmodel.IntegrationEvent{}, false, forbidden("auth.permission_denied")
	}
	provider, eventType, externalID := strings.TrimSpace(req.Provider), strings.TrimSpace(req.EventType), strings.TrimSpace(req.ExternalID)
	if provider == "" || eventType == "" || externalID == "" {
		return integrationmodel.IntegrationEvent{}, false, badRequest("backend.integration.event.missing_identity")
	}
	status, err := NormalizeEventStatus(req.Status)
	if err != nil {
		return integrationmodel.IntegrationEvent{}, false, err
	}
	event := integrationmodel.IntegrationEvent{WorkspaceID: principalWorkspaceID(principal), Provider: provider, EventType: eventType, ExternalID: externalID, Status: status, Payload: cloneMap(req.Payload)}
	delete(event.Payload, eventContextKey)
	saved, duplicate, err := s.acceptIntegrationEvent(ctx, event)
	if err != nil {
		if apperror.CodeOf(err) == "backend.integration.event.external_id_conflict" {
			s.audit(ctx, "integration_event_reconciliation_required", "integration_event", saved.ID, principal, "Integration event external identity has conflicting content", integrationprojection.IntegrationEventAuditShape(saved), integrationprojection.IntegrationEventAuditShape(saved), map[string]any{
				"workspace_id": principalWorkspaceID(principal), "provider": provider, "event_type": eventType, "external_id": externalID, "conflict": true,
			})
		}
		return integrationmodel.IntegrationEvent{}, false, err
	}
	action, summary := "integration_event_received", "Received integration event "+saved.ExternalID
	if duplicate {
		action, summary = "integration_event_duplicate", "Duplicate integration event "+saved.ExternalID
	}
	s.audit(ctx, action, "integration_event", saved.ID, principal, summary, nil, integrationprojection.IntegrationEventAuditShape(saved), map[string]any{
		"workspace_id": principalWorkspaceID(principal), "provider": saved.Provider, "event_type": saved.EventType,
		"external_id": saved.ExternalID, "status": saved.Status,
	})
	s.wakeIntegrationEvent(saved)
	return saved, duplicate, nil
}

// RecoverOfflineIntegrationEvents re-enters disconnected edge facts through
// the same immutable external-event identity and mapping-intent boundary used
// by online delivery. Conflicting content is isolated for reconciliation while
// independent events in the batch continue to converge.
func (s *IntegrationApplicationService) RecoverOfflineIntegrationEvents(ctx context.Context, req integrationmodel.IntegrationOfflineEventRecoveryRequest, principal principalmodel.Principal) (integrationmodel.IntegrationOfflineEventRecoveryResult, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return integrationmodel.IntegrationOfflineEventRecoveryResult{}, err
	}
	if !HasPermission(principal, PermissionInvoke) {
		return integrationmodel.IntegrationOfflineEventRecoveryResult{}, forbidden("auth.permission_denied")
	}
	if len(req.Events) == 0 || len(req.Events) > 500 {
		return integrationmodel.IntegrationOfflineEventRecoveryResult{}, badRequest("backend.integration.offline_recovery.batch_size_invalid")
	}
	result := integrationmodel.IntegrationOfflineEventRecoveryResult{Events: []integrationmodel.IntegrationEvent{}, ConflictExternalIDs: []string{}}
	for _, request := range req.Events {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		request.Status = "received"
		event, duplicate, err := s.RecordIntegrationEvent(ctx, request, principal)
		if err != nil {
			if apperror.CodeOf(err) == "backend.integration.event.external_id_conflict" {
				result.ReconciliationRequired++
				result.ConflictExternalIDs = append(result.ConflictExternalIDs, strings.TrimSpace(request.ExternalID))
				continue
			}
			return result, err
		}
		result.Events = append(result.Events, event)
		if duplicate {
			result.Duplicates++
		} else {
			result.Accepted++
		}
	}
	return result, nil
}

func (s *IntegrationApplicationService) UpdateIntegrationEventStatus(ctx context.Context, eventID string, req integrationmodel.IntegrationEventStatusRequest, principal principalmodel.Principal) (integrationmodel.IntegrationEvent, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return integrationmodel.IntegrationEvent{}, err
	}
	if !HasPermission(principal, PermissionInvoke) {
		return integrationmodel.IntegrationEvent{}, forbidden("auth.permission_denied")
	}
	status, err := NormalizeEventStatus(req.Status)
	if err != nil {
		return integrationmodel.IntegrationEvent{}, err
	}
	saved, err := s.eventRepo.UpdateEventStatus(ctx, principalWorkspaceID(principal), strings.TrimSpace(eventID), status, strings.TrimSpace(req.Error))
	if err != nil {
		return integrationmodel.IntegrationEvent{}, err
	}
	s.audit(ctx, "integration_event_status_updated", "integration_event", saved.ID, principal, "Updated integration event status "+saved.ID, nil, integrationprojection.IntegrationEventAuditShape(saved), map[string]any{
		"workspace_id": principalWorkspaceID(principal), "provider": saved.Provider, "event_type": saved.EventType,
		"external_id": saved.ExternalID, "status": saved.Status, "error_set": strings.TrimSpace(saved.Error) != "",
	})
	return saved, nil
}

func (s *IntegrationApplicationService) ScheduleIntegrationEventRetry(ctx context.Context, eventID string, req integrationmodel.IntegrationEventRetryRequest, principal principalmodel.Principal) (integrationmodel.IntegrationEvent, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return integrationmodel.IntegrationEvent{}, err
	}
	if !HasPermission(principal, PermissionRetry) {
		return integrationmodel.IntegrationEvent{}, forbidden("auth.permission_denied")
	}
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return integrationmodel.IntegrationEvent{}, badRequest("backend.integration.event.missing_identity")
	}
	existing, found, err := s.eventRepo.GetEvent(ctx, principalWorkspaceID(principal), eventID)
	if err != nil {
		return integrationmodel.IntegrationEvent{}, err
	}
	if !found {
		return integrationmodel.IntegrationEvent{}, notFound("backend.integration.event.not_found")
	}
	if existing.Status != "failed" && existing.Status != "dead_letter" && existing.Status != "quarantined" {
		return integrationmodel.IntegrationEvent{}, badRequest("backend.integration.event.not_retryable")
	}
	if strings.TrimSpace(existing.NextRetryAt) != "" {
		return existing, nil
	}
	saved, err := s.eventRepo.ScheduleEventRetry(ctx, principalWorkspaceID(principal), eventID, req.DelaySeconds, strings.TrimSpace(req.Error))
	if err != nil {
		return integrationmodel.IntegrationEvent{}, err
	}
	s.audit(ctx, "integration_event_retry_scheduled", "integration_event", saved.ID, principal, "Scheduled integration event retry "+saved.ID, integrationprojection.IntegrationEventAuditShape(existing), integrationprojection.IntegrationEventAuditShape(saved), map[string]any{
		"workspace_id": principalWorkspaceID(principal), "provider": saved.Provider, "event_type": saved.EventType,
		"external_id": saved.ExternalID, "previous_status": existing.Status, "retry_status": saved.Status,
		"attempt_count": saved.AttemptCount, "next_retry_at_set": strings.TrimSpace(saved.NextRetryAt) != "",
	})
	return saved, nil
}

func (s *IntegrationApplicationService) ReplayIntegrationEvent(ctx context.Context, eventID string, principal principalmodel.Principal) (integrationmodel.IntegrationEvent, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return integrationmodel.IntegrationEvent{}, err
	}
	if !HasPermission(principal, PermissionRetry) {
		return integrationmodel.IntegrationEvent{}, forbidden("auth.permission_denied")
	}
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return integrationmodel.IntegrationEvent{}, badRequest("backend.integration.event.missing_identity")
	}
	existing, found, err := s.eventRepo.GetEvent(ctx, principalWorkspaceID(principal), eventID)
	if err != nil {
		return integrationmodel.IntegrationEvent{}, err
	}
	if !found {
		return integrationmodel.IntegrationEvent{}, notFound("backend.integration.event.not_found")
	}
	if err := s.validateIntegrationEventReplayReadiness(ctx, existing, principal); err != nil {
		return integrationmodel.IntegrationEvent{}, err
	}
	if existing.Status == "received" && (strings.TrimSpace(existing.Error) == "" || strings.TrimSpace(existing.Error) == "backend.integration.event.manual_replay_queued") && strings.TrimSpace(existing.NextRetryAt) == "" {
		return existing, nil
	}
	if existing.Status != "failed" && existing.Status != "dead_letter" && existing.Status != "quarantined" && existing.Status != "ignored" {
		return integrationmodel.IntegrationEvent{}, badRequest("backend.integration.event.not_replayable")
	}
	saved, err := s.eventRepo.UpdateEventStatus(ctx, principalWorkspaceID(principal), eventID, "received", "backend.integration.event.manual_replay_queued")
	if err != nil {
		return integrationmodel.IntegrationEvent{}, err
	}
	s.audit(ctx, "integration_event_replayed", "integration_event", saved.ID, principal, "Replayed integration event "+saved.ID, integrationprojection.IntegrationEventAuditShape(existing), integrationprojection.IntegrationEventAuditShape(saved), map[string]any{
		"workspace_id": principalWorkspaceID(principal), "provider": saved.Provider, "event_type": saved.EventType,
		"external_id": saved.ExternalID, "previous_status": existing.Status, "replayed_status": saved.Status, "attempt_count": saved.AttemptCount,
	})
	s.wakeIntegrationEvent(saved)
	return saved, nil
}

func NormalizeEventStatus(value string) (string, error) {
	status := strings.TrimSpace(value)
	if status == "" {
		return "received", nil
	}
	switch status {
	case "received", "processing", "processed", "ignored", "failed", "dead_letter", "quarantined":
		return status, nil
	default:
		return "", badRequest("backend.integration.event.invalid_status")
	}
}

func notFound(code string, values ...string) error {
	params := map[string]string{}
	for index := 0; index+1 < len(values); index += 2 {
		if key := strings.TrimSpace(values[index]); key != "" {
			params[key] = values[index+1]
		}
	}
	if len(params) == 0 {
		params = nil
	}
	return &apperror.AppError{Kind: apperror.KindNotFound, Code: code, Params: params}
}
