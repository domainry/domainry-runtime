package integration

import integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
import integrationprojection "github.com/domainry/domainry-runtime/runtime/domain/integration/projection"

import (
	"github.com/domainry/domainry-connector-sdk"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationpolicy "github.com/domainry/domainry-runtime/runtime/domain/integration/policy"

	"context"
	"sort"
	"strings"
	"sync"
)

type Registry interface {
	Schema() integrationmodel.IntegrationSchema
	ReplaceSchema(integrationmodel.IntegrationSchema)
	RegisterEventHandler(string, EventHandler)
	RegisterOutboxSender(string, OutboxSender)
	MergeConnectors([]integrationmodel.ConnectorSchema)
	Catalog() ([]integrationmodel.ConnectorSchema, map[string]bool)
	ProviderAdapter(string, string) (integrationcontract.Adapter, bool)
	EventHandler(string) (EventHandler, bool)
	OutboxSender(string) (OutboxSender, bool)
	HasEventHandlers() bool
	HasEventWork() bool
	HasOutboxSenders() bool
	AdapterReady(integrationmodel.ConnectorSchema) bool
	AcquireCredentialLease(context.Context, string) (func(), error)
}

type ConnectorRegistry struct {
	mu                 sync.RWMutex
	schema             integrationmodel.IntegrationSchema
	catalogConnectors  map[string]integrationmodel.ConnectorSchema
	connectorProviders *connector.Registry
	eventHandlers      map[string]EventHandler
	outboxSenders      map[string]OutboxSender
	leaseMu            sync.Mutex
	credentialLeases   map[string]chan struct{}
}

func NewConnectorRegistry(schema integrationmodel.IntegrationSchema) *ConnectorRegistry {
	return newConnectorRegistry(schema, nil)
}

func NewConnectorRegistryWithProviders(schema integrationmodel.IntegrationSchema, connectorProviders *connector.Registry) *ConnectorRegistry {
	return newConnectorRegistry(schema, connectorProviders)
}

func newConnectorRegistry(schema integrationmodel.IntegrationSchema, connectorProviders *connector.Registry) *ConnectorRegistry {
	if connectorProviders == nil {
		connectorProviders = connector.NewRegistry()
		connectorProviders.Freeze()
	} else if !connectorProviders.Frozen() {
		panic("integration ConnectorRegistry requires a frozen connector Registry")
	}
	registry := &ConnectorRegistry{
		catalogConnectors: map[string]integrationmodel.ConnectorSchema{}, connectorProviders: connectorProviders,
		eventHandlers: map[string]EventHandler{}, outboxSenders: map[string]OutboxSender{},
		credentialLeases: map[string]chan struct{}{},
	}
	registry.ReplaceSchema(schema)
	return registry
}

func (r *ConnectorRegistry) AcquireCredentialLease(ctx context.Context, key string) (func(), error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return noOpCredentialLeaseRelease, nil
	}
	r.leaseMu.Lock()
	lease := r.credentialLeases[key]
	if lease == nil {
		lease = make(chan struct{}, 1)
		lease <- struct{}{}
		r.credentialLeases[key] = lease
	}
	r.leaseMu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-lease:
		var once sync.Once
		return func() { once.Do(func() { lease <- struct{}{} }) }, nil
	}
}

func noOpCredentialLeaseRelease() {}

func (r *ConnectorRegistry) ReplaceSchema(schema integrationmodel.IntegrationSchema) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.schema = cloneIntegrationSchema(schema)
	for connectorIndex := range r.schema.Connectors {
		connector := &r.schema.Connectors[connectorIndex]
		for providerIndex := range connector.Providers {
			if provider, ok := r.providerSchemaLocked(connector.Key, connector.Providers[providerIndex].Key); ok {
				connector.Providers[providerIndex] = integrationprojection.IntegrationMergeConnectorProviderSchema(connector.Providers[providerIndex], provider)
			}
		}
	}
}

func (r *ConnectorRegistry) Schema() integrationmodel.IntegrationSchema {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.effectiveSchemaLocked()
}

func (r *ConnectorRegistry) MergeConnectors(definitions []integrationmodel.ConnectorSchema) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, connector := range definitions {
		key := strings.TrimSpace(connector.Key)
		if key == "" {
			continue
		}
		connector.DefinitionReady = integrationpolicy.IntegrationConnectorDefinitionReady(connector)
		for index := range connector.Providers {
			if provider, ok := r.providerSchemaLocked(key, connector.Providers[index].Key); ok {
				connector.Providers[index] = integrationprojection.IntegrationMergeConnectorProviderSchema(connector.Providers[index], provider)
			}
		}
		r.catalogConnectors[key] = connector
	}
}

func (r *ConnectorRegistry) effectiveSchemaLocked() integrationmodel.IntegrationSchema {
	schema := cloneIntegrationSchema(r.schema)
	byKey := make(map[string]integrationmodel.ConnectorSchema, len(schema.Connectors)+len(r.catalogConnectors))
	for _, connector := range schema.Connectors {
		if key := strings.TrimSpace(connector.Key); key != "" {
			byKey[key] = connector
		}
	}
	for key, connector := range r.catalogConnectors {
		byKey[key] = connector
	}
	schema.Connectors = schema.Connectors[:0]
	for _, connector := range byKey {
		schema.Connectors = append(schema.Connectors, connector)
	}
	sort.Slice(schema.Connectors, func(i, j int) bool { return schema.Connectors[i].Key < schema.Connectors[j].Key })
	return schema
}

func (r *ConnectorRegistry) Catalog() ([]integrationmodel.ConnectorSchema, map[string]bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	connectors := r.effectiveSchemaLocked().Connectors
	adapters := make(map[string]bool, len(r.connectorProviders.Descriptors()))
	for _, descriptor := range r.connectorProviders.Descriptors() {
		adapters[integrationpolicy.IntegrationProviderIdentity(descriptor.ConnectorKey, descriptor.ProviderKey)] = true
	}
	return connectors, adapters
}

func (r *ConnectorRegistry) providerSchemaLocked(connectorKey, providerKey string) (integrationmodel.ConnectorProviderSchema, bool) {
	if provider, ok := r.connectorProviders.Provider(connectorKey, providerKey); ok {
		return toIntegrationProviderSchema(provider.Descriptor()), true
	}
	return integrationmodel.ConnectorProviderSchema{}, false
}

func (r *ConnectorRegistry) RegisterEventHandler(key string, handler EventHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.eventHandlers[strings.TrimSpace(key)] = handler
}

func (r *ConnectorRegistry) RegisterOutboxSender(key string, sender OutboxSender) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outboxSenders[strings.TrimSpace(key)] = sender
}

func (r *ConnectorRegistry) ProviderAdapter(connectorKey, providerKey string) (integrationcontract.Adapter, bool) {
	if provider, ok := r.connectorProviders.Provider(connectorKey, providerKey); ok {
		return newPublicProviderAdapter(provider, provider.Descriptor()), true
	}
	return nil, false
}

func (r *ConnectorRegistry) ConnectorBackgroundProvider(connectorKey, providerKey string) (connector.Adapter, connector.ProviderDescriptor, connector.BackgroundProcessor, bool) {
	provider, ok := r.connectorProviders.Provider(strings.TrimSpace(connectorKey), strings.TrimSpace(providerKey))
	if !ok {
		return nil, connector.ProviderDescriptor{}, nil, false
	}
	capability, ok := provider.(connector.BackgroundCapabilityProvider)
	if !ok {
		return nil, connector.ProviderDescriptor{}, nil, false
	}
	processor, ok := capability.BackgroundProcessor()
	return provider, provider.Descriptor(), processor, ok
}

func (r *ConnectorRegistry) ConnectorBackgroundCleanupProvider(connectorKey, providerKey string) (connector.Adapter, connector.ProviderDescriptor, connector.BackgroundCleanupProcessor, bool) {
	provider, ok := r.connectorProviders.Provider(strings.TrimSpace(connectorKey), strings.TrimSpace(providerKey))
	if !ok {
		return nil, connector.ProviderDescriptor{}, nil, false
	}
	capability, ok := provider.(connector.BackgroundCleanupCapabilityProvider)
	if !ok {
		return nil, connector.ProviderDescriptor{}, nil, false
	}
	processor, ok := capability.BackgroundCleanupProcessor()
	return provider, provider.Descriptor(), processor, ok
}

func (r *ConnectorRegistry) EventHandler(key string) (EventHandler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.eventHandlers[strings.TrimSpace(key)]
	return value, ok
}

func (r *ConnectorRegistry) OutboxSender(key string) (OutboxSender, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	value, ok := r.outboxSenders[strings.TrimSpace(key)]
	return value, ok
}

func (r *ConnectorRegistry) HasEventHandlers() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.eventHandlers) > 0
}

func (r *ConnectorRegistry) HasEventWork() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.eventHandlers) > 0 || len(r.schema.EventMappings) > 0
}

func (r *ConnectorRegistry) HasOutboxSenders() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.outboxSenders) > 0
}

func (r *ConnectorRegistry) AdapterReady(connector integrationmodel.ConnectorSchema) bool {
	for _, provider := range connector.Providers {
		if _, ready := r.ProviderAdapter(connector.Key, provider.Key); ready {
			return true
		}
	}
	return false
}
