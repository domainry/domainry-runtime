package composition

import (
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	"sync"

	appschemaservice "github.com/domainry/domainry-runtime/runtime/domain/appschema/service"
)

// runtimeConnectorCatalog is a transient read projection of the
// Connectors-owned catalog received through Integration. It contains no
// Provider adapters or Integration owner state and is never persisted.
type runtimeConnectorCatalog struct {
	mu     sync.RWMutex
	schema connectormodel.IntegrationSchema
}

func newRuntimeConnectorCatalog(schema connectormodel.IntegrationSchema) *runtimeConnectorCatalog {
	return &runtimeConnectorCatalog{schema: appschemaservice.CloneIntegrationSchema(schema)}
}

func (c *runtimeConnectorCatalog) Schema() connectormodel.IntegrationSchema {
	if c == nil {
		return connectormodel.IntegrationSchema{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return appschemaservice.CloneIntegrationSchema(c.schema)
}

func (c *runtimeConnectorCatalog) ReplaceSchema(schema connectormodel.IntegrationSchema) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.schema = appschemaservice.CloneIntegrationSchema(schema)
	c.mu.Unlock()
}

func (c *runtimeConnectorCatalog) ConnectorDeclared(key string) bool {
	for _, connector := range c.Schema().Connectors {
		if connector.Key == key {
			return true
		}
	}
	return false
}
