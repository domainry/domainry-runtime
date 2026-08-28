package integration

import (
	"github.com/domainry/domainry-connector-sdk"
	connectortest "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit/connectors"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func registerTestProviderAdapter(service *IntegrationApplicationService, connectorKey, providerKey string, adapter integrationcontract.Adapter) {
	registry, ok := service.registry.(*ConnectorRegistry)
	if !ok {
		panic("integration test provider registration requires ConnectorRegistry")
	}
	registerTestRegistryProvider(registry, connectorKey, providerKey, adapter)
}

func registerTestRegistryProvider(registry *ConnectorRegistry, connectorKey, providerKey string, adapter integrationcontract.Adapter) {
	schema := registry.Schema()
	operations := []integrationmodel.ConnectorOperationSchema(nil)
	providerSchema := integrationmodel.ConnectorProviderSchema{Key: providerKey}
	for _, connector := range schema.Connectors {
		if connector.Key == connectorKey {
			operations = append(operations, connector.Operations...)
			for _, candidate := range connector.Providers {
				if candidate.Key == providerKey {
					providerSchema = candidate
					break
				}
			}
			break
		}
	}
	provider := connectortest.Provider(connectorKey, providerKey, adapter, operations, providerSchema)
	providers := make([]connector.Adapter, 0, len(registry.connectorProviders.Providers())+1)
	for _, existing := range registry.connectorProviders.Providers() {
		descriptor := existing.Descriptor()
		if descriptor.ConnectorKey == connectorKey && descriptor.ProviderKey == providerKey {
			continue
		}
		providers = append(providers, existing)
	}
	providers = append(providers, provider)
	registry.connectorProviders = connectortest.Registry(providers...)
	registry.ReplaceSchema(schema)
}
