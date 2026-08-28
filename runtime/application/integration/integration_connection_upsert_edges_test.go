package integration

import (
	"context"
	"errors"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func integrationConnectionUpsertRegistry(lifecycle string, requiredSecret bool) *ConnectorRegistry {
	provider := integrationmodel.ConnectorProviderSchema{Key: "provider"}
	if requiredSecret {
		provider.SecretFields = []definitionmodel.FieldSchema{{Key: "token", Required: true, Config: map[string]any{"credential_kind": "api_key"}}}
	}
	return NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{
		Key: "connector", LifecycleStatus: lifecycle,
		Providers: []integrationmodel.ConnectorProviderSchema{provider, {Key: "other"}},
	}}})
}

func TestUpsertIntegrationConnectionGuardAndDependencyEdges(t *testing.T) {
	principal := integrationManagementPrincipal(PermissionConnectionManage)
	repository := &integrationRotationRepository{}
	registry := integrationConnectionUpsertRegistry("", false)
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: registry})
	request := integrationmodel.IntegrationConnectionUpsertRequest{ConnectorKey: "connector", ProviderKey: "provider", Status: "configured", Config: map[string]any{}}
	if _, err := service.UpsertIntegrationConnection(t.Context(), "connection", request, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization error=%v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.UpsertIntegrationConnection(cancelled, "connection", request, principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%v", err)
	}
	if _, err := service.UpsertIntegrationConnection(t.Context(), "connection", request, integrationManagementPrincipal()); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("permission error=%v", err)
	}
	if _, err := service.UpsertIntegrationConnection(t.Context(), "", integrationmodel.IntegrationConnectionUpsertRequest{}, principal); apperror.CodeOf(err) != "backend.integration.connection.missing_key" {
		t.Fatalf("missing key error=%v", err)
	}
	invalidKey := request
	invalidKey.Key = "Invalid Key"
	if _, err := service.UpsertIntegrationConnection(t.Context(), "", invalidKey, principal); apperror.CodeOf(err) != "backend.integration.connection.key_invalid" {
		t.Fatalf("invalid key error=%v", err)
	}
	missingConnector := request
	missingConnector.ConnectorKey = ""
	if _, err := service.UpsertIntegrationConnection(t.Context(), "connection", missingConnector, principal); apperror.CodeOf(err) != "backend.integration.connection.missing_connector" {
		t.Fatalf("missing connector error=%v", err)
	}
	if _, err := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: NewConnectorRegistry(integrationmodel.IntegrationSchema{})}).UpsertIntegrationConnection(t.Context(), "connection", request, principal); apperror.CodeOf(err) != "backend.integration.connector.not_found" {
		t.Fatalf("unknown connector error=%v", err)
	}
	deprecated := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: integrationConnectionUpsertRegistry("deprecated", false)})
	if _, err := deprecated.UpsertIntegrationConnection(t.Context(), "connection", request, principal); apperror.CodeOf(err) != "backend.integration.connector.reclassified" {
		t.Fatalf("reclassified connector error=%v", err)
	}
	draftFailure := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: registry, ConnectionDraftValidator: func(context.Context, string, integrationmodel.IntegrationConnectionUpsertRequest, principalmodel.Principal) error {
		return errIntegrationManagementTest
	}})
	if _, err := draftFailure.UpsertIntegrationConnection(t.Context(), "connection", request, principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("draft validation error=%v", err)
	}
	unsupportedProvider := request
	unsupportedProvider.ProviderKey = "missing"
	if _, err := service.UpsertIntegrationConnection(t.Context(), "connection", unsupportedProvider, principal); apperror.CodeOf(err) != "backend.integration.connection.provider_unsupported" {
		t.Fatalf("provider error=%v", err)
	}
	repository.connectionErr = errIntegrationManagementTest
	if _, err := service.UpsertIntegrationConnection(t.Context(), "connection", request, principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("connection list error=%v", err)
	}
	repository.connectionErr = nil
	if _, err := service.UpsertIntegrationConnection(t.Context(), "connection", request, principal); err != nil {
		t.Fatalf("catalog-defined connection should be configurable before adapter installation: %v", err)
	}
}

func TestUpsertIntegrationConnectionValidationPersistenceEdges(t *testing.T) {
	principal := integrationManagementPrincipal(PermissionConnectionManage)
	existing := integrationRotationExisting()
	repository := &integrationRotationRepository{connections: []integrationmodel.IntegrationConnection{existing}}
	registry := integrationConnectionUpsertRegistry("", false)
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: registry})
	request := integrationmodel.IntegrationConnectionUpsertRequest{ConnectorKey: "connector", ProviderKey: "provider", Status: "configured", Config: map[string]any{}}
	otherConnector := request
	otherConnector.ConnectorKey = "other"
	if _, err := service.UpsertIntegrationConnection(t.Context(), "connection", otherConnector, principal); apperror.CodeOf(err) != "backend.integration.connector.not_found" {
		t.Fatalf("other connector error=%v", err)
	}
	existing.ConnectorKey = "different"
	repository.connections[0] = existing
	if _, err := service.UpsertIntegrationConnection(t.Context(), "connection", request, principal); apperror.CodeOf(err) != "backend.integration.connection.connector_immutable" {
		t.Fatalf("connector immutable error=%v", err)
	}
	existing = integrationRotationExisting()
	repository.connections[0] = existing
	providerChange := request
	providerChange.ProviderKey = "other"
	if _, err := service.UpsertIntegrationConnection(t.Context(), "connection", providerChange, principal); apperror.CodeOf(err) != "backend.integration.connection.provider_immutable" {
		t.Fatalf("provider immutable error=%v", err)
	}
	existing.ProviderKey = ""
	repository.connections[0] = existing
	if saved, err := service.UpsertIntegrationConnection(t.Context(), "connection", providerChange, principal); err != nil || saved.ProviderKey != "other" {
		t.Fatalf("defaulted existing provider saved=%#v err=%v", saved, err)
	}
	repository.connections[0] = integrationRotationExisting()
	badRefs := request
	badRefs.SecretRefs = map[string]string{"token": "literal"}
	if _, err := service.UpsertIntegrationConnection(t.Context(), "connection", badRefs, principal); apperror.CodeOf(err) != "backend.integration.secret_ref.must_be_reference" {
		t.Fatalf("secret reference error=%v", err)
	}
	badStatus := request
	badStatus.Status = "invalid"
	if _, err := service.UpsertIntegrationConnection(t.Context(), "connection", badStatus, principal); apperror.CodeOf(err) != "backend.integration.connection.invalid_status" {
		t.Fatalf("status error=%v", err)
	}
	required := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: integrationConnectionUpsertRegistry("", true)})
	if _, err := required.UpsertIntegrationConnection(t.Context(), "connection", request, principal); apperror.CodeOf(err) != "backend.integration.connection.provider_secret_required" {
		t.Fatalf("provider secret error=%v", err)
	}
	prepareFailure := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: registry, ConnectionConfigPreparer: func(integrationmodel.ConnectorSchema, string, string, map[string]any) (map[string]any, error) {
		return nil, errIntegrationManagementTest
	}})
	if _, err := prepareFailure.UpsertIntegrationConnection(t.Context(), "connection", request, principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("config preparation error=%v", err)
	}
	adapter := &integrationRotationAdapter{validationErr: errIntegrationManagementTest}
	registerTestRegistryProvider(registry, "connector", "provider", adapter)
	if _, err := service.UpsertIntegrationConnection(t.Context(), "connection", request, principal); err == nil {
		t.Fatal("adapter validation error missing")
	}
	adapter.validationErr = nil
	repository.upsertErr = errIntegrationManagementTest
	if _, err := service.UpsertIntegrationConnection(t.Context(), "connection", request, principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("upsert error=%v", err)
	}
	repository.upsertErr = nil
	if saved, err := service.UpsertIntegrationConnection(t.Context(), "connection", request, principal); err != nil || saved.Key != "connection" {
		t.Fatalf("saved=%#v err=%v", saved, err)
	}
}
