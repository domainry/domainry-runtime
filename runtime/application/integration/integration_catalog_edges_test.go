package integration

import (
	"context"
	"errors"
	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestIntegrationConnectorCatalogReadinessEdges(t *testing.T) {
	repository := newIntegrationWebhookLifecycleRepository()
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{
		{Key: "disabled", LifecycleStatus: "retired", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "provider"}}},
		{Key: "multi", Provider: "multi", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "ready"}, {Key: "available"}}},
		{Key: "single", Provider: "single", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "single"}}},
	}})
	registerTestRegistryProvider(registry, "multi", "ready", integrationRotationAdapter{})
	registerTestRegistryProvider(registry, "single", "single", integrationRotationAdapter{})
	repository.connections["multi"] = integrationmodel.IntegrationConnection{Key: "multi", WorkspaceID: "workspace", ConnectorKey: "multi", ProviderKey: "ready", Status: "verified"}
	repository.connections["single"] = integrationmodel.IntegrationConnection{Key: "single", WorkspaceID: "workspace", ConnectorKey: "single", ProviderKey: "single", Status: "active"}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: registry})
	if _, err := service.IntegrationConnectorCatalog(t.Context(), principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization guard=%v", err)
	}
	if _, err := service.IntegrationConnectorCatalog(t.Context(), integrationRotationPrincipal()); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("permission guard=%v", err)
	}
	principal := integrationRotationPrincipal(PermissionCatalogView)
	repository.connectionErr = errors.New("connection list failed")
	if _, err := service.IntegrationConnectorCatalog(t.Context(), principal); !errors.Is(err, repository.connectionErr) {
		t.Fatalf("connection list error=%v", err)
	}
	repository.connectionErr = nil
	catalog, err := service.IntegrationConnectorCatalog(t.Context(), principal)
	if err != nil || len(catalog) != 3 {
		t.Fatalf("catalog=%#v err=%v", catalog, err)
	}
	byKey := map[string]integrationmodel.ConnectorSchema{}
	for _, connector := range catalog {
		byKey[connector.Key] = connector
	}
	if disabled := byKey["disabled"]; disabled.Readiness != "disabled" || disabled.Providers[0].Readiness != "disabled" || disabled.AdapterReady || disabled.ConnectionReady {
		t.Fatalf("disabled=%#v", disabled)
	}
	multi := byKey["multi"]
	if multi.Readiness != "connection_ready" || !multi.AdapterReady || !multi.ConnectionReady || multi.Providers[0].Readiness != "connection_ready" || multi.Providers[1].Readiness != "provider_available" {
		t.Fatalf("multi=%#v", multi)
	}
	single := byKey["single"]
	if single.Readiness != "connection_ready" || !single.AdapterReady || !single.ConnectionReady {
		t.Fatalf("single=%#v", single)
	}
	registerTestRegistryProvider(registry, "multi", "available", integrationRotationAdapter{validationErr: errors.New("invalid config")})
	repository.connections["invalid"] = integrationmodel.IntegrationConnection{Key: "invalid", WorkspaceID: "workspace", ConnectorKey: "multi", ProviderKey: "available", Status: "active"}
	if _, err := service.IntegrationConnectorCatalog(t.Context(), principal); err != nil {
		t.Fatalf("invalid-config catalog error=%v", err)
	}
	service.normalizeConnection = func(_ context.Context, _ integrationmodel.IntegrationConnection) (integrationmodel.IntegrationConnection, error) {
		return integrationmodel.IntegrationConnection{}, errIntegrationManagementTest
	}
	if _, err := service.IntegrationConnectorCatalog(t.Context(), principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("normalization error=%v", err)
	}
}
