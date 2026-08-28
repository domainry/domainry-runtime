package integration

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type operationIdentityAdapterEdge struct {
	identity integrationcontract.OperationIdentity
	found    bool
}

func (*operationIdentityAdapterEdge) Call(context.Context, integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	return integrationcontract.CallResult{}, nil
}

func (a *operationIdentityAdapterEdge) OperationIdentity(string) (integrationcontract.OperationIdentity, bool) {
	return a.identity, a.found
}

type providerOverrideRegistry struct {
	Registry
	adapter integrationcontract.Adapter
	found   bool
}

func (r providerOverrideRegistry) ProviderAdapter(string, string) (integrationcontract.Adapter, bool) {
	return r.adapter, r.found
}

func durableIntentForAdapterResolution() runtimeext.DurableIntent {
	return runtimeext.DurableIntent{
		ConsumerKey:    "connector",
		ConnectionKey:  "connection",
		OperationKey:   "send",
		ContractSHA256: strings.Repeat("a", 64),
		Payload:        map[string]any{"value": "ready"},
	}
}

func TestValidateActionDurableIntentRemainingFailures(t *testing.T) {
	intent := durableIntentForAdapterResolution()
	principal := integrationManagementPrincipal()
	connection := integrationmodel.IntegrationConnection{
		Key: "connection", WorkspaceID: "workspace", ConnectorKey: "connector",
		ProviderKey: "provider", Status: "active",
	}
	operation := integrationmodel.ConnectorOperationSchema{
		Key: "send", ExecutionMode: "async", SideEffect: "write", IdempotencySupported: true,
	}
	schema := integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{
		Key: "connector",
		Providers: []integrationmodel.ConnectorProviderSchema{{
			Key: "provider", OperationKeys: []string{"send"},
		}},
		Operations: []integrationmodel.ConnectorOperationSchema{operation},
	}}}
	repository := &connectionResolutionRepo{connections: []integrationmodel.IntegrationConnection{connection}}
	registry := NewConnectorRegistry(schema)
	registerTestRegistryProvider(registry, "connector", "provider", &callOnlyAdapter{})
	service := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository: repository,
		Registry:         registry,
	})
	registeredAdapter, ok := service.AdapterForConnection(connection)
	if !ok {
		t.Fatal("registered adapter is unavailable")
	}
	identityProvider, ok := registeredAdapter.(integrationcontract.OperationIdentityProvider)
	if !ok {
		t.Fatal("registered adapter does not expose operation identity")
	}
	registeredIdentity, ok := identityProvider.OperationIdentity("send")
	if !ok {
		t.Fatal("registered send operation identity is unavailable")
	}
	operationMode := operation
	operationMode.ExecutionMode = "operation"
	if _, err := service.actionOutboxOperationIdentity(connection, operationMode, registeredIdentity.ContractSHA256); err != nil {
		t.Fatalf("operation mode identity error=%v", err)
	}
	durableMessage := integrationmodel.IntegrationOutboxMessage{
		ID:                 "durable_intent:execution:0",
		ConnectorKey:       "connector",
		Operation:          "send",
		RequestFingerprint: registeredIdentity.ContractSHA256,
		AttemptCount:       1,
	}
	if _, err := service.prepareRegisteredOutboxCall(t.Context(), durableMessage, connection, principal, intent.Payload); err != nil {
		t.Fatalf("valid durable outbox preparation error=%v", err)
	}

	if err := service.ValidateActionDurableIntent(t.Context(), intent, principalmodel.Principal{}); testErrorCode(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization error=%v", err)
	}
	invalid := intent
	invalid.ContractSHA256 = "invalid"
	if err := service.ValidateActionDurableIntent(t.Context(), invalid, principal); testErrorCode(err) != "backend.action.durable_intent_invalid" {
		t.Fatalf("invalid intent error=%v", err)
	}
	missingConnector := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository: repository,
		Registry:         registry,
		ConnectorExists: func(string) bool {
			return false
		},
	})
	if err := missingConnector.ValidateActionDurableIntent(t.Context(), intent, principal); testErrorCode(err) != "backend.integration.connector.not_found" {
		t.Fatalf("missing connector error=%v", err)
	}
	repository.listErr = errIntegrationManagementTest
	if err := service.ValidateActionDurableIntent(t.Context(), intent, principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("connection lookup error=%v", err)
	}
	repository.listErr = nil
	repository.connections = nil
	if err := service.ValidateActionDurableIntent(t.Context(), intent, principal); testErrorCode(err) != "backend.integration.connection_unavailable" {
		t.Fatalf("missing connection error=%v", err)
	}
	repository.connections = []integrationmodel.IntegrationConnection{{
		Key: "connection", WorkspaceID: "workspace", ConnectorKey: "connector",
		ProviderKey: "provider", Status: "disabled",
	}}
	if err := service.ValidateActionDurableIntent(t.Context(), intent, principal); testErrorCode(err) != "backend.integration.connection_unavailable" {
		t.Fatalf("disabled connection error=%v", err)
	}
	repository.connections[0].Status = "active"
	repository.connections[0].ConnectorKey = "other"
	if err := service.ValidateActionDurableIntent(t.Context(), intent, principal); testErrorCode(err) != "backend.integration.connection_unavailable" {
		t.Fatalf("connector mismatch error=%v", err)
	}
	repository.connections = []integrationmodel.IntegrationConnection{connection}

	unsupportedSchema := schema
	unsupportedSchema.Connectors[0].Providers[0].OperationKeys = []string{"other"}
	unsupported := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository: repository,
		Registry:         NewConnectorRegistry(unsupportedSchema),
	})
	if err := unsupported.ValidateActionDurableIntent(t.Context(), intent, principal); testErrorCode(err) != "backend.integration.operation.provider_unsupported" {
		t.Fatalf("provider unsupported error=%v", err)
	}

	missingOperationSchema := schema
	missingOperationSchema.Connectors[0].Providers[0].OperationKeys = nil
	missingOperationSchema.Connectors[0].Operations = nil
	missingOperation := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository: repository,
		Registry:         NewConnectorRegistry(missingOperationSchema),
	})
	if err := missingOperation.ValidateActionDurableIntent(t.Context(), intent, principal); testErrorCode(err) != "backend.automation.connector_operation_not_found" {
		t.Fatalf("missing operation error=%v", err)
	}
}

func TestActionOutboxOperationIdentityRemainingMatrix(t *testing.T) {
	connection := integrationmodel.IntegrationConnection{ConnectorKey: "connector", ProviderKey: "provider"}
	operation := integrationmodel.ConnectorOperationSchema{Key: "send", ExecutionMode: "async", SideEffect: "write"}
	baseRegistry := NewConnectorRegistry(integrationmodel.IntegrationSchema{})
	serviceWith := func(adapter integrationcontract.Adapter, found bool) *IntegrationApplicationService {
		return NewIntegrationApplicationService(ApplicationDependencies{Registry: providerOverrideRegistry{
			Registry: baseRegistry,
			adapter:  adapter,
			found:    found,
		}})
	}
	assertCode := func(name string, service *IntegrationApplicationService, candidate integrationmodel.ConnectorOperationSchema, contract, want string) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			_, err := service.actionOutboxOperationIdentity(connection, candidate, contract)
			if testErrorCode(err) != want {
				t.Fatalf("error=%v want=%q", err, want)
			}
		})
	}

	assertCode("synchronous", serviceWith(nil, false), integrationmodel.ConnectorOperationSchema{Key: "send", ExecutionMode: "sync", SideEffect: "write"}, "", runtimeext.ConnectorActionSideEffectOutboxErrorCode)
	assertCode("read", serviceWith(nil, false), integrationmodel.ConnectorOperationSchema{Key: "send", ExecutionMode: "async", SideEffect: "read"}, "", runtimeext.ConnectorActionSideEffectOutboxErrorCode)
	assertCode("adapter missing", serviceWith(nil, false), operation, "", "backend.integration.sync_call.connector_unsupported")
	assertCode("identity provider missing", serviceWith(&callOnlyAdapter{}, true), operation, "", "backend.integration.sync_call.operation_identity_unavailable")
	assertCode("operation missing", serviceWith(&operationIdentityAdapterEdge{}, true), operation, "", "backend.integration.operation.provider_unsupported")
	assertCode("invalid mode", serviceWith(&operationIdentityAdapterEdge{found: true, identity: integrationcontract.OperationIdentity{Mode: "call"}}, true), operation, "", runtimeext.ConnectorActionSideEffectOutboxErrorCode)
	assertCode("read identity", serviceWith(&operationIdentityAdapterEdge{found: true, identity: integrationcontract.OperationIdentity{Mode: "enqueue", Effect: "read"}}, true), operation, "", "backend.action.durable_intent.operation_effect_mismatch")
	assertCode("effect mismatch", serviceWith(&operationIdentityAdapterEdge{found: true, identity: integrationcontract.OperationIdentity{Mode: "start_operation", Effect: "delete"}}, true), operation, "", "backend.action.durable_intent.operation_effect_mismatch")

	valid := &operationIdentityAdapterEdge{found: true, identity: integrationcontract.OperationIdentity{
		Key: "send", Mode: "start_operation", Effect: "write", ContractSHA256: strings.Repeat("b", 64),
	}}
	if identity, err := serviceWith(valid, true).actionOutboxOperationIdentity(connection, operation, valid.identity.ContractSHA256); err != nil || identity != valid.identity {
		t.Fatalf("valid identity=%#v error=%v", identity, err)
	}
}

func TestValidateAdapterConfigWithoutValidatorFromRegistry(t *testing.T) {
	service := NewIntegrationApplicationService(ApplicationDependencies{Registry: providerOverrideRegistry{
		Registry: NewConnectorRegistry(integrationmodel.IntegrationSchema{}),
		adapter:  &callOnlyAdapter{},
		found:    true,
	}})
	if err := service.ValidateAdapterConfig(integrationmodel.IntegrationConnection{
		ConnectorKey: "connector", ProviderKey: "provider", Status: "active",
	}); err != nil {
		t.Fatalf("non-validator adapter error=%v", err)
	}
}
