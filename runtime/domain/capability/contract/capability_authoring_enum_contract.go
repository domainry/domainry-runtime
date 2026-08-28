package contract

import integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

func RuntimeConnectorTypes() []string   { return integrationmodel.RuntimeConnectorTypes() }
func RuntimeConnectorMethods() []string { return integrationmodel.RuntimeConnectorMethods() }
func RuntimeConnectorExecutionModes() []string {
	return integrationmodel.RuntimeConnectorExecutionModes()
}
func RuntimeConnectorSideEffects() []string { return integrationmodel.RuntimeConnectorSideEffects() }
func RuntimeConnectorProtocolFieldTypes() []string {
	return integrationmodel.RuntimeConnectorProtocolFieldTypes()
}

func RuntimeIntegrationConnectionStatuses() []string {
	return integrationmodel.RuntimeIntegrationConnectionStatuses()
}

func RuntimeIntegrationOutboxStatuses() []string {
	return integrationmodel.RuntimeIntegrationOutboxStatuses()
}
