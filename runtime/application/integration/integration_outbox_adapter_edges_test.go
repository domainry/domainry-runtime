package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/telemetry"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/resilience"
)

type integrationOutboxAdapter struct {
	result  integrationcontract.CallResult
	err     error
	request integrationcontract.CallRequest
}

func (a *integrationOutboxAdapter) Call(_ context.Context, request integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	a.request = request
	return a.result, a.err
}

func integrationOutboxAdapterService(connection integrationmodel.IntegrationConnection, adapter integrationcontract.Adapter, delivery *independentDeliveryRepository) *IntegrationApplicationService {
	config := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{connection.Key: connection}, secrets: map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{}}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{
		Key: connection.ConnectorKey, Provider: connection.ProviderKey, Providers: []integrationmodel.ConnectorProviderSchema{{Key: connection.ProviderKey}},
		Operations: []integrationmodel.ConnectorOperationSchema{{
			Key: "send", Method: "POST", ExecutionMode: "async", SideEffect: "write", IdempotencySupported: true,
			Output: []definitionmodel.FieldSchema{{Key: "status_code", Type: "integer", Required: true}},
		}},
	}}})
	if adapter != nil && connection.ConnectorKey != "" && connection.ProviderKey != "" {
		registerTestRegistryProvider(registry, connection.ConnectorKey, connection.ProviderKey, adapter)
	}
	return NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: config, DeliveryRepository: delivery, Registry: registry})
}

func TestAsyncOutboxDeliveryDoesNotRequireSynchronousOperationOutput(t *testing.T) {
	connection := integrationmodel.IntegrationConnection{
		Key: "primary", WorkspaceID: "workspace", ConnectorKey: "webhook", ProviderKey: "http", Status: "active",
	}
	message := integrationmodel.IntegrationOutboxMessage{
		ID: "message", WorkspaceID: "workspace", ConnectorKey: "webhook", ConnectionKey: "primary",
		Operation: "send", RequestRef: "request-1",
	}
	adapter := &integrationOutboxAdapter{result: integrationcontract.CallResult{ResponseRef: "provider:request-1"}}
	service := integrationOutboxAdapterService(connection, adapter, &independentDeliveryRepository{})
	result, err := service.SendAdapterOutboxMessage(t.Context(), message, accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "worker"}}, accessfixture.Bundle{Key: "integration_worker"}))
	if err != nil || result.Status != "sent" || result.ResponseRef != "provider:request-1" {
		t.Fatalf("result=%#v error=%v", result, err)
	}
}

func TestSendAdapterOutboxMessageGuardsConnectionAndPreparation(t *testing.T) {
	connection := integrationmodel.IntegrationConnection{Key: "primary", WorkspaceID: "workspace", ConnectorKey: "email", ProviderKey: "provider", Status: "active"}
	message := integrationmodel.IntegrationOutboxMessage{ID: "message", WorkspaceID: "workspace", ConnectorKey: "email", ConnectionKey: "primary", Operation: "send"}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "worker"}}
	service := integrationOutboxAdapterService(connection, &integrationOutboxAdapter{}, &independentDeliveryRepository{})
	if _, err := service.SendAdapterOutboxMessage(t.Context(), message, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("principal error = %v", err)
	}
	invalidMessage := message
	invalidMessage.WorkspaceID = ""
	if _, err := service.SendAdapterOutboxMessage(t.Context(), invalidMessage, principal); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("message workspace error = %v", err)
	}
	invalidMessage.WorkspaceID = "other"
	if _, err := service.SendAdapterOutboxMessage(t.Context(), invalidMessage, principal); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("workspace mismatch error = %v", err)
	}
	invalidMessage = message
	invalidMessage.ConnectionKey = " "
	if _, err := service.SendAdapterOutboxMessage(t.Context(), invalidMessage, principal); err == nil || err.Error() != "backend.integration.outbox.connection_required" {
		t.Fatalf("connection key error = %v", err)
	}
	service.configRepo = integrationInboundConfigFaultRepository{independentConfigRepository: &independentConfigRepository{}, err: errIntegrationManagementTest}
	if _, err := service.SendAdapterOutboxMessage(t.Context(), message, principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("connection repository error = %v", err)
	}
	for name, changed := range map[string]integrationmodel.IntegrationConnection{
		"missing":            {},
		"disabled":           {Key: "primary", WorkspaceID: "workspace", ConnectorKey: "email", ProviderKey: "provider", Status: "disabled"},
		"connector mismatch": {Key: "primary", WorkspaceID: "workspace", ConnectorKey: "other", ProviderKey: "provider", Status: "active"},
	} {
		t.Run(name, func(t *testing.T) {
			service := integrationOutboxAdapterService(changed, &integrationOutboxAdapter{}, &independentDeliveryRepository{})
			_, err := service.SendAdapterOutboxMessage(t.Context(), message, principal)
			if err == nil || err.Error() != "backend.integration.outbox.connection_unavailable" {
				t.Fatalf("error = %v", err)
			}
		})
	}
	service = integrationOutboxAdapterService(connection, nil, &independentDeliveryRepository{})
	if _, err := service.SendAdapterOutboxMessage(t.Context(), message, principal); err == nil || err.Error() != "backend.integration.outbox.sender_not_found" {
		t.Fatalf("adapter error = %v", err)
	}
	connection.SecretRefs = map[string]string{"token": "plaintext"}
	service = integrationOutboxAdapterService(connection, &integrationOutboxAdapter{}, &independentDeliveryRepository{})
	if _, err := service.SendAdapterOutboxMessage(t.Context(), message, principal); apperror.CodeOf(err) != "backend.integration.secret_ref.must_be_reference" {
		t.Fatalf("secret error = %v", err)
	}
}

func TestSendAdapterOutboxMessageRecordsSanitizedSuccessAndFailureEvidence(t *testing.T) {
	connection := integrationmodel.IntegrationConnection{Key: "primary", WorkspaceID: "workspace", ConnectorKey: "email", ProviderKey: "provider", Status: "verified"}
	message := integrationmodel.IntegrationOutboxMessage{ID: "message", WorkspaceID: "workspace", ConnectorKey: "email", ConnectionKey: "primary", Operation: "send", RequestRef: "request", EventID: "event", Payload: map[string]any{"value": "ok", "request_id": "remove", telemetry.AsyncPayloadKey: map[string]any{"remove": true}}}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "worker"}}, accessfixture.Bundle{Key: "integration_worker"})
	delivery := &independentDeliveryRepository{}
	adapter := &integrationOutboxAdapter{result: integrationcontract.CallResult{Response: map[string]any{"accepted": true}, ResponseRef: "provider:1"}}
	service := integrationOutboxAdapterService(connection, adapter, delivery)
	service.operationalMetrics = nil
	result, err := service.SendAdapterOutboxMessage(t.Context(), message, principal)
	if err != nil || result.Status != "sent" || result.ResponseRef != "provider:1" || len(delivery.invocations) != 1 || delivery.invocations[0].Status != "succeeded" {
		t.Fatalf("result=%#v invocations=%#v err=%v", result, delivery.invocations, err)
	}
	if adapter.request.Request["value"] != "ok" || adapter.request.Request["request_id"] != nil || adapter.request.Request[telemetry.AsyncPayloadKey] != nil || adapter.request.Headers["X-Integration-Message-ID"] != "message" {
		t.Fatalf("adapter request = %#v", adapter.request)
	}
	connection.Status = "degraded"
	degradedDelivery := &independentDeliveryRepository{}
	degradedService := integrationOutboxAdapterService(connection, adapter, degradedDelivery)
	if result, err := degradedService.SendAdapterOutboxMessage(t.Context(), message, principal); err != nil || result.Status != "sent" {
		t.Fatalf("degraded recovery result=%#v err=%v", result, err)
	}
	connection.Status = "verified"
	wantErr := errors.New("provider secret failed")
	delivery = &independentDeliveryRepository{}
	adapter = &integrationOutboxAdapter{result: integrationcontract.CallResult{ResponseRef: "provider:failed"}, err: wantErr}
	service = integrationOutboxAdapterService(connection, adapter, delivery)
	result, err = service.SendAdapterOutboxMessage(t.Context(), message, principal)
	if !errors.Is(err, wantErr) || result.ResponseRef != "provider:failed" || len(delivery.invocations) != 1 || delivery.invocations[0].Status != "failed" {
		t.Fatalf("result=%#v invocations=%#v err=%v", result, delivery.invocations, err)
	}
	delivery = &independentDeliveryRepository{insertInvocationErr: errIntegrationManagementTest}
	adapter = &integrationOutboxAdapter{result: integrationcontract.CallResult{ResponseRef: "provider:evidence"}}
	service = integrationOutboxAdapterService(connection, adapter, delivery)
	result, err = service.SendAdapterOutboxMessage(t.Context(), message, principal)
	if !errors.Is(err, errIntegrationManagementTest) || result.ResponseRef != "provider:evidence" {
		t.Fatalf("evidence result=%#v err=%v", result, err)
	}
}

func TestSendAdapterOutboxMessageOwnsHTTPWebhookPolicy(t *testing.T) {
	connection := integrationmodel.IntegrationConnection{
		Key: "primary", WorkspaceID: "workspace", ConnectorKey: "webhook", ProviderKey: "http", Status: "verified",
		Config: map[string]any{"min_interval_seconds": 2, "circuit_failure_threshold": 7, "circuit_cooldown_seconds": 9},
	}
	message := integrationmodel.IntegrationOutboxMessage{
		ID: "message", WorkspaceID: "workspace", ConnectorKey: "webhook", ConnectionKey: "primary", Operation: "send",
	}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "worker"}}, accessfixture.Bundle{Key: "integration_worker"})
	adapter := &integrationOutboxAdapter{result: integrationcontract.CallResult{ResponseRef: "webhook:sent"}}
	probe := &integrationResilienceStoreProbe{beforeErr: resilience.ErrRateLimited}
	service := integrationOutboxAdapterService(connection, adapter, &independentDeliveryRepository{})
	service.policyStore = probe

	if _, err := service.SendAdapterOutboxMessage(t.Context(), message, principal); err == nil || err.Error() != "backend.integration.outbox.rate_limited" {
		t.Fatalf("policy admission error=%v", err)
	}
	if adapter.request.Operation != "" {
		t.Fatalf("adapter called before policy admission: %#v", adapter.request)
	}

	probe.beforeErr = nil
	probe.recordErr = errIntegrationManagementTest
	result, err := service.SendAdapterOutboxMessage(t.Context(), message, principal)
	if err != nil || result.Status != "sent" || result.ResponseRef != "webhook:sent" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if probe.beforeKey != "outbox:workspace:primary" || probe.recordKey != probe.beforeKey || !probe.recordSuccess {
		t.Fatalf("before=%q record=%q success=%v", probe.beforeKey, probe.recordKey, probe.recordSuccess)
	}
	if probe.before.MinInterval != 2*time.Second || probe.record.FailureThreshold != 7 || probe.record.Cooldown != 9*time.Second {
		t.Fatalf("before=%#v record=%#v", probe.before, probe.record)
	}

	nonWebhook := connection
	nonWebhook.ConnectorKey, nonWebhook.ProviderKey = "email", "smtp"
	nonWebhookAdapter := &integrationOutboxAdapter{result: integrationcontract.CallResult{ResponseRef: "email:sent"}}
	nonWebhookService := integrationOutboxAdapterService(nonWebhook, nonWebhookAdapter, &independentDeliveryRepository{})
	nonWebhookService.policyStore = &integrationResilienceStoreProbe{beforeErr: resilience.ErrRateLimited}
	nonWebhookMessage := message
	nonWebhookMessage.ConnectorKey = "email"
	if result, err := nonWebhookService.SendAdapterOutboxMessage(t.Context(), nonWebhookMessage, principal); err != nil || result.Status != "sent" {
		t.Fatalf("non-webhook result=%#v err=%v", result, err)
	}
}
