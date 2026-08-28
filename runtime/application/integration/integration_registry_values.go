package integration

import (
	"strings"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationpolicy "github.com/domainry/domainry-runtime/runtime/domain/integration/policy"
)

func cloneIntegrationSchema(value integrationmodel.IntegrationSchema) integrationmodel.IntegrationSchema {
	return integrationmodel.IntegrationSchema{
		Connectors:    append([]integrationmodel.ConnectorSchema(nil), value.Connectors...),
		Connections:   append([]integrationmodel.ConnectionSchema(nil), value.Connections...),
		EventMappings: append([]integrationmodel.IntegrationEventMappingSchema(nil), value.EventMappings...),
	}
}

func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func valueOrDefault(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func projectTenantAdminConnector(value integrationmodel.ConnectorSchema, connections []integrationmodel.IntegrationConnection, compiledProviders map[string]bool) TenantAdminConnectorDTO {
	enabled := connectorLifecycleStatus(value) == "active"
	out := TenantAdminConnectorDTO{
		Key: value.Key, Name: value.Name, Description: value.Description, Status: value.LifecycleStatus,
		Providers: []TenantAdminConnectorProviderDTO{}, Operations: []TenantAdminConnectorOperationDTO{},
		ConfigFields: append([]string(nil), value.ConfigFields...), SecretRefs: append([]string(nil), value.SecretRefs...),
		Availability: TenantAdminConnectorAvailabilityDTO{CatalogAvailable: true, Enabled: enabled},
	}
	for _, provider := range value.Providers {
		availability := tenantAdminProviderAvailability(value.Key, provider, enabled, connections, compiledProviders)
		out.Providers = append(out.Providers, TenantAdminConnectorProviderDTO{
			Key: provider.Key, Name: provider.Name, Description: provider.Description,
			OperationKeys: append([]string(nil), provider.OperationKeys...), Availability: availability,
		})
		out.Availability.Bound = out.Availability.Bound || availability.Bound
		out.Availability.Compiled = out.Availability.Compiled || availability.Compiled
		out.Availability.Configured = out.Availability.Configured || availability.Configured
		out.Availability.Healthy = out.Availability.Healthy || availability.Healthy
		out.Availability.Degraded = out.Availability.Degraded || availability.Degraded
	}
	for _, operation := range value.Operations {
		out.Operations = append(out.Operations, TenantAdminConnectorOperationDTO{
			Key: operation.Key, Name: operation.Name, Description: operation.Description, SideEffect: operation.SideEffect,
			IdempotencySupported: operation.IdempotencySupported, TestSupported: operation.TestSupported, DryRunSupported: operation.DryRunSupported,
		})
	}
	return out
}

func tenantAdminProviderAvailability(connectorKey string, provider integrationmodel.ConnectorProviderSchema, enabled bool, connections []integrationmodel.IntegrationConnection, compiledProviders map[string]bool) TenantAdminConnectorAvailabilityDTO {
	availability := TenantAdminConnectorAvailabilityDTO{
		CatalogAvailable: true,
		Compiled:         compiledProviders[integrationpolicy.IntegrationProviderIdentity(connectorKey, provider.Key)],
		Enabled:          enabled,
	}
	for _, connection := range connections {
		if connection.ConnectorKey != connectorKey || connection.ProviderKey != provider.Key {
			continue
		}
		availability.Bound = true
		switch strings.TrimSpace(connection.Status) {
		case "configured", "verified", "active", "degraded", "disabled":
			availability.Configured = true
		}
		switch strings.TrimSpace(connection.Status) {
		case "verified", "active":
			availability.Healthy = true
		case "degraded":
			availability.Degraded = true
		}
	}
	return availability
}
