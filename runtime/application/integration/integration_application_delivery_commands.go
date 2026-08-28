package integration

import (
	"context"
	integrationprojection "github.com/domainry/domainry-runtime/runtime/domain/integration/projection"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"strings"

	"github.com/domainry/domainry-foundation/requestcontext"
	"github.com/domainry/domainry-foundation/telemetry"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationpolicy "github.com/domainry/domainry-runtime/runtime/domain/integration/policy"
)

func (s *IntegrationApplicationService) RecordIntegrationInvocation(ctx context.Context, req integrationmodel.IntegrationInvocationRecordRequest, principal principalmodel.Principal) (integrationmodel.IntegrationInvocation, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return integrationmodel.IntegrationInvocation{}, err
	}
	if !HasPermission(principal, PermissionInvoke) {
		return integrationmodel.IntegrationInvocation{}, forbidden("auth.permission_denied")
	}
	connectorKey, operation := strings.TrimSpace(req.ConnectorKey), strings.TrimSpace(req.Operation)
	if connectorKey == "" || operation == "" {
		return integrationmodel.IntegrationInvocation{}, badRequest("backend.integration.invocation.missing_identity")
	}
	if s.resolveInvocationProvider == nil {
		return integrationmodel.IntegrationInvocation{}, notFound("backend.integration.connector.not_found")
	}
	providerKey, err := s.resolveInvocationProvider(ctx, connectorKey, strings.TrimSpace(req.ConnectionKey), strings.TrimSpace(req.ProviderKey), principalWorkspaceID(principal))
	if err != nil {
		return integrationmodel.IntegrationInvocation{}, err
	}
	status, err := NormalizeInvocationStatus(req.Status)
	if err != nil {
		return integrationmodel.IntegrationInvocation{}, err
	}
	if req.DurationMS < 0 {
		return integrationmodel.IntegrationInvocation{}, badRequest("backend.integration.invocation.invalid_duration")
	}
	metadata := integrationpolicy.RedactSensitiveMap(cloneMap(req.Metadata))
	if metadata == nil {
		metadata = map[string]any{}
	}
	if principal.RequestID != "" {
		metadata["request_id"] = principal.RequestID
	}
	if principal.UserID != "" {
		metadata["actor_id"] = principal.UserID
	}
	if principal.RoleKey != "" {
		metadata["role_key"] = principal.RoleKey
	}
	invocation := integrationmodel.IntegrationInvocation{
		WorkspaceID: principalWorkspaceID(principal), ConnectorKey: connectorKey, ProviderKey: providerKey,
		ConnectionKey: strings.TrimSpace(req.ConnectionKey), Operation: operation, Status: status, DurationMS: req.DurationMS,
		RequestRef: strings.TrimSpace(req.RequestRef), ResponseRef: strings.TrimSpace(req.ResponseRef), Error: strings.TrimSpace(req.Error),
		EventID: strings.TrimSpace(req.EventID), ObjectKey: strings.TrimSpace(req.ObjectKey), RecordID: strings.TrimSpace(req.RecordID),
		WorkflowExecutionID: strings.TrimSpace(req.WorkflowExecutionID), Metadata: metadata,
	}
	saved, err := s.deliveryRepo.InsertInvocation(ctx, invocation.WorkspaceID, invocation)
	if err != nil {
		return integrationmodel.IntegrationInvocation{}, err
	}
	s.audit(ctx, "integration_invocation_recorded", "integration_invocation", saved.ID, principal, "Recorded integration invocation "+saved.Operation, nil, integrationprojection.IntegrationInvocationAuditShape(saved), map[string]any{
		"workspace_id": principalWorkspaceID(principal), "connector_key": saved.ConnectorKey, "connection_key": saved.ConnectionKey,
		"operation": saved.Operation, "status": saved.Status, "event_id": saved.EventID, "record_id": saved.RecordID,
	})
	return saved, nil
}

func (s *IntegrationApplicationService) EnqueueIntegrationOutboxMessage(ctx context.Context, req integrationmodel.IntegrationOutboxEnqueueRequest, principal principalmodel.Principal) (integrationmodel.IntegrationOutboxMessage, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	if !HasPermission(principal, PermissionInvoke) {
		return integrationmodel.IntegrationOutboxMessage{}, forbidden("auth.permission_denied")
	}
	connectorKey, operation := strings.TrimSpace(req.ConnectorKey), strings.TrimSpace(req.Operation)
	if connectorKey == "" || operation == "" {
		return integrationmodel.IntegrationOutboxMessage{}, badRequest("backend.integration.outbox.missing_identity")
	}
	if !s.connectorExists(connectorKey) {
		return integrationmodel.IntegrationOutboxMessage{}, notFound("backend.integration.connector.not_found")
	}
	payload := integrationpolicy.RedactSensitiveMap(cloneMap(req.Payload))
	if payload == nil {
		payload = map[string]any{}
	}
	if principal.RequestID != "" {
		payload["request_id"] = principal.RequestID
	}
	correlationID := requestcontext.CorrelationID(ctx)
	if correlationID == "" {
		correlationID = principal.RequestID
	}
	payload[telemetry.AsyncPayloadKey] = telemetry.CaptureAsyncLink(ctx, correlationID)
	requestRef := strings.TrimSpace(req.RequestRef)
	if requestRef == "" {
		requestRef = strings.TrimSpace(principal.RequestID)
	}
	if requestRef == "" {
		return integrationmodel.IntegrationOutboxMessage{}, badRequest("backend.idempotency.key_required")
	}
	dedupKey := strings.TrimSpace(req.DedupKey)
	if dedupKey == "" {
		dedupKey = runtimeext.DurableIntentBatchEntryKey(payload)
	}
	message := integrationmodel.IntegrationOutboxMessage{
		WorkspaceID: principalWorkspaceID(principal), ConnectorKey: connectorKey, ConnectionKey: strings.TrimSpace(req.ConnectionKey),
		Operation: operation, Status: "queued", Payload: payload, EventID: strings.TrimSpace(req.EventID),
		RequestRef: requestRef, DedupKey: valueOrDefault(dedupKey, requestRef), CreatedBy: principal.UserID,
	}
	saved, err := s.deliveryRepo.InsertOutbox(ctx, message.WorkspaceID, message)
	if err != nil {
		return integrationmodel.IntegrationOutboxMessage{}, err
	}
	s.audit(ctx, "integration_outbox_enqueued", "integration_outbox", saved.ID, principal, "Enqueued integration outbox message "+saved.Operation, nil, integrationprojection.IntegrationOutboxAuditShape(saved), map[string]any{
		"workspace_id": principalWorkspaceID(principal), "connector_key": saved.ConnectorKey, "operation": saved.Operation, "event_id": saved.EventID,
	})
	s.wakeIntegrationOutbox(saved)
	return saved, nil
}
