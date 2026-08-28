package integration

import (
	"testing"

	"github.com/domainry/domainry-connector-sdk"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestRegisterProviderIntegrationOutboxSendersRemainingBoundaries(t *testing.T) {
	(*IntegrationApplicationService)(nil).RegisterProviderIntegrationOutboxSenders()
	empty := NewIntegrationApplicationService(ApplicationDependencies{})
	empty.RegisterProviderIntegrationOutboxSenders()

	provider := &publicBridgeProvider{descriptor: publicBridgeDescriptor(false)}
	providers := connector.NewRegistry()
	if err := providers.Register(provider); err != nil {
		t.Fatal(err)
	}
	providers.Freeze()
	registry := NewConnectorRegistryWithProviders(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{
		{Key: "crm", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "acme"}}},
		{Key: "missing", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "absent"}}},
	}}, providers)
	service := NewIntegrationApplicationService(ApplicationDependencies{Registry: registry})
	service.RegisterProviderIntegrationOutboxSenders()
	if _, ok := service.IntegrationOutboxSender("crm"); !ok {
		t.Fatal("ready provider sender was not registered")
	}
	if _, ok := service.IntegrationOutboxSender("missing"); ok {
		t.Fatal("missing provider sender was registered")
	}
}
