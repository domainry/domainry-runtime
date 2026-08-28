package integration

import (
	"context"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"strings"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func (s *IntegrationApplicationService) RotateIntegrationConnection(ctx context.Context, connectionKey string, req integrationmodel.IntegrationConnectionUpsertRequest, principal principalmodel.Principal) (integrationmodel.IntegrationConnection, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	if err := ctx.Err(); err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	if !HasPermission(principal, PermissionConnectionManage) {
		return integrationmodel.IntegrationConnection{}, forbidden("auth.permission_denied")
	}
	workspaceID := principalWorkspaceID(principal)
	existing, ok, err := s.findConnection(ctx, connectionKey, workspaceID)
	if err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	if !ok {
		return integrationmodel.IntegrationConnection{}, notFound("backend.integration.connection.not_found")
	}
	connector, ok := s.connectorDefinition(existing.ConnectorKey)
	if !ok {
		return integrationmodel.IntegrationConnection{}, notFound("backend.integration.connector.not_found")
	}
	requested := strings.TrimSpace(req.ProviderKey)
	if requested == "" {
		requested = existing.ProviderKey
	}
	providerKey, err := resolveConnectorProvider(connector, requested)
	if err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	if strings.TrimSpace(existing.ProviderKey) != "" && existing.ProviderKey != providerKey {
		return integrationmodel.IntegrationConnection{}, conflict("backend.integration.connection.provider_immutable", "connection", existing.Key, "expected", existing.ProviderKey, "actual", providerKey)
	}
	secretRefs, err := s.NormalizeSecretRefs(ctx, req.SecretRefs, workspaceID)
	if err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	if len(secretRefs) == 0 {
		return integrationmodel.IntegrationConnection{}, badRequest("backend.integration.secret_ref.missing_rotation_refs")
	}
	status := "configured"
	if strings.TrimSpace(req.Status) != "" {
		status, err = NormalizeConnectionStatus(req.Status)
		if err != nil {
			return integrationmodel.IntegrationConnection{}, err
		}
	}
	before := connectionAuditShape(existing)
	existing.ProviderKey, existing.SecretRefs, existing.Status = providerKey, secretRefs, status
	if req.Config != nil {
		existing.Config = cloneMap(req.Config)
	}
	if strings.TrimSpace(req.Name) != "" {
		existing.Name = strings.TrimSpace(req.Name)
	}
	if err := s.ValidateAdapterConfig(existing); err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	saved, err := s.upsertConnectionAndSyncBackground(ctx, existing)
	if err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	s.audit(ctx, "integration_connection_rotated", "integration_connection", saved.Key, principal, "Rotated integration connection "+saved.Key, before, connectionAuditShape(saved), map[string]any{
		"workspace_id": workspaceID, "connector_key": saved.ConnectorKey, "connection_key": saved.Key,
		"secret_ref_names": sortedStringKeys(saved.SecretRefs),
	})
	return saved, nil
}
