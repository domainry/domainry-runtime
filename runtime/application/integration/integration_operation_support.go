package integration

import (
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	"strings"
)

func (s *IntegrationApplicationService) ConnectorDefinition(connectorKey string) (integrationmodel.ConnectorSchema, bool) {
	connectorKey = strings.TrimSpace(connectorKey)
	for _, connector := range s.registry.Schema().Connectors {
		if strings.TrimSpace(connector.Key) == connectorKey {
			return connector, true
		}
	}
	return integrationmodel.ConnectorSchema{}, false
}

func (s *IntegrationApplicationService) ConnectorExists(connectorKey string) bool {
	_, ok := s.ConnectorDefinition(connectorKey)
	return ok
}

func (s *IntegrationApplicationService) ConnectorAdapterReady(connector integrationmodel.ConnectorSchema) bool {
	return s.registry.AdapterReady(connector)
}

func (s *IntegrationApplicationService) ProviderSupportsIntegrationOperation(connection integrationmodel.IntegrationConnection, operation string) bool {
	for _, connector := range s.registry.Schema().Connectors {
		if connector.Key != connection.ConnectorKey {
			continue
		}
		for _, provider := range connector.Providers {
			if provider.Key != connection.ProviderKey {
				continue
			}
			if len(provider.OperationKeys) == 0 {
				return true
			}
			for _, key := range provider.OperationKeys {
				if key == operation {
					return true
				}
			}
			return false
		}
	}
	return false
}

func (s *IntegrationApplicationService) IntegrationOperation(connection integrationmodel.IntegrationConnection, operationKey string) (integrationmodel.ConnectorOperationSchema, error) {
	if connector, ok := s.ConnectorDefinition(connection.ConnectorKey); ok {
		for index := range connector.Operations {
			if strings.TrimSpace(connector.Operations[index].Key) == strings.TrimSpace(operationKey) {
				return connector.Operations[index], nil
			}
		}
		return integrationmodel.ConnectorOperationSchema{}, badRequest("backend.automation.connector_operation_not_found", "connector", connection.ConnectorKey, "operation", operationKey)
	}
	return integrationmodel.ConnectorOperationSchema{}, notFound("backend.integration.connector.not_found")
}
