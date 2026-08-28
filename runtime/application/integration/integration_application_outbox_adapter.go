package integration

import (
	"context"
	"errors"
	"fmt"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"strings"
	"time"

	"github.com/domainry/domainry-foundation/telemetry"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationpolicy "github.com/domainry/domainry-runtime/runtime/domain/integration/policy"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
)

func (s *IntegrationApplicationService) SendAdapterOutboxMessage(ctx context.Context, message integrationmodel.IntegrationOutboxMessage, principal principalmodel.Principal) (sendResult OutboxSendResult, err error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return OutboxSendResult{}, err
	}
	if err := integrationAuthorizeWorkspaceCommand(message.WorkspaceID); err != nil {
		return OutboxSendResult{}, err
	}
	if strings.TrimSpace(message.WorkspaceID) != principalWorkspaceID(principal) {
		return OutboxSendResult{}, forbidden("auth.permission_denied")
	}
	started := time.Now().UTC()
	defer func() {
		if s.operationalMetrics != nil {
			s.operationalMetrics.observeConnector(message.ConnectorKey, time.Since(started), err)
		}
	}()
	connectionKey := strings.TrimSpace(message.ConnectionKey)
	if connectionKey == "" {
		return OutboxSendResult{}, errors.New("backend.integration.outbox.connection_required")
	}
	workspaceID := strings.TrimSpace(message.WorkspaceID)
	connection, ok, err := s.findConnection(ctx, connectionKey, workspaceID)
	if err != nil {
		return OutboxSendResult{}, err
	}
	if !ok || !connectionCanSend(connection) || connection.ConnectorKey != message.ConnectorKey {
		return OutboxSendResult{}, errors.New("backend.integration.outbox.connection_unavailable")
	}
	requestPayload := cloneMap(message.Payload)
	delete(requestPayload, "request_id")
	delete(requestPayload, telemetry.AsyncPayloadKey)
	auditRequestPayload := cloneMap(requestPayload)
	prepared, err := s.prepareRegisteredOutboxCall(ctx, message, connection, principal, requestPayload)
	if err != nil {
		return OutboxSendResult{}, err
	}
	if message.ConnectorKey == "notification" && message.Operation == "send" {
		hydrated, hydrateErr := s.hydrateWebPushSubscription(ctx, workspaceID, requestPayload)
		err = hydrateErr
		if err != nil {
			return OutboxSendResult{}, err
		}
		// The operation contract validates the public subscription_id payload.
		// Mutate the captured request only after validation so endpoint key
		// material exists solely during Provider execution and never in audit.
		for key := range requestPayload {
			delete(requestPayload, key)
		}
		for key, value := range hydrated {
			requestPayload[key] = value
		}
	}
	if integrationpolicy.IntegrationConnectionUsesRefreshToken(connection) {
		releaseCredentialLease, err := s.AcquireCredentialRefreshLease(ctx, connection, 0)
		if err != nil {
			return OutboxSendResult{}, err
		}
		defer releaseCredentialLease()
	}
	secrets, err := s.ResolveAdapterSecrets(ctx, connection)
	if err != nil {
		return OutboxSendResult{}, err
	}
	webhookPolicy := isManagedHTTPWebhookOutbox(connection)
	if webhookPolicy {
		if err := s.BeforeGenericWebhookSend(ctx, connection, time.Now().UTC()); err != nil {
			return OutboxSendResult{}, err
		}
	}
	result, callErr := prepared.Execute(ctx, secrets)
	if webhookPolicy {
		_ = s.AfterGenericWebhookSend(ctx, connection, callErr == nil, time.Now().UTC())
	}
	if persistErr := s.PersistAdapterSecretUpdates(ctx, connection, secrets, result.SecretUpdates); persistErr != nil {
		return OutboxSendResult{ResponseRef: result.ResponseRef}, persistErr
	}
	s.recordProviderResourceHealthFromCall(ctx, connection, result.ResourceHealth)
	if callErr == nil && connection.Status == "degraded" {
		s.recordCredentialRefreshRecovery(ctx, connection, principal)
	}
	if callErr != nil && ctx.Err() == nil && result.ResponseRef == "oauth:refresh_failed" {
		s.recordCredentialRefreshFailure(ctx, connection, principal, callErr)
	}
	status, errorText := "succeeded", ""
	if callErr != nil {
		status, errorText = "failed", RedactSecretText(callErr.Error(), secrets)
	}
	if _, evidenceErr := s.RecordIntegrationExecutionEvidence(ctx, ExecutionEvidence{
		Connection: connection, Operation: prepared.Operation, Status: status, StartedAt: started,
		RequestRef: message.RequestRef, ResponseRef: result.ResponseRef, Error: errorText, EventID: message.EventID,
		Request: auditRequestPayload, Response: RedactProviderResponse(result.Response, secrets), Source: "outbox", SourceID: message.ID, ErrorCode: result.ProviderErrorCode,
	}, principal); evidenceErr != nil {
		return OutboxSendResult{ResponseRef: result.ResponseRef}, evidenceErr
	}
	if callErr != nil {
		if result.ProviderErrorCode == "notification.subscription_expired" {
			if repository, ok := s.deliveryRepo.(integrationrepository.WebPushSubscriptionRepository); ok {
				_ = repository.ExpireWebPushSubscription(ctx, workspaceID, strings.TrimSpace(fmt.Sprint(message.Payload["subscription_id"])))
			}
		}
		return OutboxSendResult{ResponseRef: result.ResponseRef}, callErr
	}
	return OutboxSendResult{
		Status: "sent", ResponseRef: result.ResponseRef,
		AckTimeoutSeconds: integrationpolicy.IntegrationConfigInt(connection.Config, 0, "outbox_ack_timeout_seconds"),
		Provider:          strings.TrimSpace(connection.ProviderKey), Response: RedactProviderResponse(result.Response, secrets),
	}, nil
}

func isManagedHTTPWebhookOutbox(connection integrationmodel.IntegrationConnection) bool {
	return strings.TrimSpace(connection.ConnectorKey) == "webhook" && strings.TrimSpace(connection.ProviderKey) == "http"
}
