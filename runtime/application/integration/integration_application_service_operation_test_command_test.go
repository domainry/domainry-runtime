// Integration application service operation-command tests.
package integration

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type operationCommandEdgeRepository struct {
	*independentConfigRepository
	upsertConnectionErr error
	upsertSecretErr     error
}

func (r *operationCommandEdgeRepository) UpsertConnection(_ context.Context, _ string, value integrationmodel.IntegrationConnection) (integrationmodel.IntegrationConnection, error) {
	if r.upsertConnectionErr != nil {
		return integrationmodel.IntegrationConnection{}, r.upsertConnectionErr
	}
	r.connections[value.Key] = value
	return value, nil
}

func (r *operationCommandEdgeRepository) UpsertSecret(_ context.Context, _ string, value integrationmodel.IntegrationSecret) (integrationmodel.IntegrationSecret, error) {
	if r.upsertSecretErr != nil {
		return integrationmodel.IntegrationSecret{}, r.upsertSecretErr
	}
	r.secrets[value.Key] = value
	return value, nil
}

func TestConnectorOperationReturnsCallerCancellationBeforeRepositoryLookup(t *testing.T) {
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{}}})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin"}}, accessfixture.Bundle{Key: "admin", Permissions: []string{"workspace.admin"}})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := service.TestConnectorOperation(ctx, "missing", ConnectorOperationTestRequest{Operation: "test_connection", Confirm: true}, principal)
	if err != context.Canceled {
		t.Fatalf("expected caller cancellation, got %v", err)
	}
}

func TestConnectorOperationCommandGuardAndValidationEdges(t *testing.T) {
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{}}})
	principal := integrationManagementPrincipal(PermissionConnectionTest)
	if _, err := service.TestConnectorOperation(t.Context(), "missing", ConnectorOperationTestRequest{Confirm: true}, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization error=%v", err)
	}
	if _, err := service.TestConnectorOperation(t.Context(), "missing", ConnectorOperationTestRequest{Confirm: true}, integrationManagementPrincipal()); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("permission error=%v", err)
	}
	if _, err := service.TestConnectorOperation(t.Context(), "missing", ConnectorOperationTestRequest{}, principal); apperror.CodeOf(err) != "backend.integration.operation_test_confirmation_required" {
		t.Fatalf("confirmation error=%v", err)
	}
	if _, err := service.TestConnectorOperation(t.Context(), "missing", ConnectorOperationTestRequest{Confirm: true}, principal); apperror.CodeOf(err) != "backend.integration.connection_unavailable" {
		t.Fatalf("connection error=%v", err)
	}

	repository := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{
		"connection": {Key: "connection", WorkspaceID: "workspace", ConnectorKey: "connector", ProviderKey: "provider", Status: "configured"},
	}, secrets: map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{}}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "connector", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "provider"}}, Operations: []integrationmodel.ConnectorOperationSchema{{Key: "ping", SideEffect: "read"}}}}})
	validationErr := errors.New("validation failed")
	service = NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: registry, OperationInputValidator: func(string, integrationmodel.ConnectorOperationSchema, map[string]any) error { return validationErr }})
	if _, err := service.TestConnectorOperation(t.Context(), "connection", ConnectorOperationTestRequest{Operation: "missing", Confirm: true}, principal); err == nil {
		t.Fatal("missing operation accepted")
	}
	if _, err := service.TestConnectorOperation(t.Context(), "connection", ConnectorOperationTestRequest{Operation: "ping", Confirm: true}, principal); !errors.Is(err, validationErr) {
		t.Fatalf("input validation error=%v", err)
	}
}

func TestConnectorOperationCommandOutputEvidenceAndPersistenceEdges(t *testing.T) {
	repository := &operationCommandEdgeRepository{independentConfigRepository: &independentConfigRepository{
		connections: map[string]integrationmodel.IntegrationConnection{
			"connection": {Key: "connection", WorkspaceID: "workspace", ConnectorKey: "connector", ProviderKey: "provider", Status: "configured"},
			"test":       {Key: "test", WorkspaceID: "workspace", ConnectorKey: "connector", ProviderKey: "provider", Status: "configured", SecretRefs: map[string]string{"token": "secret:token"}},
		},
		secrets: map[string]integrationmodel.IntegrationSecret{"token": {Key: "token", WorkspaceID: "workspace", Status: "active", ValueRef: "material:token"}}, materials: map[string]string{"workspace:token": "secret"},
	}}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "connector", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "provider", SecretFields: []definitionmodel.FieldSchema{{Key: "token", Type: "text"}}}}, Operations: []integrationmodel.ConnectorOperationSchema{{Key: "ping", SideEffect: "read"}, {Key: "test_connection", SideEffect: "read"}}}}})
	outputErr := errors.New("output invalid")
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, DeliveryRepository: &independentDeliveryRepository{}, Registry: registry, OperationOutputValidator: func(string, integrationmodel.ConnectorOperationSchema, map[string]any) error { return outputErr }})
	registerTestProviderAdapter(service, "connector", "provider", lifecycleAdapter{})
	principal := integrationManagementPrincipal(PermissionConnectionTest)
	if _, err := service.TestConnectorOperation(t.Context(), "connection", ConnectorOperationTestRequest{Operation: "ping", Confirm: true}, principal); !errors.Is(err, outputErr) {
		t.Fatalf("output validation error=%v", err)
	}
	service.validateOperationOutput = func(string, integrationmodel.ConnectorOperationSchema, map[string]any) error { return nil }
	repository.upsertSecretErr = errors.New("evidence failed")
	if _, err := service.TestConnectorOperation(t.Context(), "test", ConnectorOperationTestRequest{Operation: "test_connection", Confirm: true}, principal); !errors.Is(err, repository.upsertSecretErr) {
		t.Fatalf("evidence error=%v", err)
	}
	repository.upsertSecretErr = nil
	repository.upsertConnectionErr = errors.New("connection status failed")
	if _, err := service.TestConnectorOperation(t.Context(), "connection", ConnectorOperationTestRequest{Operation: "ping", Confirm: true}, principal); !errors.Is(err, repository.upsertConnectionErr) {
		t.Fatalf("connection status error=%v", err)
	}
}
