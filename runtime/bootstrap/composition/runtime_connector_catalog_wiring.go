package composition

import (
	"sync"

	appschemaservice "github.com/domainry/domainry-runtime/runtime/domain/appschema/service"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

// runtimeConnectorCatalog is the application-authored connector requirement
// catalog. It contains no Provider adapters or Integration owner state.
type runtimeConnectorCatalog struct {
	mu     sync.RWMutex
	schema integrationmodel.IntegrationSchema
}

func newRuntimeConnectorCatalog(schema integrationmodel.IntegrationSchema) *runtimeConnectorCatalog {
	return &runtimeConnectorCatalog{schema: appschemaservice.CloneIntegrationSchema(schema)}
}

func (c *runtimeConnectorCatalog) Schema() integrationmodel.IntegrationSchema {
	if c == nil {
		return integrationmodel.IntegrationSchema{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return appschemaservice.CloneIntegrationSchema(c.schema)
}

func (c *runtimeConnectorCatalog) ReplaceSchema(schema integrationmodel.IntegrationSchema) {
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
