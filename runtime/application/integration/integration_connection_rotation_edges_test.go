package integration

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type integrationRotationRepository struct {
	integrationrepository.IntegrationConfigRepository
	connections   []integrationmodel.IntegrationConnection
	secrets       []integrationmodel.IntegrationSecret
	connectionErr error
	secretErr     error
	upsertErr     error
	saved         integrationmodel.IntegrationConnection
}

func (r *integrationRotationRepository) ListConnections(context.Context, string) ([]integrationmodel.IntegrationConnection, error) {
	return append([]integrationmodel.IntegrationConnection(nil), r.connections...), r.connectionErr
}

func (r *integrationRotationRepository) ListSecrets(context.Context, string) ([]integrationmodel.IntegrationSecret, error) {
	return append([]integrationmodel.IntegrationSecret(nil), r.secrets...), r.secretErr
}

func (r *integrationRotationRepository) UpsertConnection(_ context.Context, _ string, connection integrationmodel.IntegrationConnection) (integrationmodel.IntegrationConnection, error) {
	r.saved = connection
	return connection, r.upsertErr
}

type integrationRotationAdapter struct{ validationErr error }

func (integrationRotationAdapter) Call(context.Context, integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	return integrationcontract.CallResult{}, nil
}

func (a integrationRotationAdapter) ValidateConfig(integrationmodel.IntegrationConnection) error {
	return a.validationErr
}

func integrationRotationPrincipal(permissions ...string) principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "admin"}}, accessfixture.Bundle{Permissions: permissions})
}

func integrationRotationRegistry(adapter integrationcontract.Adapter) *ConnectorRegistry {
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{
		Key: "connector", Provider: "multi", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "provider"}, {Key: "other"}},
	}}})
	if adapter != nil {
		registerTestRegistryProvider(registry, "connector", "provider", adapter)
	}
	return registry
}

func integrationRotationExisting() integrationmodel.IntegrationConnection {
	return integrationmodel.IntegrationConnection{Key: "connection", WorkspaceID: "workspace", ConnectorKey: "connector", ProviderKey: "provider", Name: "Old", Status: "active", Config: map[string]any{"old": true}, SecretRefs: map[string]string{"old": "env:OLD"}}
}

func TestRotateIntegrationConnectionGuardsAndDependencyFailures(t *testing.T) {
	admin := integrationRotationPrincipal(PermissionConnectionManage)
	repository := &integrationRotationRepository{connections: []integrationmodel.IntegrationConnection{integrationRotationExisting()}}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: integrationRotationRegistry(nil)})
	request := integrationmodel.IntegrationConnectionUpsertRequest{SecretRefs: map[string]string{"token": "env:TOKEN"}}

	if _, err := service.RotateIntegrationConnection(t.Context(), "connection", request, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace guard=%v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.RotateIntegrationConnection(cancelled, "connection", request, admin); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	if _, err := service.RotateIntegrationConnection(t.Context(), "connection", request, integrationRotationPrincipal()); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("permission guard=%v", err)
	}
	repository.connectionErr = errors.New("list failed")
	if _, err := service.RotateIntegrationConnection(t.Context(), "connection", request, admin); !errors.Is(err, repository.connectionErr) {
		t.Fatalf("connection list error=%v", err)
	}
	repository.connectionErr, repository.connections = nil, nil
	if _, err := service.RotateIntegrationConnection(t.Context(), "missing", request, admin); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("missing connection=%v", err)
	}
	repository.connections = []integrationmodel.IntegrationConnection{integrationRotationExisting()}
	service = NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: NewConnectorRegistry(integrationmodel.IntegrationSchema{})})
	if _, err := service.RotateIntegrationConnection(t.Context(), "connection", request, admin); apperror.CodeOf(err) != "backend.integration.connector.not_found" {
		t.Fatalf("missing connector=%v", err)
	}

	service = NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: integrationRotationRegistry(nil)})
	request.ProviderKey = "unsupported"
	if _, err := service.RotateIntegrationConnection(t.Context(), "connection", request, admin); apperror.CodeOf(err) != "backend.integration.connection.provider_unsupported" {
		t.Fatalf("unsupported provider=%v", err)
	}
	request.ProviderKey = "other"
	if _, err := service.RotateIntegrationConnection(t.Context(), "connection", request, admin); apperror.CodeOf(err) != "backend.integration.connection.provider_immutable" {
		t.Fatalf("provider mutation=%v", err)
	}
	request.ProviderKey, request.SecretRefs = "", map[string]string{"": "env:TOKEN"}
	if _, err := service.RotateIntegrationConnection(t.Context(), "connection", request, admin); apperror.CodeOf(err) != "backend.integration.secret_ref.invalid" {
		t.Fatalf("invalid secret ref=%v", err)
	}
	request.SecretRefs = map[string]string{"token": "secret:missing"}
	if _, err := service.RotateIntegrationConnection(t.Context(), "connection", request, admin); apperror.CodeOf(err) != "backend.integration.secret.not_found" {
		t.Fatalf("missing secret=%v", err)
	}
	repository.secrets = []integrationmodel.IntegrationSecret{{Key: "disabled", Status: "disabled"}}
	request.SecretRefs = map[string]string{"token": "secret:disabled"}
	if _, err := service.RotateIntegrationConnection(t.Context(), "connection", request, admin); apperror.CodeOf(err) != "backend.integration.secret.disabled" {
		t.Fatalf("disabled secret=%v", err)
	}
	request.SecretRefs = nil
	if _, err := service.RotateIntegrationConnection(t.Context(), "connection", request, admin); apperror.CodeOf(err) != "backend.integration.secret_ref.missing_rotation_refs" {
		t.Fatalf("missing rotation refs=%v", err)
	}
	request.SecretRefs, request.Status = map[string]string{"token": "env:TOKEN"}, "unknown"
	if _, err := service.RotateIntegrationConnection(t.Context(), "connection", request, admin); apperror.CodeOf(err) != "backend.integration.connection.invalid_status" {
		t.Fatalf("invalid status=%v", err)
	}
}

func TestRotateIntegrationConnectionValidatesPersistsAndAudits(t *testing.T) {
	admin := integrationRotationPrincipal(PermissionConnectionManage)
	repository := &integrationRotationRepository{connections: []integrationmodel.IntegrationConnection{integrationRotationExisting()}}
	adapter := &integrationRotationAdapter{validationErr: errors.New("provider.config_invalid")}
	registry := integrationRotationRegistry(adapter)
	var auditEvent string
	var auditBefore, auditAfter, auditMetadata map[string]any
	service := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository: repository,
		Registry:         registry,
		Audit: func(_ context.Context, event, _, _ string, _ principalmodel.Principal, _ string, before, after, metadata map[string]any) {
			auditEvent, auditBefore, auditAfter, auditMetadata = event, before, after, metadata
		},
	})
	config := map[string]any{"endpoint": "https://example.invalid"}
	request := integrationmodel.IntegrationConnectionUpsertRequest{Name: " Rotated ", Status: " verified ", Config: config, SecretRefs: map[string]string{"token": "env:TOKEN"}}
	if _, err := service.RotateIntegrationConnection(t.Context(), " connection ", request, admin); apperror.CodeOf(err) != "provider.config_invalid" {
		t.Fatalf("adapter validation error=%v", err)
	}
	adapter.validationErr = nil
	repository.upsertErr = errors.New("write failed")
	if _, err := service.RotateIntegrationConnection(t.Context(), "connection", request, admin); !errors.Is(err, repository.upsertErr) {
		t.Fatalf("upsert error=%v", err)
	}
	repository.upsertErr = nil
	saved, err := service.RotateIntegrationConnection(t.Context(), "connection", request, admin)
	if err != nil {
		t.Fatal(err)
	}
	config["endpoint"] = "mutated"
	if saved.ProviderKey != "provider" || saved.Status != "verified" || saved.Name != "Rotated" || saved.Config["endpoint"] != "https://example.invalid" || saved.SecretRefs["token"] != "env:TOKEN" {
		t.Fatalf("saved connection=%#v", saved)
	}
	if auditEvent != "integration_connection_rotated" || auditBefore["name"] != "Old" || auditAfter["name"] != "Rotated" || auditMetadata["workspace_id"] != "workspace" {
		t.Fatalf("audit event=%q before=%#v after=%#v metadata=%#v", auditEvent, auditBefore, auditAfter, auditMetadata)
	}
}

func TestRotateIntegrationConnectionPreservesOptionalDefaults(t *testing.T) {
	admin := integrationRotationPrincipal(PermissionConnectionManage)
	existing := integrationRotationExisting()
	existing.ProviderKey = ""
	repository := &integrationRotationRepository{connections: []integrationmodel.IntegrationConnection{existing}}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: integrationRotationRegistry(&integrationRotationAdapter{})})
	saved, err := service.RotateIntegrationConnection(t.Context(), "connection", integrationmodel.IntegrationConnectionUpsertRequest{ProviderKey: "provider", SecretRefs: map[string]string{"token": "env:TOKEN"}}, admin)
	if err != nil || saved.ProviderKey != "provider" || saved.Status != "configured" || saved.Name != "Old" || saved.Config["old"] != true {
		t.Fatalf("saved defaults=%#v err=%v", saved, err)
	}
}
