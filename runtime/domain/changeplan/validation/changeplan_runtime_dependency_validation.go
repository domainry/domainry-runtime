package validation

import changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"

import (
	"fmt"
	"strings"
)

func (validator *businessChangePlanValidator) validateRuntimeDependencies(itemIndex int, item changeplanmodel.BusinessSystemChangeItem) {
	for dependencyIndex, dependency := range item.Dependencies {
		path := fmt.Sprintf("items[%d].dependencies[%d]", itemIndex, dependencyIndex)
		switch businessReferenceResourceType(dependency.ResourceType) {
		case "plugin":
			if !validator.pluginReady(dependency.ResourceKey) {
				validator.issue(item.ItemID, path, "backend.integration.connector.adapter_not_ready", "plugin", dependency.ResourceKey)
			}
		case "connector":
			connector, exists := validator.connector(dependency.ResourceKey)
			if !exists || !connector.DefinitionReady {
				validator.issue(item.ItemID, path, "backend.integration.connector.definition_not_ready", "connector", dependency.ResourceKey)
			} else if !connector.AdapterReady {
				validator.issue(item.ItemID, path, "backend.integration.connector.adapter_not_ready", "connector", dependency.ResourceKey)
			}
		case "connector_operation":
			if !validator.connectorOperationReady(dependency.ResourceKey) {
				validator.issue(item.ItemID, path, "backend.integration.connector.operation_not_found", "actual", dependency.ResourceKey)
			}
		case "connection":
			if !validator.connectionReady(dependency.ResourceKey) {
				validator.issue(item.ItemID, path, "backend.integration.binding.connection_required", "connection", dependency.ResourceKey)
			}
		}
	}
}

func (validator *businessChangePlanValidator) connector(key string) (connectorSnapshot, bool) {
	for _, connector := range validator.snapshot.RuntimeState.Connectors {
		if connector.Key == key {
			return connectorSnapshot{DefinitionReady: connector.DefinitionReady, AdapterReady: connector.AdapterReady, Source: connector.Source}, true
		}
	}
	return connectorSnapshot{}, false
}

type connectorSnapshot struct {
	DefinitionReady bool
	AdapterReady    bool
	Source          string
}

func (validator *businessChangePlanValidator) pluginReady(key string) bool {
	key = strings.TrimPrefix(strings.TrimSpace(key), "plugin:")
	for _, connector := range validator.snapshot.RuntimeState.Connectors {
		if strings.TrimPrefix(connector.Source, "plugin:") == key && connector.DefinitionReady && connector.AdapterReady {
			return true
		}
	}
	return false
}

func (validator *businessChangePlanValidator) connectorOperationReady(key string) bool {
	connectorKey, operationKey, ok := strings.Cut(key, ".")
	if !ok {
		return false
	}
	for _, connector := range validator.snapshot.RuntimeState.Connectors {
		if connector.Key != connectorKey || !connector.DefinitionReady || !connector.AdapterReady {
			continue
		}
		for _, operation := range connector.Operations {
			if operation.Key == operationKey {
				return true
			}
		}
	}
	return false
}

func (validator *businessChangePlanValidator) connectionReady(key string) bool {
	for _, connection := range validator.snapshot.RuntimeState.Connections {
		if connection.Key == key {
			return connection.Ready
		}
	}
	return false
}
