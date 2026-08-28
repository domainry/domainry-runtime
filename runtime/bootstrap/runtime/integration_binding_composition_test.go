package runtime

import (
	"context"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	connector "github.com/domainry/domainry-connector-sdk"
	apperror "github.com/domainry/domainry-foundation/apperror"
	businessintegration "github.com/domainry/domainry-runtime/runtime/application/integration"
	connectortest "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit/connectors"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"strings"
	"testing"
)

type integrationBindingAdapter struct{}

func (integrationBindingAdapter) Call(context.Context, integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	return integrationcontract.CallResult{}, nil
}

func integrationBindingProvider() connector.Adapter {
	connector := integrationBindingTestConnector()
	return connectortest.Provider(connector.Key, connector.Provider, integrationBindingAdapter{}, connector.Operations, connector.Providers[0])
}

func TestIntegrationBindingCompositionAggregatesReadinessSecretAndProtocolIssues(t *testing.T) {
	application, _ := newIntegrationCompositionApp(t, "binding-validation", integrationBindingProvider())
	defer application.CloseContext(t.Context())
	integrations := application.records.Applications().Integrations
	integrations.RegisterBuiltinConnectorDefinitions([]integrationmodel.ConnectorSchema{integrationBindingTestConnector()})
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "default"}}, accessfixture.Bundle{Key: "admin", Permissions: []string{businessintegration.PermissionConnectionManage}})
	draft := integrationmodel.IntegrationConnectionUpsertRequest{Key: "finance-disabled", ConnectorKey: "finance", Status: "disabled", Config: map[string]any{"responses": map[string]any{"charge": map[string]any{"receipt": "ok", "accepted": true}}}, SecretRefs: map[string]string{"api_key": "secret:missing"}}
	result, err := integrations.ValidateIntegrationBinding(t.Context(), businessintegration.BindingValidationRequest{
		ConnectorKey: "finance", ConnectionKey: "finance-disabled", ConnectionDraft: &draft, OperationKey: "charge",
		Input: map[string]any{"amount": "wrong", "extra": true}, Output: map[string]any{"accepted": "yes"},
	}, admin)
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid || result.ConnectionReady || !result.ConnectorReady || !result.OperationReady || result.SecretRefsReady || len(result.Errors) != 8 {
		t.Fatalf("unexpected binding validation: %#v", result)
	}
	expectedPaths := map[string]bool{"connection_draft.status": true, "connection.secret_refs.api_key": true, "connection_draft.secret_refs": true, "input.token": true, "input.amount": true, "input.extra": true, "output.receipt": true, "output.accepted": true}
	for _, issue := range result.Errors {
		if !expectedPaths[issue.FieldPath] || issue.OperationKey != "charge" || issue.CapabilityKey == "" || issue.ContractVersion == "" {
			t.Fatalf("unexpected binding issue: %#v", issue)
		}
		for _, value := range issue.Params {
			if strings.Contains(value, "secret:missing") {
				t.Fatalf("binding issue leaked secret material: %#v", issue)
			}
		}
	}
}

func TestIntegrationConnectionCompositionReusesBindingSecretValidation(t *testing.T) {
	application, _ := newIntegrationCompositionApp(t, "binding-write", integrationBindingProvider())
	defer application.CloseContext(t.Context())
	integrations := application.records.Applications().Integrations
	integrations.RegisterBuiltinConnectorDefinitions([]integrationmodel.ConnectorSchema{integrationBindingTestConnector()})
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "default"}}, accessfixture.Bundle{Key: "admin", Permissions: []string{businessintegration.PermissionConnectionManage}})
	request := integrationmodel.IntegrationConnectionUpsertRequest{ConnectorKey: "finance", Status: "configured", Config: map[string]any{"responses": map[string]any{"charge": map[string]any{"receipt": "ok", "accepted": true}}}}
	if _, err := integrations.UpsertIntegrationConnection(t.Context(), "finance", request, admin); apperror.CodeOf(err) != "backend.integration.binding.secret_ref_required" {
		t.Fatalf("expected write to reuse binding validation, got %v", err)
	}
	request.SecretRefs = map[string]string{"api_key": "env:FINANCE_API_KEY"}
	result, err := integrations.ValidateIntegrationBinding(t.Context(), businessintegration.BindingValidationRequest{ConnectorKey: "finance", ConnectionKey: "finance", ConnectionDraft: &request}, admin)
	if err != nil || !result.Valid || !result.ConnectionReady || !result.SecretRefsReady {
		t.Fatalf("valid connection draft result=%#v err=%v", result, err)
	}
}

func integrationBindingTestConnector() integrationmodel.ConnectorSchema {
	return integrationmodel.ConnectorSchema{Key: "finance", Type: "mock", Provider: "test", SecretRefs: []string{"api_key"}, Config: map[string]any{"required_secret_refs": []any{"api_key"}}, Providers: []integrationmodel.ConnectorProviderSchema{{Key: "test", SecretFields: []definitionmodel.FieldSchema{{Key: "api_key", Type: "text", Required: true, Config: map[string]any{"credential_kind": "api_key"}}}}}, Operations: []integrationmodel.ConnectorOperationSchema{{
		Key: "charge", Method: "POST", ExecutionMode: "sync", SideEffect: "write",
		Input:  []definitionmodel.FieldSchema{{Key: "amount", Type: "decimal", Required: true}, {Key: "token", Type: "text", Required: true}},
		Output: []definitionmodel.FieldSchema{{Key: "receipt", Type: "text", Required: true}, {Key: "accepted", Type: "boolean", Required: true}},
	}}}
}
