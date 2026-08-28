package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestIntegrationQueryAuthorizationAndFailureEdges(t *testing.T) {
	config := &integrationManagementConfigRepo{connections: []integrationmodel.IntegrationConnection{{Key: "connection"}}}
	events := &integrationManagementEventRepo{event: integrationmodel.IntegrationEvent{ID: "event"}, found: true}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: config, EventRepository: events})
	unknown := principalmodel.Principal{}
	for index, query := range []func() error{
		func() error { _, err := service.ListIntegrationSecrets(t.Context(), unknown); return err },
		func() error { _, err := service.ListIntegrationAPIKeys(t.Context(), unknown); return err },
		func() error { _, err := service.InspectIntegrationEvent(t.Context(), "event", unknown); return err },
		func() error {
			_, err := service.ListIntegrationWebhookSubscriptions(t.Context(), "", "", "", 1, unknown)
			return err
		},
		func() error { _, err := service.ListIntegrationExternalIdentities(t.Context(), unknown); return err },
	} {
		if apperror.CodeOf(query()) != "backend.workspace_scope_required" {
			t.Fatalf("authorization query %d", index)
		}
	}
	admin := integrationManagementPrincipal("workspace.admin", PermissionCatalogView, PermissionAuditView)
	if _, err := service.ListIntegrationEvents(t.Context(), "", "", 0, admin); err != nil || events.lastLimit != 100 {
		t.Fatalf("default event limit=%d err=%v", events.lastLimit, err)
	}
	if _, err := service.ListIntegrationEvents(t.Context(), "", "", 25, admin); err != nil || events.lastLimit != 25 {
		t.Fatalf("explicit event limit=%d err=%v", events.lastLimit, err)
	}
	if _, err := service.ListIntegrationWebhookSubscriptions(t.Context(), "", "", "", 0, admin); err != nil {
		t.Fatalf("default webhook limit error=%v", err)
	}
	if _, err := service.ListIntegrationWebhookSubscriptions(t.Context(), "", "", "", 501, admin); err != nil {
		t.Fatalf("bounded webhook limit error=%v", err)
	}
	if _, err := service.ListIntegrationWebhookSubscriptions(t.Context(), "", "", "", 1, admin); err != nil {
		t.Fatalf("explicit webhook limit error=%v", err)
	}
	events.err = errIntegrationManagementTest
	if _, err := service.InspectIntegrationEvent(t.Context(), "event", admin); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("inspect repository error=%v", err)
	}
	events.err, events.found = nil, false
	if _, err := service.InspectIntegrationEvent(t.Context(), "event", admin); apperror.CodeOf(err) != "backend.integration.event.not_found" {
		t.Fatalf("inspect missing error=%v", err)
	}
	if _, err := service.ListIntegrationConnections(t.Context(), integrationManagementPrincipal()); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("connection permission error=%v", err)
	}
	config.err = errIntegrationManagementTest
	if _, err := service.ListIntegrationConnections(t.Context(), admin); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("connection list error=%v", err)
	}
	config.err = nil
	service.normalizeConnection = func(context.Context, integrationmodel.IntegrationConnection) (integrationmodel.IntegrationConnection, error) {
		return integrationmodel.IntegrationConnection{}, errIntegrationManagementTest
	}
	if _, err := service.ListIntegrationConnections(t.Context(), admin); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("connection normalization error=%v", err)
	}
}

func TestIntegrationBestEffortLookupEdges(t *testing.T) {
	config := &integrationManagementConfigRepo{connections: []integrationmodel.IntegrationConnection{{Key: "connection"}}}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: config})
	if _, ok := service.LookupConnection(t.Context(), "connection", ""); ok {
		t.Fatal("connection lookup accepted missing workspace")
	}
	if _, ok := service.LookupSecret(t.Context(), "secret", ""); ok {
		t.Fatal("secret lookup accepted missing workspace")
	}
	if connection, ok := service.LookupConnection(t.Context(), " connection ", " workspace "); !ok || connection.Key != "connection" {
		t.Fatalf("connection=%#v ok=%v", connection, ok)
	}
	if secret, ok := service.LookupSecret(t.Context(), " secret ", " workspace "); !ok || secret.Key != "secret" {
		t.Fatalf("secret=%#v ok=%v", secret, ok)
	}
	config.err = errIntegrationManagementTest
	if _, ok := service.LookupConnection(t.Context(), "connection", "workspace"); ok {
		t.Fatal("failed connection lookup succeeded")
	}
	if _, ok := service.LookupSecret(t.Context(), "secret", "workspace"); ok {
		t.Fatal("failed secret lookup succeeded")
	}
}

func TestIntegrationOperationSupportEdges(t *testing.T) {
	schema := integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{
		{Key: "legacy", Operations: []integrationmodel.ConnectorOperationSchema{{Key: "send"}}},
		{Key: "open", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "provider"}}},
		{Key: "closed", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "provider", OperationKeys: []string{"send"}}}, Operations: []integrationmodel.ConnectorOperationSchema{{Key: " send "}}},
	}}
	service := NewIntegrationApplicationService(ApplicationDependencies{Registry: NewConnectorRegistry(schema)})
	if service.ProviderSupportsIntegrationOperation(integrationmodel.IntegrationConnection{ConnectorKey: "legacy"}, "anything") {
		t.Fatal("provider-less connector accepted operation")
	}
	if !service.ProviderSupportsIntegrationOperation(integrationmodel.IntegrationConnection{ConnectorKey: "open", ProviderKey: "provider"}, "anything") {
		t.Fatal("open provider rejected operation")
	}
	if !service.ProviderSupportsIntegrationOperation(integrationmodel.IntegrationConnection{ConnectorKey: "closed", ProviderKey: "provider"}, "send") {
		t.Fatal("closed provider rejected declared operation")
	}
	for _, connection := range []integrationmodel.IntegrationConnection{{ConnectorKey: "closed", ProviderKey: "provider"}, {ConnectorKey: "closed", ProviderKey: "missing"}, {ConnectorKey: "missing"}} {
		if service.ProviderSupportsIntegrationOperation(connection, "missing") {
			t.Fatalf("unsupported operation accepted: %#v", connection)
		}
	}
	if operation, err := service.IntegrationOperation(integrationmodel.IntegrationConnection{ConnectorKey: "closed"}, "send"); err != nil || operation.Key != " send " {
		t.Fatalf("operation=%#v err=%v", operation, err)
	}
	if _, err := service.IntegrationOperation(integrationmodel.IntegrationConnection{ConnectorKey: "closed"}, "missing"); apperror.CodeOf(err) != "backend.automation.connector_operation_not_found" {
		t.Fatalf("missing operation error=%v", err)
	}
	if _, err := service.IntegrationOperation(integrationmodel.IntegrationConnection{ConnectorKey: "missing"}, "send"); apperror.CodeOf(err) != "backend.integration.connector.not_found" {
		t.Fatalf("missing connector error=%v", err)
	}
}
