package composition

import (
	"encoding/json"
	"fmt"

	integrationsdk "github.com/domainry/domainry-integration-sdk"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func IntegrationConnectionRequirements(connections []integrationmodel.ConnectionSchema) []integrationsdk.ConnectionRequirement {
	result := make([]integrationsdk.ConnectionRequirement, 0, len(connections))
	for _, connection := range connections {
		if connection.Key == "" || connection.ConnectorKey == "" {
			continue
		}
		config, err := json.Marshal(connection.Config)
		if err != nil {
			panic(fmt.Errorf("encode manifest Integration connection %q: %w", connection.Key, err))
		}
		result = append(result, integrationsdk.ConnectionRequirement{Key: connection.Key, WorkspaceID: principalmodel.InstallationWorkspaceID, ConnectorKey: connection.ConnectorKey, ProviderKey: connection.ProviderKey, Name: connection.Name, Status: connection.Status, Config: config})
	}
	return result
}
