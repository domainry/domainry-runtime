package integration

import (
	"context"
	"fmt"
	"github.com/domainry/domainry-connector-sdk"
	connectorcatalog "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"strings"
)

func SyncManifestIntegrationConnections(ctx context.Context, repository integrationrepository.IntegrationConnectionRepository, providers *connector.Registry, integrations integrationmodel.IntegrationSchema, scope principalmodel.SystemScope) error {
	return syncManifestIntegrationConnections(ctx, repository, providers, integrations, scope, connectorcatalog.Builtin)
}

func syncManifestIntegrationConnections(ctx context.Context, repository integrationrepository.IntegrationConnectionRepository, providers *connector.Registry, integrations integrationmodel.IntegrationSchema, scope principalmodel.SystemScope, loadCatalog func() ([]integrationmodel.ConnectorSchema, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return fmt.Errorf("backend.system_scope_required: %w", err)
	}
	connectors, err := loadCatalog()
	if err != nil {
		return fmt.Errorf("load Runtime Connector catalog: %w", err)
	}
	byKey := make(map[string]integrationmodel.ConnectorSchema, len(connectors)+len(integrations.Connectors))
	for _, connector := range connectors {
		byKey[connector.Key] = connector
	}
	for _, connector := range integrations.Connectors {
		byKey[connector.Key] = connector
	}
	existingConnections, err := repository.ListConnections(ctx, principalmodel.InstallationWorkspaceID)
	if err != nil {
		return err
	}
	existingByKey := make(map[string]integrationmodel.IntegrationConnection, len(existingConnections))
	for _, connection := range existingConnections {
		existingByKey[strings.TrimSpace(connection.Key)] = connection
	}
	for _, connection := range integrations.Connections {
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.TrimSpace(connection.Key) == "" || strings.TrimSpace(connection.ConnectorKey) == "" {
			continue
		}
		connector, exists := byKey[strings.TrimSpace(connection.ConnectorKey)]
		if !exists {
			return fmt.Errorf("manifest Connection %q references unknown Connector %q", connection.Key, connection.ConnectorKey)
		}
		providerKey, err := resolveManifestConnectionProvider(connector, connection.ProviderKey)
		if err != nil {
			return fmt.Errorf("manifest Connection %q: %w", connection.Key, err)
		}
		for configKey := range connection.Config {
			trimmed := strings.TrimSpace(configKey)
			if trimmed == "provider" || trimmed == "providers" || strings.HasSuffix(trimmed, "_providers") {
				return fmt.Errorf("manifest Connection %q must select Provider with provider_key, not config.%s", connection.Key, configKey)
			}
		}
		status := strings.TrimSpace(connection.Status)
		if status != "" {
			status, err = NormalizeConnectionStatus(status)
			if err != nil {
				return fmt.Errorf("manifest Connection %q: %w", connection.Key, err)
			}
		}
		candidate := integrationmodel.IntegrationConnection{
			Key: connection.Key, WorkspaceID: principalmodel.InstallationWorkspaceID, ConnectorKey: connection.ConnectorKey, ProviderKey: providerKey,
			Name: connection.Name, Status: status, Config: connection.Config, SecretRefs: map[string]string{}, CreatedBy: "manifest",
		}
		if existing, found := existingByKey[strings.TrimSpace(connection.Key)]; found &&
			strings.TrimSpace(existing.ConnectorKey) == strings.TrimSpace(candidate.ConnectorKey) &&
			strings.TrimSpace(existing.ProviderKey) == strings.TrimSpace(candidate.ProviderKey) {
			candidate = preserveManagedConnectionState(candidate, existing)
		}
		candidate.Config, err = materializeManifestConnectionConfig(providers, candidate)
		if err != nil {
			return fmt.Errorf("manifest Connection %q: %w", connection.Key, err)
		}
		if strings.TrimSpace(candidate.Status) == "" || manifestConnectionEligibleForDefaultSafeActivation(providers, candidate) {
			candidate.Status = manifestConnectionDefaultStatus(providers, candidate)
		}
		if _, err := repository.UpsertConnection(ctx, principalmodel.InstallationWorkspaceID, candidate); err != nil {
			return err
		}
	}
	return nil
}

func manifestConnectionEligibleForDefaultSafeActivation(providers *connector.Registry, connection integrationmodel.IntegrationConnection) bool {
	if providers == nil || strings.TrimSpace(connection.Status) != "configured" || strings.TrimSpace(connection.CreatedBy) != "manifest" {
		return false
	}
	provider, ok := providers.Provider(strings.TrimSpace(connection.ConnectorKey), strings.TrimSpace(connection.ProviderKey))
	return ok && provider.Descriptor().StartupActivation == connector.StartupActivationDefaultSafe
}

func materializeManifestConnectionConfig(providers *connector.Registry, connection integrationmodel.IntegrationConnection) (map[string]any, error) {
	if providers == nil {
		return cloneManifestConnectionConfig(connection.Config), nil
	}
	provider, ok := providers.Provider(strings.TrimSpace(connection.ConnectorKey), strings.TrimSpace(connection.ProviderKey))
	if !ok {
		return cloneManifestConnectionConfig(connection.Config), nil
	}
	return connector.ApplyConfigDefaults(provider.Descriptor().ConfigFields, connection.Config)
}

func cloneManifestConnectionConfig(config map[string]any) map[string]any {
	result := make(map[string]any, len(config))
	for key, value := range config {
		result[key] = value
	}
	return result
}

func preserveManagedConnectionState(manifest, existing integrationmodel.IntegrationConnection) integrationmodel.IntegrationConnection {
	config := make(map[string]any, len(manifest.Config)+len(existing.Config))
	for key, value := range manifest.Config {
		config[key] = value
	}
	for key, value := range existing.Config {
		config[key] = value
	}
	manifest.Config = config
	manifest.SecretRefs = make(map[string]string, len(existing.SecretRefs))
	for key, value := range existing.SecretRefs {
		manifest.SecretRefs[key] = value
	}
	if status := strings.TrimSpace(existing.Status); status != "" {
		manifest.Status = status
	}
	if strings.TrimSpace(manifest.Name) == "" {
		manifest.Name = existing.Name
	}
	if strings.TrimSpace(existing.CreatedBy) != "" {
		manifest.CreatedBy = existing.CreatedBy
	}
	return manifest
}

// manifestConnectionDefaultStatus makes a project-owned Provider immediately
// executable only when its published descriptor proves that no Runtime-managed
// setup is missing. "verified" remains reserved for real connection-test
// evidence; providers that require configuration or credentials stay
// "configured" until the management plane supplies them.
func manifestConnectionDefaultStatus(providers *connector.Registry, connection integrationmodel.IntegrationConnection) string {
	if providers == nil {
		return "configured"
	}
	provider, ok := providers.Provider(strings.TrimSpace(connection.ConnectorKey), strings.TrimSpace(connection.ProviderKey))
	if !ok {
		return "configured"
	}
	descriptor := provider.Descriptor()
	if descriptor.StartupActivation != connector.StartupActivationDefaultSafe {
		return "configured"
	}
	for _, field := range descriptor.ConfigFields {
		if field.Required && !manifestConnectionConfigValuePresent(connection.Config, field.Key) {
			return "configured"
		}
	}
	for _, field := range descriptor.SecretFields {
		if field.Required {
			return "configured"
		}
	}
	if validator, ok := provider.(connector.ConfigValidator); ok {
		if err := validator.ValidateConfig(connector.Connection{
			Key: connection.Key, WorkspaceID: principalmodel.InstallationWorkspaceID,
			ConnectorKey: connection.ConnectorKey, ProviderKey: connection.ProviderKey,
			Name: connection.Name, Status: "active", Config: connection.Config,
			SecretRefs: map[string]string{}, CreatedBy: "manifest",
		}); err != nil {
			return "configured"
		}
	}
	return "active"
}

func manifestConnectionConfigValuePresent(config map[string]any, key string) bool {
	value, exists := config[strings.TrimSpace(key)]
	if !exists || value == nil {
		return false
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text) != ""
	}
	return true
}

func resolveManifestConnectionProvider(connector integrationmodel.ConnectorSchema, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "multi" || requested == "generated" {
		return "", fmt.Errorf("Provider %q is not executable", requested)
	}
	if requested == "" {
		return "", fmt.Errorf("concrete provider_key is required for Connector %q", connector.Key)
	}
	for _, provider := range connector.Providers {
		if strings.TrimSpace(provider.Key) == requested {
			return requested, nil
		}
	}
	return "", fmt.Errorf("Provider %q is not declared by Connector %q", requested, connector.Key)
}
