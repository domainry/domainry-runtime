// Integration application service connection lifecycle tests.
package integration

import integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"

import (
	"context"
	"errors"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

type lifecycleAdapter struct {
	err  error
	wait bool
}

func (a lifecycleAdapter) Call(ctx context.Context, _ integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	if a.wait {
		<-ctx.Done()
		return integrationcontract.CallResult{}, ctx.Err()
	}
	if a.err != nil {
		return integrationcontract.CallResult{}, a.err
	}
	return integrationcontract.CallResult{Response: map[string]any{"ok": true}}, nil
}

func (a lifecycleAdapter) TestConnection(ctx context.Context, request integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	return a.Call(ctx, request)
}

func TestConnectionLifecycleRequiresVerificationAndPreservesCallerCancellation(t *testing.T) {
	repository := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{}, secrets: map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{}}
	delivery := &independentDeliveryRepository{}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "probe", Type: "http", Provider: "probe", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "probe"}}, Operations: []integrationmodel.ConnectorOperationSchema{{Key: "ping", Method: "POST", SideEffect: "read"}}}}})
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, DeliveryRepository: delivery, Registry: registry})
	registerTestProviderAdapter(service, "probe", "probe", lifecycleAdapter{})
	admin := integrationWorkspaceAdmin("admin", "workspace")
	configured, err := service.UpsertIntegrationConnection(t.Context(), "probe", integrationmodel.IntegrationConnectionUpsertRequest{ConnectorKey: "probe", ProviderKey: "probe", Status: "configured", Config: map[string]any{"url": "https://example.invalid"}}, admin)
	if err != nil {
		t.Fatal(err)
	}
	if ConnectionCanSend(configured) {
		t.Fatal("configured connection entered production execution")
	}
	if _, err := service.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{ConnectorKey: "probe", ConnectionKey: "probe", Operation: "ping"}, admin); testErrorCode(err) != "backend.integration.connection_unavailable" {
		t.Fatalf("production call error=%v", err)
	}
	verified, err := service.TestConnectorOperation(t.Context(), "probe", ConnectorOperationTestRequest{Operation: "ping", Confirm: true}, admin)
	if err != nil || verified.Connection.Status != "verified" || !ConnectionCanSend(verified.Connection) {
		t.Fatalf("verified=%+v error=%v", verified, err)
	}

	registerTestProviderAdapter(service, "probe", "probe", lifecycleAdapter{wait: true})
	if _, err := service.UpsertIntegrationConnection(t.Context(), "cancelled", integrationmodel.IntegrationConnectionUpsertRequest{ConnectorKey: "probe", ProviderKey: "probe", Status: "configured", Config: map[string]any{"url": "https://example.invalid"}}, admin); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.TestConnectorOperation(ctx, "cancelled", ConnectorOperationTestRequest{Operation: "ping", Confirm: true}, admin); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%v", err)
	}
	if status := repository.connections["cancelled"].Status; status != "configured" {
		t.Fatalf("cancelled test mutated status to %q", status)
	}
}

func TestConnectionLifecycleMarksProviderFailureDegraded(t *testing.T) {
	repository := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{}, secrets: map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{}}
	delivery := &independentDeliveryRepository{}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "probe", Type: "http", Provider: "probe", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "probe"}}, Operations: []integrationmodel.ConnectorOperationSchema{{Key: "test_connection", Method: "POST", SideEffect: "read"}}}}})
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, DeliveryRepository: delivery, Registry: registry})
	registerTestProviderAdapter(service, "probe", "probe", lifecycleAdapter{err: errors.New("provider unavailable")})
	admin := integrationWorkspaceAdmin("admin", "workspace")
	if _, err := service.UpsertIntegrationConnection(t.Context(), "probe", integrationmodel.IntegrationConnectionUpsertRequest{ConnectorKey: "probe", ProviderKey: "probe", Status: "configured", Config: map[string]any{"url": "https://example.invalid"}}, admin); err != nil {
		t.Fatal(err)
	}
	result, err := service.TestConnectorOperation(t.Context(), "probe", ConnectorOperationTestRequest{Operation: "test_connection", Confirm: true}, admin)
	if err == nil || result.Connection.Status != "degraded" || repository.connections["probe"].Status != "degraded" {
		t.Fatalf("result=%+v error=%v stored=%+v", result, err, repository.connections)
	}
}

func TestConnectionIdentityKeysAreStableAndImmutable(t *testing.T) {
	repository := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{}, secrets: map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{}}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{
		{Key: "probe", Type: "http", Provider: "multi", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "first"}, {Key: "second"}}},
		{Key: "other", Type: "http", Provider: "first", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "first"}}},
	}})
	application := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: registry})
	registerTestProviderAdapter(application, "probe", "first", lifecycleAdapter{})
	registerTestProviderAdapter(application, "probe", "second", lifecycleAdapter{})
	registerTestProviderAdapter(application, "other", "first", lifecycleAdapter{})
	admin := integrationWorkspaceAdmin("admin", "workspace")

	if _, err := application.UpsertIntegrationConnection(t.Context(), "Invalid Key", integrationmodel.IntegrationConnectionUpsertRequest{ConnectorKey: "probe", ProviderKey: "first"}, admin); testErrorCode(err) != "backend.integration.connection.key_invalid" {
		t.Fatalf("invalid key error=%v", err)
	}
	config := map[string]any{"url": "https://example.invalid"}
	if _, err := application.UpsertIntegrationConnection(t.Context(), "stable_connection", integrationmodel.IntegrationConnectionUpsertRequest{ConnectorKey: "probe", ProviderKey: "first", Config: config}, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := application.UpsertIntegrationConnection(t.Context(), "stable_connection", integrationmodel.IntegrationConnectionUpsertRequest{ConnectorKey: "other", ProviderKey: "first", Config: config}, admin); testErrorCode(err) != "backend.integration.connection.connector_immutable" {
		t.Fatalf("connector mutation error=%v", err)
	}
	if _, err := application.UpsertIntegrationConnection(t.Context(), "stable_connection", integrationmodel.IntegrationConnectionUpsertRequest{ConnectorKey: "probe", ProviderKey: "second", Config: config}, admin); testErrorCode(err) != "backend.integration.connection.provider_immutable" {
		t.Fatalf("provider mutation error=%v", err)
	}
	if _, err := application.RotateIntegrationConnection(t.Context(), "stable_connection", integrationmodel.IntegrationConnectionUpsertRequest{ProviderKey: "second", SecretRefs: map[string]string{"token": "env:PROBE_TOKEN"}}, admin); testErrorCode(err) != "backend.integration.connection.provider_immutable" {
		t.Fatalf("rotation provider mutation error=%v", err)
	}
	stored := repository.connections["stable_connection"]
	if stored.ConnectorKey != "probe" || stored.ProviderKey != "first" {
		t.Fatalf("stored identity mutated: %+v", stored)
	}
}

func TestDisableConnectionStopsCallsAndPreservesDeliveryHistory(t *testing.T) {
	repository := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{}, secrets: map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{}}
	delivery := &independentDeliveryRepository{}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "probe", Type: "http", Provider: "probe", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "probe"}}, Operations: []integrationmodel.ConnectorOperationSchema{{Key: "ping", Method: "POST", SideEffect: "read"}}}}})
	application := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, DeliveryRepository: delivery, Registry: registry})
	registerTestProviderAdapter(application, "probe", "probe", lifecycleAdapter{})
	admin := integrationWorkspaceAdmin("admin", "workspace")
	if _, err := application.UpsertIntegrationConnection(t.Context(), "probe", integrationmodel.IntegrationConnectionUpsertRequest{ConnectorKey: "probe", ProviderKey: "probe", Status: "verified", Config: map[string]any{"url": "https://example.invalid"}}, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := application.TestConnectorOperation(t.Context(), "probe", ConnectorOperationTestRequest{Operation: "ping", Confirm: true}, admin); err != nil {
		t.Fatal(err)
	}
	if len(delivery.invocations) != 1 {
		t.Fatalf("invocations=%d", len(delivery.invocations))
	}
	disabled, err := application.DisableIntegrationConnection(t.Context(), "probe", admin)
	if err != nil || disabled.Status != "disabled" {
		t.Fatalf("disabled=%+v error=%v", disabled, err)
	}
	if _, err := application.TestConnectorOperation(t.Context(), "probe", ConnectorOperationTestRequest{Operation: "ping", Confirm: true}, admin); testErrorCode(err) != "backend.integration.connection_unavailable" {
		t.Fatalf("disabled call error=%v", err)
	}
	if len(delivery.invocations) != 1 {
		t.Fatalf("disable removed delivery history: %d", len(delivery.invocations))
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := application.UpsertIntegrationConnection(t.Context(), "cancel-probe", integrationmodel.IntegrationConnectionUpsertRequest{ConnectorKey: "probe", ProviderKey: "probe", Status: "verified", Config: map[string]any{"url": "https://example.invalid"}}, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := application.DisableIntegrationConnection(ctx, "cancel-probe", admin); err != context.Canceled {
		t.Fatalf("cancelled disable error=%v", err)
	}
	if repository.connections["cancel-probe"].Status != "verified" {
		t.Fatalf("cancelled disable mutated connection: %+v", repository.connections["cancel-probe"])
	}
}

func TestDeleteConnectionFailsClosedForEveryReferenceKind(t *testing.T) {
	connectionKey := "probe"
	tests := []struct {
		name string
		kind string
	}{
		{name: "action", kind: "action"}, {name: "automation", kind: "automation"},
		{name: "workflow", kind: "workflow"}, {name: "event mapping", kind: "event_mapping"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{connectionKey: {Key: connectionKey, WorkspaceID: "workspace", ConnectorKey: "probe", ProviderKey: "probe", Status: "disabled"}}, secrets: map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{}}
			registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "probe", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "probe"}}}}})
			application := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: registry, ConnectionReferenceCheck: func(context.Context, string, principalmodel.Principal) ([]ConnectionReference, error) {
				return []ConnectionReference{{Kind: test.kind, Key: "reference", Path: "connection_key"}}, nil
			}})
			admin := integrationWorkspaceAdmin("admin", "workspace")
			err := application.DeleteIntegrationConnection(t.Context(), connectionKey, admin)
			if testErrorCode(err) != "backend.integration.connection.referenced" || repository.connections[connectionKey].Key == "" {
				t.Fatalf("delete error=%v remaining=%+v", err, repository.connections)
			}
		})
	}
}

func TestDeleteConnectionChecksWebhookAndPreservesCallerContext(t *testing.T) {
	repository := &independentConfigRepository{
		connections: map[string]integrationmodel.IntegrationConnection{"probe": {Key: "probe", WorkspaceID: "workspace", ConnectorKey: "probe", ProviderKey: "probe", Status: "disabled"}},
		secrets:     map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{},
		subscriptions: []integrationmodel.IntegrationWebhookSubscription{{Key: "hook", WorkspaceID: "workspace", ConnectionKey: "probe"}},
	}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "probe", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "probe"}}}}})
	application := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: registry})
	admin := integrationWorkspaceAdmin("admin", "workspace")
	if err := application.DeleteIntegrationConnection(t.Context(), "probe", admin); testErrorCode(err) != "backend.integration.connection.referenced" {
		t.Fatalf("webhook reference error=%v", err)
	}
	repository.subscriptions = nil
	ctx, cancel := context.WithCancel(t.Context())
	application = NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: registry, ConnectionReferenceCheck: func(ctx context.Context, _ string, _ principalmodel.Principal) ([]ConnectionReference, error) {
		cancel()
		return nil, ctx.Err()
	}})
	if err := application.DeleteIntegrationConnection(ctx, "probe", admin); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled delete error=%v", err)
	}
	application = NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: registry, ConnectionReferenceCheck: func(context.Context, string, principalmodel.Principal) ([]ConnectionReference, error) {
		return nil, nil
	}})
	if err := application.DeleteIntegrationConnection(t.Context(), "probe", admin); err != nil {
		t.Fatal(err)
	}
	if _, exists := repository.connections["probe"]; exists {
		t.Fatal("unreferenced connection was not deleted")
	}
}
