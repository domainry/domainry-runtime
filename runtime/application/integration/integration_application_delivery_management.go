package integration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/telemetry"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationpolicy "github.com/domainry/domainry-runtime/runtime/domain/integration/policy"
	integrationprojection "github.com/domainry/domainry-runtime/runtime/domain/integration/projection"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func (s *IntegrationApplicationService) ListIntegrationInvocations(ctx context.Context, connectorKey, recordID, workflowExecutionID, status, provider, externalPrincipal string, limit int, principal principalmodel.Principal) ([]integrationmodel.IntegrationInvocation, error) {
	if err := integrationAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	if !HasPermission(principal, PermissionAuditView) {
		return nil, forbidden("auth.permission_denied")
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	invocations, err := s.invocationRepo.ListInvocations(ctx, principalWorkspaceID(principal), strings.TrimSpace(connectorKey), strings.TrimSpace(recordID), strings.TrimSpace(workflowExecutionID), strings.TrimSpace(status), 200)
	if err != nil {
		return nil, err
	}
	provider, externalPrincipal = strings.TrimSpace(provider), strings.TrimSpace(externalPrincipal)
	if provider == "" && externalPrincipal == "" && len(invocations) <= limit {
		return invocations, nil
	}
	filtered := []integrationmodel.IntegrationInvocation{}
	for _, invocation := range invocations {
		if integrationprojection.IntegrationInvocationMetadataMatches(invocation, provider, externalPrincipal) {
			filtered = append(filtered, invocation)
		}
		if len(filtered) >= limit {
			break
		}
	}
	return filtered, nil
}

func (s *IntegrationApplicationService) UpdateIntegrationInvocationStatus(ctx context.Context, invocationID string, req integrationmodel.IntegrationInvocationStatusRequest, principal principalmodel.Principal) (integrationmodel.IntegrationInvocation, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return integrationmodel.IntegrationInvocation{}, err
	}
	if !HasPermission(principal, PermissionInvoke) {
		return integrationmodel.IntegrationInvocation{}, forbidden("auth.permission_denied")
	}
	status, err := NormalizeInvocationStatus(req.Status)
	if err != nil {
		return integrationmodel.IntegrationInvocation{}, err
	}
	if req.DurationMS < 0 {
		return integrationmodel.IntegrationInvocation{}, badRequest("backend.integration.invocation.invalid_duration")
	}
	saved, err := s.invocationRepo.UpdateInvocationStatus(ctx, principalWorkspaceID(principal), strings.TrimSpace(invocationID), status, req.DurationMS, strings.TrimSpace(req.ResponseRef), strings.TrimSpace(req.Error))
	if err != nil {
		return integrationmodel.IntegrationInvocation{}, err
	}
	s.audit(ctx, "integration_invocation_status_updated", "integration_invocation", saved.ID, principal, "Updated integration invocation status "+saved.ID, nil, integrationprojection.IntegrationInvocationAuditShape(saved), map[string]any{
		"workspace_id": principalWorkspaceID(principal), "connector_key": saved.ConnectorKey, "operation": saved.Operation,
		"status": saved.Status, "error_set": strings.TrimSpace(saved.Error) != "",
	})
	return saved, nil
}

func (s *IntegrationApplicationService) ListIntegrationOutboxMessages(ctx context.Context, connectorKey, status string, limit int, principal principalmodel.Principal) ([]integrationmodel.IntegrationOutboxMessage, error) {
	if err := integrationAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	if !HasPermission(principal, PermissionAuditView) {
		return nil, forbidden("auth.permission_denied")
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	return s.publicationRepo.ListOutbox(ctx, principalWorkspaceID(principal), strings.TrimSpace(connectorKey), strings.TrimSpace(status), limit)
}

func (s *IntegrationApplicationService) InspectIntegrationOutboxMessage(ctx context.Context, messageID string, principal principalmodel.Principal) (integrationmodel.IntegrationOutboxMessage, error) {
	if err := integrationAuthorizeQuery(principal); err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	reader, ok := s.publicationRepo.(integrationrepository.IntegrationOutboxReader)
	if !ok {
		return integrationmodel.IntegrationOutboxMessage{}, internalError("read integration outbox", errors.New("outbox reader unavailable"))
	}
	message, found, err := reader.GetOutbox(ctx, principalWorkspaceID(principal), strings.TrimSpace(messageID))
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	if !found {
		return integrationmodel.IntegrationOutboxMessage{}, notFound("backend.integration.outbox.not_found")
	}
	return message, nil
}

func (s *IntegrationApplicationService) GetBusinessIntegrationIntent(ctx context.Context, messageID string, principal principalmodel.Principal) (integrationmodel.IntegrationIntentResult, error) {
	if err := integrationAuthorizeQuery(principal); err != nil {
		return integrationmodel.IntegrationIntentResult{}, err
	}
	reader, ok := s.publicationRepo.(integrationrepository.IntegrationOutboxReader)
	if !ok {
		return integrationmodel.IntegrationIntentResult{}, internalError("read integration intent", errors.New("outbox reader unavailable"))
	}
	message, found, err := reader.GetOutbox(ctx, principalWorkspaceID(principal), strings.TrimSpace(messageID))
	if err != nil {
		return integrationmodel.IntegrationIntentResult{}, err
	}
	if !found || strings.TrimSpace(message.CreatedBy) == "" || strings.TrimSpace(message.CreatedBy) != strings.TrimSpace(principal.UserID) {
		// Ownership mismatches are deliberately hidden as not-found so a
		// business user cannot enumerate another user's durable intents.
		return integrationmodel.IntegrationIntentResult{}, notFound("backend.integration.intent.not_found")
	}
	result := integrationmodel.IntegrationIntentResult{
		ID: message.ID, ConnectorKey: message.ConnectorKey, ConnectionKey: message.ConnectionKey,
		Operation: message.Operation, Status: message.Status, ResponseRef: message.ResponseRef,
		Error: message.Error, AttemptCount: message.AttemptCount, CreatedAt: message.CreatedAt, UpdatedAt: message.UpdatedAt,
	}
	if s.invocationRepo == nil {
		return result, nil
	}
	invocations, err := s.invocationRepo.ListInvocations(ctx, principalWorkspaceID(principal), message.ConnectorKey, "", "", "", 500)
	if err != nil {
		return integrationmodel.IntegrationIntentResult{}, err
	}
	for _, invocation := range invocations {
		if strings.TrimSpace(fmt.Sprint(invocation.Metadata["source"])) != "outbox" ||
			strings.TrimSpace(fmt.Sprint(invocation.Metadata["source_id"])) != message.ID {
			continue
		}
		result.ProviderKey = invocation.ProviderKey
		result.Status = invocation.Status
		result.ResponseRef = invocation.ResponseRef
		result.Error = invocation.Error
		if response, ok := invocation.Metadata["response"].(map[string]any); ok {
			result.Response = cloneMap(response)
		}
		if invocation.UpdatedAt != "" {
			result.UpdatedAt = invocation.UpdatedAt
		}
		break
	}
	return result, nil
}

func (s *IntegrationApplicationService) UpdateIntegrationOutboxStatus(ctx context.Context, messageID string, req integrationmodel.IntegrationOutboxStatusRequest, principal principalmodel.Principal) (integrationmodel.IntegrationOutboxMessage, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	if !HasPermission(principal, PermissionInvoke) {
		return integrationmodel.IntegrationOutboxMessage{}, forbidden("auth.permission_denied")
	}
	status, err := NormalizeOutboxStatus(req.Status)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	saved, err := s.publicationRepo.UpdateOutboxStatus(ctx, principalWorkspaceID(principal), strings.TrimSpace(messageID), status, strings.TrimSpace(req.ResponseRef), strings.TrimSpace(req.Error))
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	s.audit(ctx, "integration_outbox_status_updated", "integration_outbox", saved.ID, principal, "Updated integration outbox message status "+saved.ID, nil, integrationprojection.IntegrationOutboxAuditShape(saved), map[string]any{
		"workspace_id": principalWorkspaceID(principal), "connector_key": saved.ConnectorKey, "operation": saved.Operation,
		"status": saved.Status, "error_set": strings.TrimSpace(saved.Error) != "",
	})
	return saved, nil
}

func (s *IntegrationApplicationService) ScheduleIntegrationOutboxRetry(ctx context.Context, messageID string, req integrationmodel.IntegrationOutboxRetryRequest, principal principalmodel.Principal) (integrationmodel.IntegrationOutboxMessage, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	if !HasPermission(principal, PermissionRetry) {
		return integrationmodel.IntegrationOutboxMessage{}, forbidden("auth.permission_denied")
	}
	messageID = strings.TrimSpace(messageID)
	reader, ok := s.publicationRepo.(integrationrepository.IntegrationOutboxReader)
	if !ok {
		return integrationmodel.IntegrationOutboxMessage{}, internalError("read integration outbox for retry", errors.New("outbox reader unavailable"))
	}
	existing, found, err := reader.GetOutbox(ctx, principalWorkspaceID(principal), messageID)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	if !found {
		return integrationmodel.IntegrationOutboxMessage{}, notFound("backend.integration.outbox.not_found")
	}
	clearRejection := integrationpolicy.ProviderResponseProvesNoEffect(errors.New(existing.Error), existing.ResponseRef)
	if (existing.Status == "quarantined" || existing.Error == "backend.integration.outbox.outcome_uncertain" || strings.TrimSpace(existing.ResponseRef) != "") && !clearRejection {
		return integrationmodel.IntegrationOutboxMessage{}, conflict("backend.integration.outbox.reconciliation_required")
	}
	scheduledAutomaticRetry := existing.Status == "queued" &&
		strings.TrimSpace(existing.NextAttemptAt) != "" &&
		strings.TrimSpace(existing.Error) != ""
	if existing.Status != "failed" && existing.Status != "dead_letter" && existing.Status != "cancelled" && !(existing.Status == "quarantined" && clearRejection) && !scheduledAutomaticRetry {
		return integrationmodel.IntegrationOutboxMessage{}, badRequest("backend.integration.outbox.not_retryable")
	}
	if strings.TrimSpace(existing.NextAttemptAt) != "" && !scheduledAutomaticRetry {
		return existing, nil
	}
	if existing.ConnectorKey != "__automation__" {
		connection, err := s.IntegrationConnectionForOutboxMessage(ctx, existing, principal, existing.ConnectorKey)
		if err != nil {
			return integrationmodel.IntegrationOutboxMessage{}, err
		}
		if _, err := s.ResolveAdapterSecrets(ctx, connection); err != nil {
			return integrationmodel.IntegrationOutboxMessage{}, err
		}
		payload := cloneMap(existing.Payload)
		delete(payload, "request_id")
		delete(payload, telemetry.AsyncPayloadKey)
		if _, err := s.prepareRegisteredOutboxCall(ctx, existing, connection, principal, payload); err != nil {
			return integrationmodel.IntegrationOutboxMessage{}, err
		}
	}
	retryError := strings.TrimSpace(req.Error)
	if existing.ConnectorKey != "__automation__" && clearRejection {
		// Keep the provider's explicit no-effect rejection on the outbox. The
		// worker re-evaluates retry safety after the message is queued; replacing
		// a 4xx/http:* rejection with an operator note would erase the evidence
		// that makes a non-idempotent write safe to retry.
		retryError = existing.Error
	}
	saved, err := s.publicationRepo.ScheduleOutboxRetry(ctx, principalWorkspaceID(principal), messageID, req.DelaySeconds, retryError)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	s.audit(ctx, "integration_outbox_retry_scheduled", "integration_outbox", saved.ID, principal, "Scheduled integration outbox retry "+saved.ID, nil, integrationprojection.IntegrationOutboxAuditShape(saved), map[string]any{
		"workspace_id": principalWorkspaceID(principal), "connector_key": saved.ConnectorKey, "operation": saved.Operation,
		"attempt_count": saved.AttemptCount, "next_attempt_at_set": strings.TrimSpace(saved.NextAttemptAt) != "",
		"automatic_retry_expedited": scheduledAutomaticRetry,
	})
	return saved, nil
}

func NormalizeInvocationStatus(value string) (string, error) {
	status := strings.TrimSpace(value)
	if status == "" {
		return "queued", nil
	}
	switch status {
	case "queued", "running", "succeeded", "failed", "cancelled":
		return status, nil
	default:
		return "", badRequest("backend.integration.invocation.invalid_status")
	}
}

func NormalizeOutboxStatus(value string) (string, error) {
	status := strings.TrimSpace(value)
	if status == "" {
		return "queued", nil
	}
	switch status {
	case "cancelled", "dead_letter", "delivered", "failed", "quarantined", "queued", "read", "sending", "sent":
		return status, nil
	default:
		return "", badRequest("backend.integration.outbox.invalid_status")
	}
}

func principalWorkspaceID(principal principalmodel.Principal) string {
	return strings.TrimSpace(principal.WorkspaceID)
}

func badRequest(code string, values ...string) error {
	params := map[string]string{}
	for index := 0; index+1 < len(values); index += 2 {
		if key := strings.TrimSpace(values[index]); key != "" {
			params[key] = values[index+1]
		}
	}
	if len(params) == 0 {
		params = nil
	}
	return &apperror.AppError{Kind: apperror.KindBadRequest, Code: code, Params: params}
}
func forbidden(code string) error {
	return &apperror.AppError{Kind: apperror.KindForbidden, Code: code}
}

func conflict(code string, values ...string) error {
	params := map[string]string{}
	for index := 0; index+1 < len(values); index += 2 {
		if key := strings.TrimSpace(values[index]); key != "" {
			params[key] = values[index+1]
		}
	}
	if len(params) == 0 {
		params = nil
	}
	return &apperror.AppError{Kind: apperror.KindConflict, Code: code, Params: params}
}

func internalError(operation string, err error) error {
	return &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.internal", Params: map[string]string{"operation": operation}, Err: err}
}
