package integration

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func bindingValidationConnector() integrationmodel.ConnectorSchema {
	return integrationmodel.ConnectorSchema{
		Key: "finance", Type: "mock", Provider: "test", SecretRefs: []string{"api_key"},
		Config: map[string]any{"required_secret_refs": []any{"api_key"}},
		Providers: []integrationmodel.ConnectorProviderSchema{{Key: "test", SecretFields: []definitionmodel.FieldSchema{{
			Key: "api_key", Type: "text", Required: true, Config: map[string]any{"credential_kind": "api_key"},
		}}}},
		Operations: []integrationmodel.ConnectorOperationSchema{{
			Key: "charge", Method: "POST", ExecutionMode: "sync", SideEffect: "write",
			Input:  []definitionmodel.FieldSchema{{Key: "amount", Type: "decimal", Required: true}, {Key: "token", Type: "text", Required: true}},
			Output: []definitionmodel.FieldSchema{{Key: "receipt", Type: "text", Required: true}, {Key: "accepted", Type: "boolean", Required: true}},
		}},
	}
}

func newBindingValidationService(connector integrationmodel.ConnectorSchema, adapterReady bool) (*IntegrationApplicationService, *independentConfigRepository) {
	repository := &independentConfigRepository{
		connections: map[string]integrationmodel.IntegrationConnection{},
		secrets:     map[string]integrationmodel.IntegrationSecret{},
		materials:   map[string]string{},
	}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{connector}})
	if adapterReady {
		registerTestRegistryProvider(registry, "finance", "test", independentAdapter{provider: "mock"})
	}
	return NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: registry}), repository
}

func bindingValidationPrincipal(permissions ...string) principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user", WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: permissions})
}

func TestValidateIntegrationBindingAuthorization(t *testing.T) {
	service, _ := newBindingValidationService(bindingValidationConnector(), true)
	if _, err := service.ValidateIntegrationBinding(t.Context(), BindingValidationRequest{}, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace authorization error = %v", err)
	}
	if _, err := service.ValidateIntegrationBinding(t.Context(), BindingValidationRequest{}, bindingValidationPrincipal()); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("permission error = %v", err)
	}
}

func TestValidateIntegrationBindingConnectorReadiness(t *testing.T) {
	principal := bindingValidationPrincipal(PermissionCatalogView)

	t.Run("not found", func(t *testing.T) {
		service, _ := newBindingValidationService(bindingValidationConnector(), true)
		result, err := service.ValidateIntegrationBinding(t.Context(), BindingValidationRequest{ConnectorKey: "missing"}, principal)
		assertBindingValidationResult(t, result, err, false, "backend.integration.connector.not_found")
		if len(result.Errors) != 1 || result.Errors[0].Params["actual"] != "missing" {
			t.Fatalf("result = %#v", result)
		}
	})

	t.Run("reclassified", func(t *testing.T) {
		connector := bindingValidationConnector()
		connector.LifecycleStatus, connector.ReplacementCapability = "deprecated", "payments.v2"
		service, _ := newBindingValidationService(connector, true)
		result, err := service.ValidateIntegrationBinding(t.Context(), BindingValidationRequest{ConnectorKey: "finance"}, principal)
		assertBindingValidationResult(t, result, err, false, "backend.integration.connector.reclassified")
	})

	t.Run("definition not ready", func(t *testing.T) {
		connector := bindingValidationConnector()
		connector.Type = ""
		service, _ := newBindingValidationService(connector, true)
		result, err := service.ValidateIntegrationBinding(t.Context(), BindingValidationRequest{ConnectorKey: "finance"}, principal)
		assertBindingValidationResult(t, result, err, false, "backend.integration.connector.definition_not_ready")
	})

	t.Run("adapter not ready", func(t *testing.T) {
		service, _ := newBindingValidationService(bindingValidationConnector(), false)
		result, err := service.ValidateIntegrationBinding(t.Context(), BindingValidationRequest{ConnectorKey: "finance"}, principal)
		assertBindingValidationResult(t, result, err, false, "backend.integration.connector.adapter_not_ready")
	})
}

func TestValidateIntegrationBindingAggregatesOperationConnectionSecretAndProtocolIssues(t *testing.T) {
	service, repository := newBindingValidationService(bindingValidationConnector(), true)
	repository.connections["wrong"] = integrationmodel.IntegrationConnection{
		Key: "wrong", WorkspaceID: "workspace", ConnectorKey: "other", ProviderKey: "test", Status: "configured",
		SecretRefs: map[string]string{"api_key": "secret:disabled"},
	}
	repository.secrets["disabled"] = integrationmodel.IntegrationSecret{Key: "disabled", WorkspaceID: "workspace", Status: "disabled"}
	principal := bindingValidationPrincipal(PermissionConnectionManage)

	result, err := service.ValidateIntegrationBinding(t.Context(), BindingValidationRequest{
		ConnectorKey: "finance", OperationKey: "charge", ConnectionKey: "wrong",
		Input: map[string]any{"amount": "wrong", "token": nil, "extra": true}, Output: map[string]any{"accepted": "yes"},
	}, principal)
	if err != nil || result.Valid || result.ConnectionReady || !result.ConnectorReady || !result.OperationReady || result.SecretRefsReady {
		t.Fatalf("result = %#v err=%v", result, err)
	}
	wantCodes := map[string]bool{
		"backend.integration.binding.connection_connector_mismatch": true,
		"backend.integration.connection_unavailable":                true,
		"backend.integration.secret.disabled":                       true,
		"backend.integration.binding.protocol_value_required":       true,
		"backend.integration.binding.protocol_field_unknown":        true,
		"backend.integration.binding.protocol_type_mismatch":        true,
	}
	for _, issue := range result.Errors {
		delete(wantCodes, issue.ErrorCode)
		if issue.ContractVersion == "" || issue.CapabilityKey == "" || issue.ConnectorKey != "finance" || issue.OperationKey != "charge" {
			t.Fatalf("issue contract metadata = %#v", issue)
		}
	}
	if len(wantCodes) != 0 {
		t.Fatalf("missing issue codes = %#v; result=%#v", wantCodes, result)
	}
}

func TestValidateIntegrationBindingOperationAndConnectionSelection(t *testing.T) {
	service, repository := newBindingValidationService(bindingValidationConnector(), true)
	principal := bindingValidationPrincipal(PermissionCatalogView)

	tests := []struct {
		name    string
		request BindingValidationRequest
		code    string
	}{
		{name: "operation required", request: BindingValidationRequest{ConnectorKey: "finance", ConnectionKey: "missing", Input: map[string]any{}}, code: "backend.integration.binding.operation_required"},
		{name: "operation required by output", request: BindingValidationRequest{ConnectorKey: "finance", ConnectionKey: "missing", Output: map[string]any{}}, code: "backend.integration.binding.operation_required"},
		{name: "operation not found", request: BindingValidationRequest{ConnectorKey: "finance", OperationKey: "missing", ConnectionKey: "missing"}, code: "backend.integration.binding.operation_not_found"},
		{name: "connection required", request: BindingValidationRequest{ConnectorKey: "finance"}, code: "backend.integration.binding.connection_required"},
		{name: "connection not found", request: BindingValidationRequest{ConnectorKey: "finance", ConnectionKey: "missing"}, code: "backend.integration.connection.not_found"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := service.ValidateIntegrationBinding(t.Context(), test.request, principal)
			if err != nil || !bindingResultHasCode(result, test.code) {
				t.Fatalf("result = %#v err=%v", result, err)
			}
		})
	}

	repository.connections["ready"] = integrationmodel.IntegrationConnection{
		Key: "ready", WorkspaceID: "workspace", ConnectorKey: "finance", ProviderKey: "test", Status: "active",
		SecretRefs: map[string]string{"api_key": "env:FINANCE_API_KEY"},
		Config:     map[string]any{"responses": map[string]any{"charge": map[string]any{"receipt": "ok", "accepted": true}}},
	}
	result, err := service.ValidateIntegrationBinding(t.Context(), BindingValidationRequest{ConnectorKey: "finance", OperationKey: "charge", ConnectionKey: "ready", Input: map[string]any{"amount": 12.5, "token": "token"}, Output: map[string]any{"receipt": "ok", "accepted": true}}, principal)
	if err != nil || !result.Valid || !result.ConnectionReady || !result.ConnectorReady || !result.OperationReady || !result.SecretRefsReady || len(result.Errors) != 0 {
		t.Fatalf("ready result = %#v err=%v", result, err)
	}
}

func TestIntegrationBindingAuthoringExamplesExecuteThroughRealValidator(t *testing.T) {
	connector := bindingValidationConnector()
	operation := connector.Operations[0]
	capability := integrationcontract.SpecializeIntegrationBindingValidationAuthoringCapability(connector, nil, &operation)
	service, repository := newBindingValidationService(connector, true)
	repository.connections["finance_primary"] = integrationmodel.IntegrationConnection{
		Key: "finance_primary", WorkspaceID: "workspace", ConnectorKey: "finance", ProviderKey: "test", Status: "active",
		SecretRefs: map[string]string{"api_key": "env:FINANCE_API_KEY"},
		Config:     map[string]any{"responses": map[string]any{"charge": map[string]any{"receipt": "ok", "accepted": true}}},
	}
	principal := bindingValidationPrincipal(PermissionCatalogView)
	for _, example := range capability.Examples {
		t.Run(example.Name, func(t *testing.T) {
			encoded, err := json.Marshal(example.Value)
			if err != nil {
				t.Fatal(err)
			}
			var request BindingValidationRequest
			if err := json.Unmarshal(encoded, &request); err != nil {
				t.Fatal(err)
			}
			result, err := service.ValidateIntegrationBinding(t.Context(), request, principal)
			if err != nil {
				t.Fatal(err)
			}
			if len(example.ExpectedErrorCodes) == 0 {
				if !result.Valid || len(result.Errors) != 0 {
					t.Fatalf("example=%#v result=%#v", example.Value, result)
				}
				return
			}
			for _, code := range example.ExpectedErrorCodes {
				if !bindingResultHasCode(result, code) {
					t.Fatalf("example=%#v missing error=%s result=%#v", example.Value, code, result)
				}
			}
		})
	}
}

func TestBindingValidationAtomicConditionEdges(t *testing.T) {
	service, repository := newBindingValidationService(bindingValidationConnector(), true)
	if err := service.ValidateIntegrationConnectionDraft(t.Context(), "connection", integrationmodel.IntegrationConnectionUpsertRequest{}, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("draft authorization error=%v", err)
	}
	repository.connections["active-secret"] = integrationmodel.IntegrationConnection{Key: "active-secret", WorkspaceID: "workspace", ConnectorKey: "finance", ProviderKey: "test", Status: "active", SecretRefs: map[string]string{"api_key": "secret:active"}}
	repository.secrets["active"] = integrationmodel.IntegrationSecret{Key: "active", WorkspaceID: "workspace", Status: "active"}
	result, err := service.ValidateIntegrationBinding(t.Context(), BindingValidationRequest{ConnectorKey: "finance", OperationKey: "charge", ConnectionKey: "active-secret", Input: map[string]any{"amount": "", "token": struct{}{}}, Output: map[string]any{"receipt": "", "accepted": true}}, bindingValidationPrincipal(PermissionConnectionManage))
	if err != nil || result.SecretRefsReady == false || len(result.Errors) == 0 {
		t.Fatalf("atomic validation result=%#v err=%v", result, err)
	}
	connector := bindingValidationConnector()
	connector.Operations[0].Input = append(connector.Operations[0].Input, definitionmodel.FieldSchema{Key: "optional", Type: "text"})
	service, _ = newBindingValidationService(connector, true)
	_, _ = service.ValidateIntegrationBinding(t.Context(), BindingValidationRequest{ConnectorKey: "finance", OperationKey: "charge", Input: map[string]any{"amount": 1, "token": "token", "optional": "value"}}, bindingValidationPrincipal(PermissionCatalogView))
	validator := &bindingValidator{request: BindingValidationRequest{}, result: BindingValidationResult{Errors: []BindingValidationIssue{}}}
	validator.appendServiceError("connection", "connection", errors.New("plain failure"), "integration.connection")
	if len(validator.result.Errors) != 1 || validator.result.Errors[0].ErrorCode != "backend.internal" {
		t.Fatalf("plain service error=%#v", validator.result.Errors)
	}
}

func TestValidateIntegrationConnectionDraftMapsFirstIssue(t *testing.T) {
	service, _ := newBindingValidationService(bindingValidationConnector(), true)
	principal := bindingValidationPrincipal(PermissionConnectionManage)

	request := integrationmodel.IntegrationConnectionUpsertRequest{ConnectorKey: "finance", ProviderKey: "missing", Status: "invalid", SecretRefs: map[string]string{"api_key": "literal"}}
	err := service.ValidateIntegrationConnectionDraft(t.Context(), "", request, principal)
	if err == nil || apperror.CodeOf(err) != "backend.integration.connection.missing_key" || apperror.ParamsOf(err)["field_path"] != "connection_key" {
		t.Fatalf("draft error = %v params=%#v", err, apperror.ParamsOf(err))
	}

	valid := integrationmodel.IntegrationConnectionUpsertRequest{
		ConnectorKey: "finance", ProviderKey: "test", Status: "configured", SecretRefs: map[string]string{"api_key": "env:FINANCE_API_KEY"},
		Config: map[string]any{"responses": map[string]any{"charge": map[string]any{"receipt": "ok", "accepted": true}}},
	}
	if err := service.ValidateIntegrationConnectionDraft(t.Context(), "finance", valid, principal); err != nil {
		t.Fatalf("valid draft error = %v", err)
	}
}

func TestValidateIntegrationBindingDraftAndSecretBranches(t *testing.T) {
	principal := bindingValidationPrincipal(PermissionConnectionManage)
	validConfig := map[string]any{"responses": map[string]any{"charge": map[string]any{"receipt": "ok", "accepted": true}}}

	t.Run("draft connector mismatch and disabled status", func(t *testing.T) {
		service, _ := newBindingValidationService(bindingValidationConnector(), true)
		draft := integrationmodel.IntegrationConnectionUpsertRequest{
			Key: "draft", ConnectorKey: "other", ProviderKey: "test", Status: "disabled",
			SecretRefs: map[string]string{"api_key": "env:FINANCE_API_KEY"}, Config: validConfig,
		}
		result, err := service.ValidateIntegrationBinding(t.Context(), BindingValidationRequest{ConnectorKey: "finance", ConnectionDraft: &draft}, principal)
		if err != nil || !bindingResultHasCode(result, "backend.integration.binding.connection_connector_mismatch") || !bindingResultHasCode(result, "backend.integration.connection_unavailable") {
			t.Fatalf("result = %#v err=%v", result, err)
		}
	})

	t.Run("provider secret validation", func(t *testing.T) {
		connector := bindingValidationConnector()
		connector.Providers = []integrationmodel.ConnectorProviderSchema{{
			Key: "test", SecretFields: []definitionmodel.FieldSchema{{Key: "provider_token", Type: "text", Required: true}},
		}}
		service, _ := newBindingValidationService(connector, true)
		draft := integrationmodel.IntegrationConnectionUpsertRequest{
			Key: "draft", ConnectorKey: "finance", ProviderKey: "test", Status: "configured",
			SecretRefs: map[string]string{"api_key": "env:FINANCE_API_KEY"}, Config: validConfig,
		}
		result, err := service.ValidateIntegrationBinding(t.Context(), BindingValidationRequest{ConnectorKey: "finance", ConnectionDraft: &draft}, principal)
		if err != nil || !bindingResultHasCode(result, "backend.integration.connection.provider_secret_required") {
			t.Fatalf("result = %#v err=%v", result, err)
		}
	})

	t.Run("required secret missing", func(t *testing.T) {
		service, repository := newBindingValidationService(bindingValidationConnector(), true)
		repository.connections["missing-ref"] = integrationmodel.IntegrationConnection{
			Key: "missing-ref", WorkspaceID: "workspace", ConnectorKey: "finance", ProviderKey: "test", Status: "active", Config: validConfig,
		}
		result, err := service.ValidateIntegrationBinding(t.Context(), BindingValidationRequest{ConnectorKey: "finance", ConnectionKey: "missing-ref"}, principal)
		if err != nil || !bindingResultHasCode(result, "backend.integration.binding.secret_ref_required") {
			t.Fatalf("result = %#v err=%v", result, err)
		}
	})

	t.Run("referenced secret missing", func(t *testing.T) {
		service, repository := newBindingValidationService(bindingValidationConnector(), true)
		repository.connections["missing-secret"] = integrationmodel.IntegrationConnection{
			Key: "missing-secret", WorkspaceID: "workspace", ConnectorKey: "finance", ProviderKey: "test", Status: "active", Config: validConfig,
			SecretRefs: map[string]string{"api_key": "secret:missing"},
		}
		result, err := service.ValidateIntegrationBinding(t.Context(), BindingValidationRequest{ConnectorKey: "finance", ConnectionKey: "missing-secret"}, principal)
		if err != nil || !bindingResultHasCode(result, "backend.integration.secret.not_found") {
			t.Fatalf("result = %#v err=%v", result, err)
		}
	})
}

func TestBindingValidationHelpers(t *testing.T) {
	issue := BindingValidationIssue{FieldPath: "connection.config.url", ErrorCode: "backend.integration.config.invalid", Params: map[string]string{"field": "url"}}
	err := bindingIssueError(issue)
	if apperror.CodeOf(err) != issue.ErrorCode || apperror.ParamsOf(err)["field_path"] != issue.FieldPath || apperror.ParamsOf(err)["field"] != "url" {
		t.Fatalf("binding issue error = %v params=%#v", err, apperror.ParamsOf(err))
	}

	connector := bindingValidationConnector()
	if got := RequiredSecretRefNames(connector); !reflect.DeepEqual(got, []string{"api_key"}) {
		t.Fatalf("required secret refs = %#v", got)
	}
	if cloneStringMap(nil) != nil {
		t.Fatal("nil string map clone must remain nil")
	}
	source := map[string]string{"key": "value"}
	clone := cloneStringMap(source)
	clone["key"] = "changed"
	if source["key"] != "value" {
		t.Fatal("string map clone aliases source")
	}
}

func assertBindingValidationResult(t *testing.T, result BindingValidationResult, err error, valid bool, code string) {
	t.Helper()
	if err != nil || result.Valid != valid || !bindingResultHasCode(result, code) {
		t.Fatalf("result = %#v err=%v, want valid=%v code=%s", result, err, valid, code)
	}
}

func bindingResultHasCode(result BindingValidationResult, code string) bool {
	for _, issue := range result.Errors {
		if issue.ErrorCode == code {
			return true
		}
	}
	return false
}
