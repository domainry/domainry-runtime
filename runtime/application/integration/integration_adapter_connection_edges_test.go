package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type connectionResolutionRepo struct {
	integrationrepository.IntegrationConfigRepository
	connections []integrationmodel.IntegrationConnection
	listErr     error
	upsertErr   error
}

func (r *connectionResolutionRepo) ListConnections(context.Context, string) ([]integrationmodel.IntegrationConnection, error) {
	return append([]integrationmodel.IntegrationConnection(nil), r.connections...), r.listErr
}

func (r *connectionResolutionRepo) UpsertConnection(_ context.Context, _ string, value integrationmodel.IntegrationConnection) (integrationmodel.IntegrationConnection, error) {
	return value, r.upsertErr
}

type adapterEdge struct {
	configErr error
	callErr   error
	result    integrationcontract.CallResult
	called    string
	operation string
}

func (a *adapterEdge) Call(_ context.Context, request integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	a.called = "call"
	a.operation = request.Operation
	return a.result, a.callErr
}
func (a *adapterEdge) ValidateConfig(integrationmodel.IntegrationConnection) error {
	return a.configErr
}
func (a *adapterEdge) TestConnection(context.Context, integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	a.called = "test"
	return a.result, a.callErr
}
func (a *adapterEdge) Invoke(context.Context, integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	a.called = "invoke"
	return a.result, a.callErr
}

type callOnlyAdapter struct {
	result  integrationcontract.CallResult
	callErr error
	called  string
}

func (a *callOnlyAdapter) Call(ctx context.Context, request integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	a.called = "call"
	return a.result, a.callErr
}

func integrationAdapterSchema(providerCount int) integrationmodel.IntegrationSchema {
	providers := make([]integrationmodel.ConnectorProviderSchema, 0, providerCount)
	for index := 0; index < providerCount; index++ {
		key := "provider"
		if index > 0 {
			key = "other"
		}
		providers = append(providers, integrationmodel.ConnectorProviderSchema{Key: key, SecretFields: []definitionmodel.FieldSchema{{Key: "token", Type: "text"}}})
	}
	return integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{
		Key: "connector", Providers: providers,
		Operations: []integrationmodel.ConnectorOperationSchema{{Key: "send", SideEffect: "write", IdempotencySupported: true}, {Key: "unsafe", SideEffect: "write"}},
	}}}
}

func TestIntegrationAdapterResolutionAndValidationEdges(t *testing.T) {
	connection := integrationmodel.IntegrationConnection{ConnectorKey: "connector", ProviderKey: "provider", Status: "active"}
	service := NewIntegrationApplicationService(ApplicationDependencies{})
	if adapter, ok := service.AdapterForConnection(connection); ok || adapter != nil {
		t.Fatal("nil registry resolved connection adapter")
	}
	registry := NewConnectorRegistry(integrationAdapterSchema(2))
	service = NewIntegrationApplicationService(ApplicationDependencies{Registry: registry})
	legacy := &callOnlyAdapter{}
	if _, ok := service.AdapterForConnection(connection); ok {
		t.Fatal("unregistered provider resolved an adapter")
	}
	provider := &adapterEdge{}
	registerTestRegistryProvider(registry, "connector", "provider", provider)
	if adapter, ok := service.AdapterForConnection(connection); !ok || adapter == nil {
		t.Fatalf("provider adapter=%#v ok=%v", adapter, ok)
	}

	singleRegistry := NewConnectorRegistry(integrationAdapterSchema(1))
	registerTestRegistryProvider(singleRegistry, "connector", "provider", legacy)
	single := NewIntegrationApplicationService(ApplicationDependencies{Registry: singleRegistry})
	if adapter, ok := single.AdapterForConnection(connection); !ok || adapter == nil {
		t.Fatalf("exact provider adapter=%#v ok=%v", adapter, ok)
	}
	if adapter, ok := single.AdapterForConnection(integrationmodel.IntegrationConnection{ConnectorKey: "connector", ProviderKey: "missing"}); ok || adapter != nil {
		t.Fatalf("wrong provider crossed exact-pair boundary: adapter=%#v ok=%v", adapter, ok)
	}

	connection.Status = "disabled"
	provider.configErr = errIntegrationManagementTest
	if err := service.ValidateAdapterConfig(connection); err != nil {
		t.Fatalf("disabled config validated=%v", err)
	}
	connection.Status = "active"
	if err := NewIntegrationApplicationService(ApplicationDependencies{}).ValidateAdapterConfig(connection); err != nil {
		t.Fatalf("missing adapter config=%v", err)
	}
	if err := single.ValidateAdapterConfig(connection); err != nil {
		t.Fatalf("non-validator config=%v", err)
	}
	provider.configErr = &apperror.AppError{Kind: apperror.KindBadRequest, Code: "provider.bad"}
	if err := service.ValidateAdapterConfig(connection); apperror.CodeOf(err) != "provider.bad" {
		t.Fatalf("app config error=%v", err)
	}
	provider.configErr = errors.New("provider.invalid: details")
	if err := service.ValidateAdapterConfig(connection); apperror.CodeOf(err) != "provider.invalid" {
		t.Fatalf("plain config error=%v", err)
	}
	provider.configErr = errors.New("")
	if err := service.ValidateAdapterConfig(connection); apperror.CodeOf(err) != "backend.integration.connection.config_invalid" {
		t.Fatalf("empty config error=%v", err)
	}
	provider.configErr = nil
	if err := service.ValidateAdapterConfig(connection); err != nil {
		t.Fatalf("valid config=%v", err)
	}
}

func TestIntegrationConnectionResolutionEdges(t *testing.T) {
	repository := &connectionResolutionRepo{connections: []integrationmodel.IntegrationConnection{{Key: "connection", WorkspaceID: "workspace", ConnectorKey: "connector", Status: "active", Config: map[string]any{"b": true, "a": true}, SecretRefs: map[string]string{"z": "secret", "a": "secret"}}}}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, ConnectionNormalizer: func(_ context.Context, value integrationmodel.IntegrationConnection) (integrationmodel.IntegrationConnection, error) {
		value.Name = "normalized"
		return value, nil
	}})
	principal := integrationManagementPrincipal(PermissionConnectionManage)
	invalidWorkspace := principal
	invalidWorkspace.WorkspaceID = ""
	if err := service.RecordCredentialRefreshFailure(t.Context(), repository.connections[0], invalidWorkspace, errors.New("refresh")); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("credential failure workspace=%v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := service.RecordCredentialRefreshFailure(cancelled, repository.connections[0], principal, errors.New("refresh")); !errors.Is(err, context.Canceled) {
		t.Fatalf("credential cancellation=%v", err)
	}
	repository.upsertErr = errIntegrationManagementTest
	if err := service.RecordCredentialRefreshFailure(t.Context(), repository.connections[0], principal, errors.New("refresh")); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("credential upsert failure=%v", err)
	}
	repository.upsertErr = nil
	if err := service.RecordCredentialRefreshFailure(t.Context(), repository.connections[0], principal, nil); err != nil {
		t.Fatalf("credential failure record=%v", err)
	}
	if _, err := service.DisableIntegrationConnection(t.Context(), "connection", invalidWorkspace); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("disable workspace=%v", err)
	}
	if _, err := service.DisableIntegrationConnection(cancelled, "connection", principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("disable cancellation=%v", err)
	}
	if _, err := service.DisableIntegrationConnection(t.Context(), "connection", integrationManagementPrincipal()); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("disable permission=%v", err)
	}
	repository.listErr = errIntegrationManagementTest
	if _, err := service.DisableIntegrationConnection(t.Context(), "connection", principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("disable list failure=%v", err)
	}
	repository.listErr = nil
	if _, err := service.DisableIntegrationConnection(t.Context(), "missing", principal); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("disable missing=%v", err)
	}
	repository.upsertErr = errIntegrationManagementTest
	if _, err := service.DisableIntegrationConnection(t.Context(), "connection", principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("disable upsert failure=%v", err)
	}
	repository.upsertErr = nil
	if disabled, err := service.DisableIntegrationConnection(t.Context(), "connection", principal); err != nil || disabled.Status != "disabled" {
		t.Fatalf("disabled connection=%#v err=%v", disabled, err)
	}
	repository.connections[0].Status = "active"
	message := integrationmodel.IntegrationOutboxMessage{WorkspaceID: "workspace", ConnectionKey: "connection", ConnectorKey: "connector"}
	if _, err := service.IntegrationConnectionForOutboxMessage(t.Context(), message, principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, ""); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("principal workspace error=%v", err)
	}
	missing := message
	missing.ConnectionKey = ""
	if _, err := service.IntegrationConnectionForOutboxMessage(t.Context(), missing, principal, ""); apperror.CodeOf(err) != "backend.integration.outbox.connection_required" {
		t.Fatalf("missing outbox connection=%v", err)
	}
	missing = message
	missing.WorkspaceID = ""
	if _, err := service.IntegrationConnectionForOutboxMessage(t.Context(), missing, principal, ""); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("message workspace error=%v", err)
	}
	otherWorkspace := message
	otherWorkspace.WorkspaceID = "other"
	if _, err := service.IntegrationConnectionForOutboxMessage(t.Context(), otherWorkspace, principal, ""); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("workspace mismatch=%v", err)
	}
	repository.listErr = errIntegrationManagementTest
	if _, err := service.IntegrationConnectionForOutboxMessage(t.Context(), message, principal, ""); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("outbox list error=%v", err)
	}
	repository.listErr = nil
	message.ConnectionKey = "missing"
	if _, err := service.IntegrationConnectionForOutboxMessage(t.Context(), message, principal, ""); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("outbox not found=%v", err)
	}
	message.ConnectionKey = "connection"
	if _, err := service.IntegrationConnectionForOutboxMessage(t.Context(), message, principal, "other"); apperror.CodeOf(err) != "backend.integration.outbox.connection_connector_mismatch" {
		t.Fatalf("outbox connector mismatch=%v", err)
	}
	repository.connections[0].Status = "disabled"
	if _, err := service.IntegrationConnectionForOutboxMessage(t.Context(), message, principal, ""); apperror.CodeOf(err) != "backend.integration.outbox.connection_unavailable" {
		t.Fatalf("outbox unavailable=%v", err)
	}
	repository.connections[0].Status = "verified"
	if connection, err := service.IntegrationConnectionForOutboxMessage(t.Context(), message, principal, ""); err != nil || connection.Name != "normalized" {
		t.Fatalf("outbox connection=%#v err=%v", connection, err)
	}
	withoutExpectedConnector := message
	withoutExpectedConnector.ConnectorKey = ""
	if _, err := service.IntegrationConnectionForOutboxMessage(t.Context(), withoutExpectedConnector, principal, ""); err != nil {
		t.Fatalf("outbox without expected connector error=%v", err)
	}

	if _, err := service.IntegrationConnectionForWebhook(t.Context(), " ", principal, ""); apperror.CodeOf(err) != "backend.integration.webhook.connection_required" {
		t.Fatalf("webhook required=%v", err)
	}
	if _, err := service.IntegrationConnectionForWebhook(t.Context(), "connection", invalidWorkspace, ""); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("webhook principal workspace=%v", err)
	}
	repository.listErr = errIntegrationManagementTest
	if _, err := service.IntegrationConnectionForWebhook(t.Context(), "connection", principal, ""); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("webhook list error=%v", err)
	}
	repository.listErr = nil
	repository.connections[0].Status = "active"
	if _, err := service.IntegrationConnectionForWebhook(t.Context(), "missing", principal, ""); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("webhook not found=%v", err)
	}
	if _, err := service.IntegrationConnectionForWebhook(t.Context(), "connection", principal, "other"); apperror.CodeOf(err) != "backend.integration.webhook.connection_connector_mismatch" {
		t.Fatalf("webhook connector mismatch=%v", err)
	}
	repository.connections[0].Status = "disabled"
	if _, err := service.IntegrationConnectionForWebhook(t.Context(), "connection", principal, ""); apperror.CodeOf(err) != "backend.integration.connection.disabled" {
		t.Fatalf("disabled webhook=%v", err)
	}
	repository.connections[0].Status = "active"
	if connection, err := service.IntegrationConnectionForWebhook(t.Context(), " connection ", principal, ""); err != nil || connection.Key != "connection" {
		t.Fatalf("webhook connection=%#v err=%v", connection, err)
	}
	if _, err := service.IntegrationConnectionForWebhook(t.Context(), "connection", principal, "connector"); err != nil {
		t.Fatalf("matching webhook connector error=%v", err)
	}
	normalizeFailure := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, ConnectionNormalizer: func(context.Context, integrationmodel.IntegrationConnection) (integrationmodel.IntegrationConnection, error) {
		return integrationmodel.IntegrationConnection{}, errIntegrationManagementTest
	}})
	if _, _, err := normalizeFailure.findConnection(t.Context(), "connection", "workspace"); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("normalizer error=%v", err)
	}
	if !connectionCanSend(repository.connections[0]) || connectionCanSend(integrationmodel.IntegrationConnection{Status: "disabled"}) || !connectionCanSend(integrationmodel.IntegrationConnection{Status: "verified"}) {
		t.Fatal("connection send status matrix mismatch")
	}
	shape := connectionAuditShape(repository.connections[0])
	if len(shape["config_keys"].([]string)) != 2 || len(sortedStringKeys(repository.connections[0].SecretRefs)) != 2 {
		t.Fatalf("connection audit shape=%#v", shape)
	}
}

func TestPreparedRegisteredCallsExerciseAdapterModes(t *testing.T) {
	registry := NewConnectorRegistry(integrationAdapterSchema(1))
	adapter := &adapterEdge{result: integrationcontract.CallResult{Response: map[string]any{"ok": true}, ResponseRef: "response", SecretUpdates: map[string]string{"token": "next"}}}
	registerTestRegistryProvider(registry, "connector", "provider", adapter)
	service := NewIntegrationApplicationService(ApplicationDependencies{Registry: registry})
	connection := integrationmodel.IntegrationConnection{ConnectorKey: "connector", ProviderKey: "provider", Status: "active", Config: map[string]any{}}
	prepared, err := service.prepareRegisteredSyncCall(connection, "connector")
	if err != nil {
		t.Fatal(err)
	}
	for operation, want := range map[string]string{"test_connection": "test", "send": "call"} {
		adapter.called = ""
		result, err := prepared.Execute(t.Context(), SyncCallRequest{ConnectorKey: "connector", Operation: operation, Timeout: time.Second}, map[string]any{}, "request", integrationManagementPrincipal(), map[string]string{})
		wantRef := "response"
		if operation == "test_connection" {
			wantRef = ""
		}
		if err != nil || adapter.called != want || result.ResponseRef != wantRef {
			t.Fatalf("sync %s called=%q result=%#v err=%v", operation, adapter.called, result, err)
		}
	}
	if _, err := NewIntegrationApplicationService(ApplicationDependencies{}).prepareRegisteredSyncCall(connection, "connector"); apperror.CodeOf(err) != "backend.integration.sync_call.connector_unsupported" {
		t.Fatalf("unsupported sync adapter=%v", err)
	}
	callRegistry := NewConnectorRegistry(integrationAdapterSchema(1))
	callAdapter := &callOnlyAdapter{result: integrationcontract.CallResult{ResponseRef: "call"}}
	registerTestRegistryProvider(callRegistry, "connector", "provider", callAdapter)
	callService := NewIntegrationApplicationService(ApplicationDependencies{Registry: callRegistry})
	callPrepared, err := callService.prepareRegisteredSyncCall(connection, "connector")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := callPrepared.Execute(t.Context(), SyncCallRequest{ConnectorKey: "connector", Operation: "test_connection"}, nil, "", principalmodel.Principal{}, nil); apperror.CodeOf(err) != "backend.integration.connection.test_unsupported" || callAdapter.called != "" {
		t.Fatalf("call-only test_connection called=%q err=%v", callAdapter.called, err)
	}
	if _, err := callPrepared.Execute(t.Context(), SyncCallRequest{ConnectorKey: "connector", Operation: "send"}, nil, "", principalmodel.Principal{}, nil); err != nil || callAdapter.called != "call" {
		t.Fatalf("call adapter send called=%q err=%v", callAdapter.called, err)
	}

	message := integrationmodel.IntegrationOutboxMessage{ID: "message", ConnectorKey: "connector", ConnectionKey: "connection", Operation: "send", AttemptCount: 1}
	preparedOutbox, err := service.prepareRegisteredOutboxCall(t.Context(), message, connection, integrationManagementPrincipal(), map[string]any{"value": true})
	if err != nil || preparedOutbox.Operation != "send" {
		t.Fatalf("prepared outbox=%#v err=%v", preparedOutbox, err)
	}
	adapter.called = ""
	result, err := preparedOutbox.Execute(t.Context(), map[string]string{})
	if err != nil || adapter.called != "call" || result.ResponseRef != "response" {
		t.Fatalf("outbox result=%#v called=%q err=%v", result, adapter.called, err)
	}
	message.Operation = "missing"
	if _, err := service.prepareRegisteredOutboxCall(t.Context(), message, connection, principalmodel.Principal{}, nil); err == nil {
		t.Fatal("missing operation accepted")
	}
	message.Operation = "unsafe"
	if _, err := service.prepareRegisteredOutboxCall(t.Context(), message, connection, principalmodel.Principal{}, nil); apperror.CodeOf(err) != "backend.integration.provider.retry_strategy_required" {
		t.Fatalf("unsafe retry error=%v", err)
	}
	message.Operation = "send"
	inputFailure := NewIntegrationApplicationService(ApplicationDependencies{Registry: registry, OperationInputValidator: func(string, integrationmodel.ConnectorOperationSchema, map[string]any) error {
		return errIntegrationManagementTest
	}})
	if _, err := inputFailure.prepareRegisteredOutboxCall(t.Context(), message, connection, principalmodel.Principal{}, nil); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("input validation error=%v", err)
	}
	noAdapter := NewIntegrationApplicationService(ApplicationDependencies{Registry: NewConnectorRegistry(integrationAdapterSchema(1))})
	if _, err := noAdapter.prepareRegisteredOutboxCall(t.Context(), message, connection, principalmodel.Principal{}, nil); err == nil {
		t.Fatal("missing outbox adapter accepted")
	}
	callPreparedOutbox, err := callService.prepareRegisteredOutboxCall(t.Context(), message, connection, principalmodel.Principal{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	callAdapter.called = ""
	if _, err := callPreparedOutbox.Execute(t.Context(), nil); err != nil || callAdapter.called != "call" {
		t.Fatalf("call outbox called=%q err=%v", callAdapter.called, err)
	}
	adapter.callErr = errIntegrationManagementTest
	preparedOutbox, err = service.prepareRegisteredOutboxCall(t.Context(), message, connection, principalmodel.Principal{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result, err = preparedOutbox.Execute(t.Context(), nil); err == nil {
		t.Fatalf("provider failure result=%#v err=%v", result, err)
	}
	adapter.callErr = nil
	outputValidated := false
	outputFailure := NewIntegrationApplicationService(ApplicationDependencies{Registry: registry, OperationOutputValidator: func(string, integrationmodel.ConnectorOperationSchema, map[string]any) error {
		outputValidated = true
		return errIntegrationManagementTest
	}})
	preparedOutbox, err = outputFailure.prepareRegisteredOutboxCall(t.Context(), message, connection, principalmodel.Principal{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := preparedOutbox.Execute(t.Context(), nil); err != nil || outputValidated {
		t.Fatalf("async delivery output validation called=%t error=%v", outputValidated, err)
	}
}

func TestPreparedWebhookSubscriptionOutboxResolvesTransportOperationFailClosed(t *testing.T) {
	schema := integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{
		Key:       "webhook",
		Providers: []integrationmodel.ConnectorProviderSchema{{Key: "http"}},
		Operations: []integrationmodel.ConnectorOperationSchema{{
			Key: "send", SideEffect: "write", IdempotencySupported: true,
		}},
	}}}
	registry := NewConnectorRegistry(schema)
	adapter := &adapterEdge{}
	registerTestRegistryProvider(registry, "webhook", "http", adapter)
	service := NewIntegrationApplicationService(ApplicationDependencies{Registry: registry})
	connection := integrationmodel.IntegrationConnection{
		ConnectorKey: "webhook", ProviderKey: "http", Status: "active",
	}

	message := integrationmodel.IntegrationOutboxMessage{
		ID: "subscription-message", ConnectorKey: "webhook", ConnectionKey: "primary",
		Operation: "webhook.deliver.order.created",
	}
	prepared, err := service.prepareRegisteredOutboxCall(t.Context(), message, connection, integrationManagementPrincipal(), map[string]any{"event_type": "order.created"})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Operation != "send" {
		t.Fatalf("prepared operation=%q", prepared.Operation)
	}
	if _, err := prepared.Execute(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if adapter.operation != "send" {
		t.Fatalf("adapter operation=%q", adapter.operation)
	}
	if message.Operation != "webhook.deliver.order.created" {
		t.Fatalf("persisted business operation mutated to %q", message.Operation)
	}

	for _, operation := range []string{"webhook.deliver.", "webhook.deliver.   ", "webhook.unknown.order.created"} {
		message.Operation = operation
		if _, err := service.prepareRegisteredOutboxCall(t.Context(), message, connection, integrationManagementPrincipal(), nil); apperror.CodeOf(err) != "backend.automation.connector_operation_not_found" {
			t.Fatalf("unknown operation %q did not fail closed: %v", operation, err)
		}
	}

	nonWebhook := connection
	nonWebhook.ConnectorKey = "connector"
	message.ConnectorKey = "connector"
	message.Operation = "webhook.deliver.order.created"
	if got := integrationOutboxConnectorOperation(nonWebhook, message.Operation); got != message.Operation {
		t.Fatalf("non-webhook operation normalized to %q", got)
	}
}

func TestSendAdapterOutboxMessageEdges(t *testing.T) {
	principal := integrationManagementPrincipal()
	message := integrationmodel.IntegrationOutboxMessage{ID: "message", WorkspaceID: "workspace", ConnectorKey: "connector", ConnectionKey: "connection", Operation: "send", RequestRef: "request", Payload: map[string]any{"value": true, "request_id": "drop"}}

	service := NewIntegrationApplicationService(ApplicationDependencies{})
	if _, err := service.SendAdapterOutboxMessage(t.Context(), message, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("unknown principal error=%v", err)
	}
	invalidWorkspace := message
	invalidWorkspace.WorkspaceID = ""
	if _, err := service.SendAdapterOutboxMessage(t.Context(), invalidWorkspace, principal); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("message workspace error=%v", err)
	}
	otherWorkspace := message
	otherWorkspace.WorkspaceID = "other"
	if _, err := service.SendAdapterOutboxMessage(t.Context(), otherWorkspace, principal); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("workspace mismatch error=%v", err)
	}
	missingConnection := message
	missingConnection.ConnectionKey = " "
	if _, err := service.SendAdapterOutboxMessage(t.Context(), missingConnection, principal); err == nil || err.Error() != "backend.integration.outbox.connection_required" {
		t.Fatalf("missing connection error=%v", err)
	}

	config := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{}, secrets: map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{}, apiKeys: map[string]integrationmodel.IntegrationAPIKey{}}
	connection := integrationmodel.IntegrationConnection{Key: "connection", WorkspaceID: "workspace", ConnectorKey: "connector", ProviderKey: "provider", Status: "active", Config: map[string]any{}}
	config.connections[connection.Key] = connection
	registry := NewConnectorRegistry(integrationAdapterSchema(1))
	adapter := &adapterEdge{result: integrationcontract.CallResult{Response: map[string]any{"ok": true}, ResponseRef: "response"}}
	registerTestRegistryProvider(registry, "connector", "provider", adapter)
	delivery := &independentDeliveryRepository{}
	service = NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: config, DeliveryRepository: delivery, Registry: registry})

	config.connections[connection.Key] = integrationmodel.IntegrationConnection{Key: "connection", WorkspaceID: "workspace", ConnectorKey: "connector", ProviderKey: "provider", Status: "disabled"}
	if _, err := service.SendAdapterOutboxMessage(t.Context(), message, principal); err == nil || err.Error() != "backend.integration.outbox.connection_unavailable" {
		t.Fatalf("unavailable connection error=%v", err)
	}
	config.connections[connection.Key] = connection
	badOperation := message
	badOperation.Operation = "missing"
	if _, err := service.SendAdapterOutboxMessage(t.Context(), badOperation, principal); err == nil {
		t.Fatal("missing operation accepted")
	}
	result, err := service.SendAdapterOutboxMessage(t.Context(), message, principal)
	if err != nil || result.Status != "sent" || result.ResponseRef != "response" || len(delivery.invocations) != 1 {
		t.Fatalf("send result=%#v invocations=%#v err=%v", result, delivery.invocations, err)
	}
	adapter.callErr = errIntegrationManagementTest
	if result, err := service.SendAdapterOutboxMessage(t.Context(), message, principal); !errors.Is(err, errIntegrationManagementTest) || result.ResponseRef != "response" {
		t.Fatalf("provider failure result=%#v err=%v", result, err)
	}
	adapter.callErr = nil
	delivery.insertInvocationErr = errIntegrationManagementTest
	if result, err := service.SendAdapterOutboxMessage(t.Context(), message, principal); !errors.Is(err, errIntegrationManagementTest) || result.ResponseRef != "response" {
		t.Fatalf("evidence failure result=%#v err=%v", result, err)
	}
}
