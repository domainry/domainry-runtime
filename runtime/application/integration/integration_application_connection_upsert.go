package integration

import (
	"context"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"strings"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func (s *IntegrationApplicationService) UpsertIntegrationConnection(ctx context.Context, connectionKey string, req integrationmodel.IntegrationConnectionUpsertRequest, principal principalmodel.Principal) (integrationmodel.IntegrationConnection, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	if err := ctx.Err(); err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	if !HasPermission(principal, PermissionConnectionManage) {
		return integrationmodel.IntegrationConnection{}, forbidden("auth.permission_denied")
	}
	key := strings.TrimSpace(connectionKey)
	if key == "" {
		key = strings.TrimSpace(req.Key)
	}
	if key == "" {
		return integrationmodel.IntegrationConnection{}, badRequest("backend.integration.connection.missing_key", "field_path", "key")
	}
	if !integrationmodel.ValidConnectorIdentityKey(key) {
		return integrationmodel.IntegrationConnection{}, badRequest("backend.integration.connection.key_invalid", "field_path", "key", "actual", key)
	}
	connectorKey := strings.TrimSpace(req.ConnectorKey)
	if connectorKey == "" {
		return integrationmodel.IntegrationConnection{}, badRequest("backend.integration.connection.missing_connector", "field_path", "connector_key")
	}
	connector, ok := s.connectorDefinition(connectorKey)
	if !ok {
		return integrationmodel.IntegrationConnection{}, notFound("backend.integration.connector.not_found")
	}
	if connectorLifecycleStatus(connector) != "active" {
		return integrationmodel.IntegrationConnection{}, badRequest("backend.integration.connector.reclassified", "connector", connector.Key, "replacement_capability", connector.ReplacementCapability)
	}
	if err := s.validateConnectionDraft(ctx, key, req, principal); err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	providerKey, err := resolveConnectorProvider(connector, req.ProviderKey)
	if err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	workspaceID := principalWorkspaceID(principal)
	existing, existed, err := s.findConnection(ctx, key, workspaceID)
	if err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	if existed && existing.ConnectorKey != connectorKey {
		return integrationmodel.IntegrationConnection{}, conflict("backend.integration.connection.connector_immutable", "connection", key, "expected", existing.ConnectorKey, "actual", connectorKey)
	}
	if existed && strings.TrimSpace(existing.ProviderKey) != "" && existing.ProviderKey != providerKey {
		return integrationmodel.IntegrationConnection{}, conflict("backend.integration.connection.provider_immutable", "connection", key, "expected", existing.ProviderKey, "actual", providerKey)
	}
	secretRefs, err := s.NormalizeSecretRefs(ctx, req.SecretRefs, workspaceID)
	if err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	status, err := NormalizeConnectionStatus(req.Status)
	if err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	if err := s.ValidateProviderSecretRefs(ctx, connector, providerKey, status, secretRefs, workspaceID); err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	config, err := s.prepareConnectionConfig(connector, providerKey, status, req.Config)
	if err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	connection := integrationmodel.IntegrationConnection{
		Key: key, WorkspaceID: workspaceID, ConnectorKey: connectorKey, ProviderKey: providerKey,
		Name: strings.TrimSpace(req.Name), Status: status, Config: config, SecretRefs: secretRefs,
		CreatedBy: strings.TrimSpace(principal.UserID),
	}
	if err := s.ValidateAdapterConfig(connection); err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	saved, err := s.upsertConnectionAndSyncBackground(ctx, connection)
	if err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	var before map[string]any
	if existed {
		before = connectionAuditShape(existing)
	}
	s.audit(ctx, "integration_connection_upserted", "integration_connection", saved.Key, principal, "Upserted integration connection "+saved.Key, before, connectionAuditShape(saved), map[string]any{
		"workspace_id": workspaceID, "connector_key": saved.ConnectorKey, "connection_key": saved.Key,
		"secret_ref_names": sortedStringKeys(saved.SecretRefs),
	})
	return saved, nil
}

func connectorLifecycleStatus(connector integrationmodel.ConnectorSchema) string {
	if status := strings.TrimSpace(connector.LifecycleStatus); status != "" {
		return status
	}
	return "active"
}
