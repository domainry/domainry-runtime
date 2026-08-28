package integration

import (
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestMigratePersistedConnectionProviderEdges(t *testing.T) {
	connector := integrationmodel.ConnectorSchema{Key: "connector", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "provider"}}}
	if provider, err := ResolveConnectorProvider(connector, "provider"); err != nil || provider != "provider" {
		t.Fatalf("resolved provider=%q err=%v", provider, err)
	}
	if keys := ConnectorProviderKeys(connector); len(keys) != 1 || keys[0] != "provider" {
		t.Fatalf("provider keys=%v", keys)
	}
	repository := &connectionResolutionRepo{}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{connector}})})
	connection := integrationmodel.IntegrationConnection{Key: "connection", WorkspaceID: "workspace", ConnectorKey: "missing", Config: map[string]any{}}
	if _, err := service.MigratePersistedConnectionProvider(t.Context(), connection); apperror.CodeOf(err) != "backend.integration.connector.not_found" {
		t.Fatalf("missing connector error=%v", err)
	}
	connection.ConnectorKey = "connector"
	connection.ProviderKey = "missing"
	if _, err := service.MigratePersistedConnectionProvider(t.Context(), connection); apperror.CodeOf(err) != "backend.integration.connection.provider_unsupported" {
		t.Fatalf("missing provider error=%v", err)
	}
	connection.ProviderKey, connection.Status = "provider", "invalid"
	if _, err := service.MigratePersistedConnectionProvider(t.Context(), connection); apperror.CodeOf(err) != "backend.integration.connection.invalid_status" {
		t.Fatalf("invalid status error=%v", err)
	}
	connection.Status = "configured"
	if migrated, err := service.MigratePersistedConnectionProvider(t.Context(), connection); err != nil || migrated.ProviderKey != "provider" {
		t.Fatalf("unchanged migration=%#v err=%v", migrated, err)
	}
	connection.ProviderKey, connection.Status, connection.Config = "", "rotating", map[string]any{"value": true}
	repository.upsertErr = errIntegrationManagementTest
	if _, err := service.MigratePersistedConnectionProvider(t.Context(), connection); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("migration upsert error=%v", err)
	}
	repository.upsertErr = nil
	migrated, err := service.MigratePersistedConnectionProvider(t.Context(), connection)
	if err != nil || migrated.ProviderKey != "provider" || migrated.Status != "configured" || migrated.Config["value"] != true {
		t.Fatalf("migrated=%#v err=%v", migrated, err)
	}
	connection.Config = map[string]any{"provider": "provider"}
	if _, err := service.MigratePersistedConnectionProvider(t.Context(), connection); apperror.CodeOf(err) != "backend.integration.connection.provider_in_config_forbidden" {
		t.Fatalf("legacy config provider error=%v", err)
	}
}
