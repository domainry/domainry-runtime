package integration

import integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
import integrationpolicy "github.com/domainry/domainry-runtime/runtime/domain/integration/policy"

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"strings"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
)

func syncHTTPStatusFromError(errorText string) int {
	var status int
	_, _ = fmt.Sscan(strings.TrimPrefix(errorText, "backend.integration.sync_call.http_status_"), &status)
	return status
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:16]
}

func (s *IntegrationApplicationService) AdapterForConnection(connection integrationmodel.IntegrationConnection) (integrationcontract.Adapter, bool) {
	if s.registry == nil {
		return nil, false
	}
	return s.registry.ProviderAdapter(strings.TrimSpace(connection.ConnectorKey), strings.TrimSpace(connection.ProviderKey))
}

// ValidateActionDurableIntent resolves the current Connection, Runtime catalog
// and Provider catalog before an Action may append an intent to its UoW. It
// performs no external I/O and does not enqueue outside that UoW.
func (s *IntegrationApplicationService) ValidateActionDurableIntent(ctx context.Context, intent runtimeext.DurableIntent, principal principalmodel.Principal) error {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return err
	}
	if !intent.Valid() {
		return badRequest("backend.action.durable_intent_invalid")
	}
	connectorKey, connectionKey := strings.TrimSpace(intent.ConsumerKey), strings.TrimSpace(intent.ConnectionKey)
	if !s.connectorExists(connectorKey) {
		return notFound("backend.integration.connector.not_found")
	}
	connection, ok, err := s.findConnection(ctx, connectionKey, principalWorkspaceID(principal))
	if err != nil {
		return err
	}
	if !ok || !connectionCanSend(connection) || strings.TrimSpace(connection.ConnectorKey) != connectorKey {
		return badRequest("backend.integration.connection_unavailable")
	}
	if !s.ProviderSupportsIntegrationOperation(connection, intent.OperationKey) {
		return badRequest("backend.integration.operation.provider_unsupported", "connector", connectorKey, "provider", connection.ProviderKey, "operation", intent.OperationKey)
	}
	operation, err := s.IntegrationOperation(connection, intent.OperationKey)
	if err != nil {
		return err
	}
	if _, err := s.actionOutboxOperationIdentity(connection, operation, intent.ContractSHA256); err != nil {
		return err
	}
	return s.validateOperationInput(connectorKey, operation, intent.Payload)
}

func (s *IntegrationApplicationService) actionOutboxOperationIdentity(connection integrationmodel.IntegrationConnection, operation integrationmodel.ConnectorOperationSchema, contractSHA256 string) (integrationcontract.OperationIdentity, error) {
	mode := strings.TrimSpace(operation.ExecutionMode)
	if (mode != "async" && mode != "operation") || strings.TrimSpace(operation.SideEffect) == "read" {
		return integrationcontract.OperationIdentity{}, badRequest(runtimeext.ConnectorActionSideEffectOutboxErrorCode, "connector", connection.ConnectorKey, "operation", operation.Key)
	}
	adapter, ok := s.AdapterForConnection(connection)
	if !ok {
		return integrationcontract.OperationIdentity{}, badRequest("backend.integration.sync_call.connector_unsupported", "connector", connection.ConnectorKey)
	}
	identities, ok := adapter.(integrationcontract.OperationIdentityProvider)
	if !ok {
		return integrationcontract.OperationIdentity{}, badRequest("backend.integration.sync_call.operation_identity_unavailable", "connector", connection.ConnectorKey, "provider", connection.ProviderKey)
	}
	identity, ok := identities.OperationIdentity(operation.Key)
	if !ok {
		return integrationcontract.OperationIdentity{}, badRequest("backend.integration.operation.provider_unsupported", "connector", connection.ConnectorKey, "provider", connection.ProviderKey, "operation", operation.Key)
	}
	if identity.Mode != "enqueue" && identity.Mode != "start_operation" {
		return integrationcontract.OperationIdentity{}, badRequest(runtimeext.ConnectorActionSideEffectOutboxErrorCode, "connector", connection.ConnectorKey, "operation", operation.Key)
	}
	if identity.Effect == "read" || identity.Effect != strings.TrimSpace(operation.SideEffect) {
		return integrationcontract.OperationIdentity{}, badRequest("backend.action.durable_intent.operation_effect_mismatch", "expected", operation.SideEffect, "actual", identity.Effect)
	}
	if strings.TrimSpace(contractSHA256) != identity.ContractSHA256 {
		return integrationcontract.OperationIdentity{}, badRequest("backend.action.durable_intent.operation_contract_mismatch", "expected", identity.ContractSHA256, "actual", contractSHA256)
	}
	return identity, nil
}

func (s *IntegrationApplicationService) ValidateAdapterConfig(connection integrationmodel.IntegrationConnection) error {
	if connection.Status == "disabled" {
		return nil
	}
	adapter, ok := s.AdapterForConnection(connection)
	if !ok {
		return nil
	}
	validator, ok := adapter.(integrationcontract.ConfigValidator)
	if !ok {
		return nil
	}
	if err := validator.ValidateConfig(connection); err != nil {
		var appErr *apperror.AppError
		if errors.As(err, &appErr) {
			return err
		}
		code := strings.TrimSpace(err.Error())
		if index := strings.IndexByte(code, ':'); index > 0 {
			code = strings.TrimSpace(code[:index])
		}
		return badRequest(valueOrDefault(code, "backend.integration.connection.config_invalid"))
	}
	return nil
}

func (s *IntegrationApplicationService) prepareRegisteredSyncCall(connection integrationmodel.IntegrationConnection, connectorKey string) (PreparedSyncCall, error) {
	adapter, ok := s.AdapterForConnection(connection)
	if !ok {
		return PreparedSyncCall{}, badRequest("backend.integration.sync_call.connector_unsupported", "connector", connectorKey)
	}
	return PreparedSyncCall{Execute: func(ctx context.Context, req SyncCallRequest, requestPayload map[string]any, requestRef string, principal principalmodel.Principal, secrets map[string]string) (SyncAdapterResult, error) {
		callRequest := integrationcontract.CallRequest{
			ConnectorKey: strings.TrimSpace(req.ConnectorKey), Connection: connection, Operation: strings.TrimSpace(req.Operation),
			Method: req.Method, Request: requestPayload, RequestRef: requestRef, Timeout: req.Timeout, Principal: principal, Secrets: secrets,
		}
		var result integrationcontract.CallResult
		var callErr error
		if strings.TrimSpace(req.Operation) == "test_connection" {
			if tester, ok := adapter.(integrationcontract.ConnectionTester); ok {
				result, callErr = tester.TestConnection(ctx, callRequest)
			} else {
				return SyncAdapterResult{}, badRequest("backend.integration.connection.test_unsupported", "connector", connection.ConnectorKey, "provider", connection.ProviderKey)
			}
		} else {
			result, callErr = adapter.Call(ctx, callRequest)
		}
		callErr, providerErrorCode := integrationpolicy.NormalizeProviderError(callErr)
		return SyncAdapterResult{Response: result.Response, ResponseRef: result.ResponseRef, SecretUpdates: result.SecretUpdates, ProviderErrorCode: providerErrorCode, ResourceHealth: result.ResourceHealth}, callErr
	}}, nil
}

func (s *IntegrationApplicationService) prepareRegisteredOutboxCall(ctx context.Context, message integrationmodel.IntegrationOutboxMessage, connection integrationmodel.IntegrationConnection, principal principalmodel.Principal, requestPayload map[string]any) (PreparedOutboxCall, error) {
	operationKey := integrationOutboxConnectorOperation(connection, message.Operation)
	operation, err := s.IntegrationOperation(connection, operationKey)
	if err != nil {
		return PreparedOutboxCall{}, err
	}
	if strings.HasPrefix(strings.TrimSpace(message.ID), "durable_intent:") {
		if _, err := s.actionOutboxOperationIdentity(connection, operation, message.RequestFingerprint); err != nil {
			return PreparedOutboxCall{}, err
		}
	}
	if err := s.validateOperationInput(connection.ConnectorKey, operation, requestPayload); err != nil {
		return PreparedOutboxCall{}, err
	}
	requestPayload, err = s.prepareOutboxPayload(ctx, message, requestPayload)
	if err != nil {
		return PreparedOutboxCall{}, err
	}
	if !integrationpolicy.ProviderRetryIsProtected(operation.IdempotencySupported, operation.SideEffect, message.AttemptCount, connection.Config) && !integrationpolicy.ProviderResponseProvesNoEffect(errors.New(message.Error), message.ResponseRef) {
		return PreparedOutboxCall{}, badRequest("backend.integration.provider.retry_strategy_required", "operation", operation.Key)
	}
	adapter, ok := s.AdapterForConnection(connection)
	if !ok {
		return PreparedOutboxCall{}, errors.New("backend.integration.outbox.sender_not_found")
	}
	return PreparedOutboxCall{Operation: operation.Key, Execute: func(ctx context.Context, secrets map[string]string) (OutboxAdapterResult, error) {
		call := integrationcontract.CallRequest{
			ConnectorKey: message.ConnectorKey, Connection: connection, Operation: strings.TrimSpace(operation.Key),
			Request: requestPayload, RequestRef: message.RequestRef, Principal: principal, Delivery: true,
			Headers: map[string]string{"X-Integration-Message-ID": message.ID, "X-Request-ID": requestcontext.RequestID(ctx)},
			Secrets: secrets,
		}
		result, callErr := adapter.Call(ctx, call)
		providerErrorCode := ""
		if callErr != nil {
			callErr, providerErrorCode = integrationpolicy.NormalizeProviderError(callErr)
		}
		// Outbox execution is the worker-side half of an async operation.
		// connector.DeliveryResult deliberately contains delivery metadata
		// (response reference and secret rotations), not the synchronous
		// operation output. Validating the catalog's response schema here would
		// make every source-owned enqueue/start provider with required output
		// fail after the external side effect, forcing a false uncertain-outcome
		// quarantine.
		return OutboxAdapterResult{Response: result.Response, ResponseRef: result.ResponseRef, SecretUpdates: result.SecretUpdates, ProviderErrorCode: providerErrorCode, ResourceHealth: result.ResourceHealth}, callErr
	}}, nil
}

// Webhook subscriptions persist the concrete business event in the outbox
// operation (for example, webhook.deliver.order.created). The built-in webhook
// Connector exposes the transport operation "send", so resolve only that
// explicit subscription namespace to the catalog operation at dispatch time.
// All other unknown operations remain fail-closed.
func integrationOutboxConnectorOperation(connection integrationmodel.IntegrationConnection, messageOperation string) string {
	messageOperation = strings.TrimSpace(messageOperation)
	const webhookDeliveryPrefix = "webhook.deliver."
	if strings.TrimSpace(connection.ConnectorKey) == "webhook" &&
		strings.HasPrefix(messageOperation, webhookDeliveryPrefix) &&
		strings.TrimSpace(strings.TrimPrefix(messageOperation, webhookDeliveryPrefix)) != "" {
		return "send"
	}
	return messageOperation
}
