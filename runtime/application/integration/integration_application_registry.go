package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/domainry/domainry-connector-sdk"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func (s *IntegrationApplicationService) RegisterBuiltinConnectorDefinitions(definitions []integrationmodel.ConnectorSchema) {
	s.registry.MergeConnectors(definitions)
}

func (s *IntegrationApplicationService) RegisterIntegrationEventHandler(provider string, handler EventHandler) {
	if provider = strings.TrimSpace(provider); provider != "" && handler != nil {
		s.registry.RegisterEventHandler(provider, handler)
	}
}

func (s *IntegrationApplicationService) RegisterIntegrationOutboxSender(connectorKey string, sender OutboxSender) {
	if connectorKey = strings.TrimSpace(connectorKey); connectorKey != "" && sender != nil {
		s.registry.RegisterOutboxSender(connectorKey, sender)
	}
}

func (s *IntegrationApplicationService) IntegrationEventHandler(provider string) (EventHandler, bool) {
	return s.registry.EventHandler(strings.TrimSpace(provider))
}

func (s *IntegrationApplicationService) IntegrationOutboxSender(connectorKey string) (OutboxSender, bool) {
	return s.registry.OutboxSender(strings.TrimSpace(connectorKey))
}

type publicProviderAdapter struct {
	provider   connector.Adapter
	descriptor connector.ProviderDescriptor
}

type publicConnectionTester func(context.Context, integrationcontract.CallRequest) (integrationcontract.CallResult, error)

func (f publicConnectionTester) TestConnection(ctx context.Context, request integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	return f(ctx, request)
}

type publicWebhookVerifier func(context.Context, integrationcontract.InboundWebhookRequest) (integrationcontract.VerifiedInboundWebhook, error)

func (f publicWebhookVerifier) VerifyWebhook(ctx context.Context, request integrationcontract.InboundWebhookRequest) (integrationcontract.VerifiedInboundWebhook, error) {
	return f(ctx, request)
}

type publicReconciler func(context.Context, integrationcontract.ReconcileRequest) (integrationcontract.ReconcileResult, error)

func (f publicReconciler) Reconcile(ctx context.Context, request integrationcontract.ReconcileRequest) (integrationcontract.ReconcileResult, error) {
	return f(ctx, request)
}

func newPublicProviderAdapter(provider connector.Adapter, descriptor connector.ProviderDescriptor) integrationcontract.Adapter {
	base := &publicProviderAdapter{provider: provider, descriptor: descriptor}
	tester, hasTester := provider.(connector.ConnectionTester)
	verifier, hasVerifier := provider.(connector.WebhookVerifier)
	reconciler, hasReconciler := provider.(connector.Reconciler)
	test := publicConnectionTester(func(ctx context.Context, request integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
		return base.testConnection(ctx, tester, request)
	})
	verify := publicWebhookVerifier(func(ctx context.Context, request integrationcontract.InboundWebhookRequest) (integrationcontract.VerifiedInboundWebhook, error) {
		return base.verifyWebhook(ctx, verifier, request)
	})
	reconcile := publicReconciler(func(ctx context.Context, request integrationcontract.ReconcileRequest) (integrationcontract.ReconcileResult, error) {
		return base.reconcile(ctx, reconciler, request)
	})
	switch {
	case hasTester && hasVerifier && hasReconciler:
		return struct {
			*publicProviderAdapter
			publicConnectionTester
			publicWebhookVerifier
			publicReconciler
		}{base, test, verify, reconcile}
	case hasTester && hasVerifier:
		return struct {
			*publicProviderAdapter
			publicConnectionTester
			publicWebhookVerifier
		}{base, test, verify}
	case hasTester && hasReconciler:
		return struct {
			*publicProviderAdapter
			publicConnectionTester
			publicReconciler
		}{base, test, reconcile}
	case hasVerifier && hasReconciler:
		return struct {
			*publicProviderAdapter
			publicWebhookVerifier
			publicReconciler
		}{base, verify, reconcile}
	case hasTester:
		return struct {
			*publicProviderAdapter
			publicConnectionTester
		}{base, test}
	case hasVerifier:
		return struct {
			*publicProviderAdapter
			publicWebhookVerifier
		}{base, verify}
	case hasReconciler:
		return struct {
			*publicProviderAdapter
			publicReconciler
		}{base, reconcile}
	default:
		return base
	}
}

func (a *publicProviderAdapter) Call(ctx context.Context, request integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	if a.provider == nil {
		return integrationcontract.CallResult{}, fmt.Errorf("connector provider adapter is required")
	}
	if err := a.validateInternalIdentity(request.ConnectorKey, request.Connection); err != nil {
		return integrationcontract.CallResult{}, err
	}
	operation, ok := publicProviderOperation(a.descriptor, request.Operation)
	if !ok {
		return integrationcontract.CallResult{}, fmt.Errorf("connector provider %s/%s operation %s is not registered", a.descriptor.ConnectorKey, a.descriptor.ProviderKey, request.Operation)
	}
	payload, err := json.Marshal(request.Request)
	if err != nil {
		return integrationcontract.CallResult{}, fmt.Errorf("encode connector provider %s/%s request: %w", a.descriptor.ConnectorKey, a.descriptor.ProviderKey, err)
	}
	secrets, err := a.scopeSecrets(request.Secrets)
	if err != nil {
		return integrationcontract.CallResult{}, err
	}
	result, callErr := a.provider.Call(ctx, connector.CallRequest{
		ConnectorKey: a.descriptor.ConnectorKey, ProviderKey: a.descriptor.ProviderKey,
		OperationKey: operation.Key, ContractSHA256: operation.ContractSHA256, Mode: operation.Mode,
		Connection: a.scopedConnection(request.Connection), Payload: payload,
		RequestRef: request.RequestRef, Headers: cloneIntegrationStrings(request.Headers),
		Secrets: secrets, Delivery: request.Delivery,
		Timeout: request.Timeout, Principal: toConnectorPrincipal(request.Principal),
	})
	response, err := decodePublicObject(result.Payload)
	if err != nil {
		return integrationcontract.CallResult{}, fmt.Errorf("decode connector provider %s/%s response: %w", a.descriptor.ConnectorKey, a.descriptor.ProviderKey, err)
	}
	secretUpdates, err := a.scopeSecretUpdates(result.SecretUpdates)
	if err != nil {
		return integrationcontract.CallResult{}, err
	}
	return integrationcontract.CallResult{Response: response, ResponseRef: result.ResponseRef, SecretUpdates: secretUpdates, ResourceHealth: publicProviderResourceHealth(result.ResourceHealth)}, toIntegrationProviderError(callErr)
}

func publicProviderResourceHealth(report *connector.ResourceHealthReport) *integrationmodel.IntegrationProviderResourceHealth {
	if report == nil {
		return nil
	}
	return &integrationmodel.IntegrationProviderResourceHealth{
		ObservationID: report.ObservationID, Kind: report.Kind, State: report.State, PreviousState: report.PreviousState,
		EvidenceSource: report.EvidenceSource, QuotaUsedPercent: report.QuotaUsedPercent, BalanceBand: report.BalanceBand,
		CapabilityBlocked: report.CapabilityBlocked, ObservedAt: report.ObservedAt, ErrorCode: report.ErrorCode,
	}
}

func (a *publicProviderAdapter) ProviderSchema() integrationmodel.ConnectorProviderSchema {
	return toIntegrationProviderSchema(a.descriptor)
}

func (a *publicProviderAdapter) OperationIdentity(operationKey string) (integrationcontract.OperationIdentity, bool) {
	operation, ok := publicProviderOperation(a.descriptor, operationKey)
	if !ok {
		return integrationcontract.OperationIdentity{}, false
	}
	return integrationcontract.OperationIdentity{Key: operation.Key, Mode: string(operation.Mode), ContractSHA256: operation.ContractSHA256, Effect: string(operation.Reliability.Effect)}, true
}

func (a *publicProviderAdapter) ValidateConfig(connection integrationmodel.IntegrationConnection) error {
	validator, ok := a.provider.(connector.ConfigValidator)
	if !ok {
		return nil
	}
	if err := a.validateInternalIdentity(connection.ConnectorKey, connection); err != nil {
		return err
	}
	return validator.ValidateConfig(a.scopedConnection(connection))
}

func (a *publicProviderAdapter) testConnection(ctx context.Context, tester connector.ConnectionTester, request integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	if err := a.validateInternalIdentity(request.ConnectorKey, request.Connection); err != nil {
		return integrationcontract.CallResult{}, err
	}
	secrets, err := a.scopeSecrets(request.Secrets)
	if err != nil {
		return integrationcontract.CallResult{}, err
	}
	result, err := tester.TestConnection(ctx, connector.TestConnectionRequest{
		ConnectorKey: a.descriptor.ConnectorKey, ProviderKey: a.descriptor.ProviderKey,
		Connection: a.scopedConnection(request.Connection), Secrets: secrets,
		Timeout: request.Timeout, Principal: toConnectorPrincipal(request.Principal),
	})
	response := map[string]any{"connected": result.Connected}
	if len(result.Details) > 0 && string(result.Details) != "null" {
		var details any
		if err := json.Unmarshal(result.Details, &details); err != nil {
			return integrationcontract.CallResult{}, fmt.Errorf("decode connector provider %s/%s connection test details: %w", a.descriptor.ConnectorKey, a.descriptor.ProviderKey, err)
		}
		response["details"] = details
		// The internal catalog still validates builtin test_connection output
		// fields individually. Preserve the public Details envelope while also
		// projecting object fields until the generated public catalog owns them.
		if object, ok := details.(map[string]any); ok {
			for key, value := range object {
				if _, exists := response[key]; !exists {
					response[key] = value
				}
			}
		}
	}
	secretUpdates, scopeErr := a.scopeSecretUpdates(result.SecretUpdates)
	if scopeErr != nil {
		return integrationcontract.CallResult{}, scopeErr
	}
	return integrationcontract.CallResult{Response: response, SecretUpdates: secretUpdates}, toIntegrationProviderError(err)
}

func (a *publicProviderAdapter) verifyWebhook(ctx context.Context, verifier connector.WebhookVerifier, request integrationcontract.InboundWebhookRequest) (integrationcontract.VerifiedInboundWebhook, error) {
	if err := a.validateInternalIdentity(request.Connection.ConnectorKey, request.Connection); err != nil {
		return integrationcontract.VerifiedInboundWebhook{}, err
	}
	headers, query := request.HeaderValues, request.QueryValues
	if headers == nil {
		headers = toPublicMultiStrings(request.Headers)
	}
	if query == nil {
		query = toPublicMultiStrings(request.Query)
	}
	secrets, err := a.scopeSecrets(request.Secrets)
	if err != nil {
		return integrationcontract.VerifiedInboundWebhook{}, err
	}
	verified, err := verifier.VerifyWebhook(ctx, connector.VerifyWebhookRequest{
		ConnectorKey: a.descriptor.ConnectorKey, ProviderKey: a.descriptor.ProviderKey,
		Connection: a.scopedConnection(request.Connection), Headers: cloneIntegrationMultiStrings(headers),
		Query: cloneIntegrationMultiStrings(query), Secrets: secrets,
		Body: append([]byte(nil), request.Body...), ReceivedAt: request.ReceivedAt,
	})
	if err != nil {
		return integrationcontract.VerifiedInboundWebhook{}, toIntegrationProviderError(err)
	}
	payload, err := decodePublicWebhookObject(verified.Payload)
	if err != nil {
		return integrationcontract.VerifiedInboundWebhook{}, fmt.Errorf("decode connector provider %s/%s webhook payload: %w", a.descriptor.ConnectorKey, a.descriptor.ProviderKey, err)
	}
	return integrationcontract.VerifiedInboundWebhook{
		EventType: verified.EventType, ExternalID: verified.ExternalID, Payload: payload,
		Security: toIntegrationWebhookSecurity(verified.Security), Challenge: verified.Challenge,
		ChallengeFormat: verified.ChallengeFormat, ExternalIdentity: toIntegrationWebhookIdentity(verified.ExternalIdentity),
		DeliveryReceipt: toIntegrationWebhookReceipt(verified.DeliveryReceipt),
	}, nil
}

func (a *publicProviderAdapter) reconcile(ctx context.Context, reconciler connector.Reconciler, request integrationcontract.ReconcileRequest) (integrationcontract.ReconcileResult, error) {
	if err := a.validateInternalIdentity(request.ConnectorKey, request.Connection); err != nil {
		return integrationcontract.ReconcileResult{}, err
	}
	operation, ok := publicProviderOperation(a.descriptor, request.Operation)
	if !ok {
		return integrationcontract.ReconcileResult{}, fmt.Errorf("connector provider %s/%s operation %s is not registered", a.descriptor.ConnectorKey, a.descriptor.ProviderKey, request.Operation)
	}
	if operation.Reliability.Reconciliation != connector.ReconciliationProviderLookup {
		return integrationcontract.ReconcileResult{}, fmt.Errorf("connector provider %s/%s operation %s does not support reconciliation", a.descriptor.ConnectorKey, a.descriptor.ProviderKey, operation.Key)
	}
	if supplied := strings.TrimSpace(request.ContractSHA256); supplied != "" && supplied != operation.ContractSHA256 {
		return integrationcontract.ReconcileResult{}, fmt.Errorf("connector provider %s/%s operation %s contract mismatch: got %s, want %s", a.descriptor.ConnectorKey, a.descriptor.ProviderKey, operation.Key, supplied, operation.ContractSHA256)
	}
	payload, err := json.Marshal(request.Request)
	if err != nil {
		return integrationcontract.ReconcileResult{}, fmt.Errorf("encode connector provider %s/%s reconciliation request: %w", a.descriptor.ConnectorKey, a.descriptor.ProviderKey, err)
	}
	secrets, err := a.scopeSecrets(request.Secrets)
	if err != nil {
		return integrationcontract.ReconcileResult{}, err
	}
	publicRequest := connector.ReconcileRequest{
		ConnectorKey: a.descriptor.ConnectorKey, ProviderKey: a.descriptor.ProviderKey,
		OperationKey: operation.Key, ContractSHA256: operation.ContractSHA256,
		Connection: a.scopedConnection(request.Connection), RequestRef: request.RequestRef,
		ResponseRef: request.ResponseRef, Payload: payload, Secrets: secrets,
		Timeout: request.Timeout, Principal: toConnectorPrincipal(request.Principal),
	}
	if err := publicRequest.Validate(); err != nil {
		return integrationcontract.ReconcileResult{}, err
	}
	result, err := reconciler.Reconcile(ctx, publicRequest)
	if err != nil {
		return integrationcontract.ReconcileResult{}, toIntegrationProviderError(err)
	}
	if err := result.Validate(); err != nil {
		return integrationcontract.ReconcileResult{}, err
	}
	response, responseRef, secretUpdates := map[string]any{}, "", map[string]string(nil)
	if result.Result != nil {
		response, err = decodePublicObject(result.Result.Payload)
		if err != nil {
			return integrationcontract.ReconcileResult{}, fmt.Errorf("decode connector provider %s/%s reconciliation result: %w", a.descriptor.ConnectorKey, a.descriptor.ProviderKey, err)
		}
		responseRef = result.Result.ResponseRef
		secretUpdates, err = a.scopeSecretUpdates(result.Result.SecretUpdates)
		if err != nil {
			return integrationcontract.ReconcileResult{}, err
		}
	}
	return integrationcontract.ReconcileResult{
		Outcome: integrationcontract.ReconciliationOutcome(result.Outcome), Response: response,
		ResponseRef: responseRef, SecretUpdates: secretUpdates, FailureCode: result.FailureCode, RetryAfter: result.RetryAfter,
	}, nil
}

func (a *publicProviderAdapter) validateInternalIdentity(connectorKey string, connection integrationmodel.IntegrationConnection) error {
	if strings.TrimSpace(connectorKey) != a.descriptor.ConnectorKey || strings.TrimSpace(connection.ConnectorKey) != a.descriptor.ConnectorKey || strings.TrimSpace(connection.ProviderKey) != a.descriptor.ProviderKey {
		return fmt.Errorf("connector provider mismatch: got %s/%s, want %s/%s", connection.ConnectorKey, connection.ProviderKey, a.descriptor.ConnectorKey, a.descriptor.ProviderKey)
	}
	return nil
}

func (a *publicProviderAdapter) scopedConnection(connection integrationmodel.IntegrationConnection) connector.Connection {
	public := toConnectorConnection(connection)
	if len(public.SecretRefs) == 0 {
		return public
	}
	allowed := make(map[string]bool, len(a.descriptor.SecretFields))
	for _, field := range a.descriptor.SecretFields {
		allowed[field.Key] = true
	}
	refs := make(map[string]string, len(allowed))
	for key, reference := range public.SecretRefs {
		if allowed[key] {
			refs[key] = reference
		}
	}
	public.SecretRefs = refs
	return public
}

func (a *publicProviderAdapter) scopeSecrets(input map[string]string) (map[string]string, error) {
	fields := make(map[string]connector.SecretField, len(a.descriptor.SecretFields))
	for _, field := range a.descriptor.SecretFields {
		fields[field.Key] = field
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		canonical := strings.TrimSpace(key)
		field, ok := fields[canonical]
		if !ok {
			return nil, fmt.Errorf("connector provider %s/%s received undeclared secret %s", a.descriptor.ConnectorKey, a.descriptor.ProviderKey, key)
		}
		if canonical != key {
			return nil, fmt.Errorf("connector provider %s/%s received non-canonical secret key %q", a.descriptor.ConnectorKey, a.descriptor.ProviderKey, key)
		}
		result[field.Key] = value
	}
	return result, nil
}

func (a *publicProviderAdapter) scopeSecretUpdates(input map[string]string) (map[string]string, error) {
	if len(input) == 0 {
		return nil, nil
	}
	allowed := make(map[string]bool, len(a.descriptor.SecretFields))
	for _, field := range a.descriptor.SecretFields {
		allowed[field.Key] = true
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		canonical := strings.TrimSpace(key)
		if !allowed[canonical] {
			return nil, fmt.Errorf("connector provider %s/%s returned undeclared secret update %s", a.descriptor.ConnectorKey, a.descriptor.ProviderKey, key)
		}
		if canonical != key {
			return nil, fmt.Errorf("connector provider %s/%s returned non-canonical secret update key %q", a.descriptor.ConnectorKey, a.descriptor.ProviderKey, key)
		}
		result[canonical] = value
	}
	return result, nil
}

func publicProviderOperation(provider connector.ProviderDescriptor, operationKey string) (connector.OperationDescriptor, bool) {
	operationKey = strings.TrimSpace(operationKey)
	for _, operation := range provider.Operations {
		if operation.Key == operationKey {
			return operation, true
		}
	}
	return connector.OperationDescriptor{}, false
}
