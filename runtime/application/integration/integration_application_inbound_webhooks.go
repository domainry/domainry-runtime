package integration

import (
	"context"
	"strings"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationpolicy "github.com/domainry/domainry-runtime/runtime/domain/integration/policy"
	integrationprojection "github.com/domainry/domainry-runtime/runtime/domain/integration/projection"
	integrationvalidation "github.com/domainry/domainry-runtime/runtime/domain/integration/validation"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

var integrationWebhookExternalEventID = integrationpolicy.WebhookExternalEventID

type WebhookReceiveResult struct {
	Challenge        string                                        `json:"challenge,omitempty"`
	ChallengeFormat  string                                        `json:"-"`
	Event            *integrationmodel.IntegrationEvent            `json:"event,omitempty"`
	ExternalIdentity *integrationmodel.IntegrationExternalIdentity `json:"external_identity,omitempty"`
	Duplicate        bool                                          `json:"duplicate,omitempty"`
	Delivery         *integrationmodel.IntegrationOutboxMessage    `json:"delivery,omitempty"`
}

func (s *IntegrationApplicationService) ReceiveIntegrationWebhookForWorkspace(ctx context.Context, workspaceID, connectionKey string, headers, query map[string]string, body []byte) (WebhookReceiveResult, error) {
	return s.receiveIntegrationWebhookForWorkspace(ctx, workspaceID, connectionKey, headers, query, toIntegrationMultiStrings(headers), toIntegrationMultiStrings(query), body)
}

func (s *IntegrationApplicationService) ReceiveIntegrationWebhookValuesForWorkspace(ctx context.Context, workspaceID, connectionKey string, headers, query map[string][]string, body []byte) (WebhookReceiveResult, error) {
	return s.receiveIntegrationWebhookForWorkspace(ctx, workspaceID, connectionKey, firstIntegrationValues(headers), firstIntegrationValues(query), cloneIntegrationMultiStrings(headers), cloneIntegrationMultiStrings(query), body)
}

func (s *IntegrationApplicationService) receiveIntegrationWebhookForWorkspace(ctx context.Context, workspaceID, connectionKey string, headers, query map[string]string, headerValues, queryValues map[string][]string, body []byte) (WebhookReceiveResult, error) {
	if err := ctx.Err(); err != nil {
		return WebhookReceiveResult{}, err
	}
	workspaceID, connectionKey = strings.TrimSpace(workspaceID), strings.TrimSpace(connectionKey)
	if err := integrationAuthorizeWorkspaceCommand(workspaceID); err != nil {
		return WebhookReceiveResult{}, err
	}
	if connectionKey == "" {
		return WebhookReceiveResult{}, badRequest("backend.integration.webhook.connection_required")
	}
	connection, ok, err := s.findConnection(ctx, connectionKey, workspaceID)
	if err != nil {
		return WebhookReceiveResult{}, err
	}
	if !ok || !connectionCanSend(connection) {
		return WebhookReceiveResult{}, notFound("backend.integration.connection.not_found")
	}
	secrets, err := s.ResolveAdapterSecrets(ctx, connection)
	if err != nil {
		return WebhookReceiveResult{}, err
	}
	verified, err := s.verifyRegisteredInboundWebhook(ctx, integrationcontract.InboundWebhookRequest{Connection: connection, Headers: headers, Query: query, HeaderValues: headerValues, QueryValues: queryValues, Secrets: secrets, Body: body, ReceivedAt: time.Now().UTC()})
	if err != nil {
		return WebhookReceiveResult{}, err
	}
	if verified.Challenge != "" {
		return WebhookReceiveResult{Challenge: verified.Challenge, ChallengeFormat: verified.ChallengeFormat}, nil
	}
	security, err := s.validateInboundWebhookSecurity(ctx, connection, verified, time.Now().UTC())
	if err != nil {
		return WebhookReceiveResult{}, err
	}
	externalID, fallbackID, err := integrationWebhookExternalEventID(connection.ProviderKey, connection.Key, verified.EventType, verified.ExternalID, verified.Payload)
	if err != nil {
		return WebhookReceiveResult{}, internalError("fingerprint inbound webhook event", err)
	}
	event := integrationmodel.IntegrationEvent{
		WorkspaceID: connection.WorkspaceID, Provider: connection.ProviderKey, EventType: verified.EventType,
		ExternalID: externalID, Status: "received", Payload: inboundEventPayload(verified.Payload, connection),
	}
	event.Payload["_integration_external_id_fallback"] = fallbackID
	if security != nil {
		event.Payload["_integration_security"] = security
	}
	saved, duplicate, err := s.acceptIntegrationEvent(ctx, event)
	if err != nil {
		return WebhookReceiveResult{}, err
	}
	identity, err := s.WritebackWebhookExternalIdentity(ctx, connection, verified.ExternalIdentity)
	if err != nil {
		_, _ = s.eventRepo.UpdateEventStatus(ctx, connection.WorkspaceID, saved.ID, "failed", valueOrDefault(integrationErrorCode(err), "backend.internal"))
		return WebhookReceiveResult{}, err
	}
	var delivery *integrationmodel.IntegrationOutboxMessage
	if verified.DeliveryReceipt != nil {
		status, normalizeErr := NormalizeOutboxStatus(verified.DeliveryReceipt.Status)
		if normalizeErr != nil {
			_, _ = s.eventRepo.UpdateEventStatus(ctx, connection.WorkspaceID, saved.ID, "failed", valueOrDefault(integrationErrorCode(normalizeErr), "backend.internal"))
			return WebhookReceiveResult{}, normalizeErr
		}
		updated, found, updateErr := s.publicationRepo.UpdateOutboxStatusByResponseRef(ctx, connection.WorkspaceID, connection.Key, strings.TrimSpace(verified.DeliveryReceipt.ResponseRef), status, strings.TrimSpace(verified.DeliveryReceipt.Error))
		if updateErr != nil {
			return WebhookReceiveResult{}, updateErr
		}
		if found {
			delivery = &updated
			principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: connection.WorkspaceID, UserID: "integration:webhook:" + connection.ProviderKey, RoleKey: "integration_webhook"}}
			s.audit(ctx, "integration_outbox_delivery_receipt_applied", "integration_outbox", updated.ID, principal, "Applied provider delivery receipt to integration outbox "+updated.ID, nil, integrationprojection.IntegrationOutboxAuditShape(updated), map[string]any{
				"workspace_id": connection.WorkspaceID, "connector_key": connection.ConnectorKey, "provider_key": connection.ProviderKey,
				"connection_key": connection.Key, "request_ref": updated.RequestRef, "response_ref": verified.DeliveryReceipt.ResponseRef, "status": updated.Status,
			})
		}
	}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: connection.WorkspaceID, UserID: "integration:webhook:" + connection.ProviderKey, RoleKey: "integration_webhook"}}
	s.audit(ctx, "integration_webhook_received", "integration_event", saved.ID, principal, "Received verified integration webhook "+saved.ExternalID, nil, integrationprojection.IntegrationEventAuditShape(saved), map[string]any{
		"workspace_id": connection.WorkspaceID, "connector_key": connection.ConnectorKey, "provider_key": connection.ProviderKey,
		"connection_key": connection.Key, "event_type": saved.EventType, "duplicate": duplicate,
	})
	s.wakeIntegrationEvent(saved)
	return WebhookReceiveResult{Event: &saved, ExternalIdentity: identity, Duplicate: duplicate, Delivery: delivery}, nil
}

func toIntegrationMultiStrings(input map[string]string) map[string][]string {
	if input == nil {
		return nil
	}
	result := make(map[string][]string, len(input))
	for key, value := range input {
		result[key] = []string{value}
	}
	return result
}

func cloneIntegrationMultiStrings(input map[string][]string) map[string][]string {
	if input == nil {
		return nil
	}
	result := make(map[string][]string, len(input))
	for key, values := range input {
		result[key] = append([]string(nil), values...)
	}
	return result
}

func toPublicMultiStrings(input map[string]string) map[string][]string {
	return toIntegrationMultiStrings(input)
}

func firstIntegrationValues(input map[string][]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, values := range input {
		if len(values) > 0 {
			result[key] = values[0]
		}
	}
	return result
}

func integrationTimeString(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func (s *IntegrationApplicationService) validateInboundWebhookSecurity(ctx context.Context, connection integrationmodel.IntegrationConnection, verified integrationcontract.VerifiedInboundWebhook, now time.Time) (map[string]any, error) {
	profile := strings.TrimSpace(integrationConfigStringValue(connection.Config, "inbound_security_profile"))
	if profile == "" {
		return nil, nil
	}
	if profile != integrationmodel.IntegrationInboundSecurityProfileDevice {
		return nil, badRequest("backend.integration.webhook.security_profile_invalid", "profile", profile)
	}
	evidence := integrationcontract.WebhookSecurityEvidence{}
	if verified.Security != nil {
		evidence = *verified.Security
	}
	validation := integrationvalidation.IntegrationValidateInboundSecurity(
		integrationmodel.IntegrationInboundSecurityPolicy{Profile: profile, MaxSkewSeconds: integrationConfigInt64(connection.Config, 300, "inbound_event_max_skew_seconds")},
		integrationmodel.IntegrationInboundSecurityEvidence{SignatureVerified: evidence.SignatureVerified, Nonce: evidence.Nonce, DeviceIdentity: evidence.DeviceIdentity, EventTime: evidence.EventTime, ExternalID: verified.ExternalID, Now: now},
	)
	if !validation.Valid {
		return nil, forbidden("backend.integration.webhook." + validation.Failure)
	}
	nonceScope := strings.TrimSpace(connection.ConnectorKey) + ":" + strings.TrimSpace(connection.Key)
	expiresAt := now.Add(time.Duration(max(1, integrationConfigInt64(connection.Config, 300, "inbound_event_max_skew_seconds"))) * time.Second).Format(time.RFC3339)
	duplicate, err := s.eventRepo.RecordWebhookNonce(ctx, connection.WorkspaceID, nonceScope, strings.TrimSpace(evidence.Nonce), validation.EventTime.Format(time.RFC3339Nano), expiresAt)
	if err != nil {
		return nil, err
	}
	if duplicate {
		return nil, forbidden("backend.integration.webhook.replay_detected")
	}
	return map[string]any{"profile": profile, "device_identity": strings.TrimSpace(evidence.DeviceIdentity), "event_time": validation.EventTime.Format(time.RFC3339Nano), "signature_verified": true}, nil
}

func inboundEventPayload(payload map[string]any, connection integrationmodel.IntegrationConnection) map[string]any {
	result := integrationpolicy.RedactSensitiveMap(cloneMap(payload))
	if result == nil {
		result = map[string]any{}
	}
	result["_integration_context"] = map[string]any{
		"connector_key": connection.ConnectorKey, "provider_key": connection.ProviderKey, "connection_key": connection.Key,
	}
	return result
}
