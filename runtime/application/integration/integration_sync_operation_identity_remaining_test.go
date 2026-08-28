package integration

import (
	"strings"
	"testing"

	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestValidateSyncOperationIdentityRemainingMatrix(t *testing.T) {
	connection := integrationmodel.IntegrationConnection{ConnectorKey: "connector", ProviderKey: "provider"}
	baseRegistry := NewConnectorRegistry(integrationmodel.IntegrationSchema{})
	serviceWith := func(adapter integrationcontract.Adapter, found bool) *IntegrationApplicationService {
		return NewIntegrationApplicationService(ApplicationDependencies{Registry: providerOverrideRegistry{
			Registry: baseRegistry,
			adapter:  adapter,
			found:    found,
		}})
	}
	request := SyncCallRequest{
		Operation:       "send",
		OperationMode:   "call",
		ContractSHA256:  strings.Repeat("a", 64),
		OperationEffect: "read",
	}
	assertCode := func(name string, service *IntegrationApplicationService, candidate SyncCallRequest, want string) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			if code := testErrorCode(service.validateSyncOperationIdentity(connection, candidate)); code != want {
				t.Fatalf("code=%q want=%q", code, want)
			}
		})
	}

	partial := request
	partial.ContractSHA256 = ""
	assertCode("missing contract", serviceWith(nil, false), partial, "backend.integration.sync_call.operation_identity_required")
	partial = request
	partial.OperationMode = ""
	assertCode("missing mode", serviceWith(nil, false), partial, "backend.integration.sync_call.operation_identity_required")
	assertCode("adapter missing", serviceWith(nil, false), request, "backend.integration.sync_call.connector_unsupported")
	assertCode("identity provider missing", serviceWith(&callOnlyAdapter{}, true), request, "backend.integration.sync_call.operation_identity_unavailable")
	assertCode("operation missing", serviceWith(&operationIdentityAdapterEdge{}, true), request, "backend.integration.operation.provider_unsupported")

	identity := integrationcontract.OperationIdentity{
		Key:            "send",
		Mode:           "call",
		Effect:         "read",
		ContractSHA256: request.ContractSHA256,
	}
	valid := serviceWith(&operationIdentityAdapterEdge{identity: identity, found: true}, true)
	if err := valid.validateSyncOperationIdentity(connection, request); err != nil {
		t.Fatalf("matching operation identity error=%v", err)
	}
	withoutEffect := request
	withoutEffect.OperationEffect = ""
	if err := valid.validateSyncOperationIdentity(connection, withoutEffect); err != nil {
		t.Fatalf("optional effect error=%v", err)
	}
	mismatchedEffect := request
	mismatchedEffect.OperationEffect = "write"
	assertCode("effect mismatch", valid, mismatchedEffect, "backend.integration.sync_call.operation_effect_mismatch")
}
