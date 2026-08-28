package integration

import (
	"context"
	"time"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"sort"
	"strings"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func (s *IntegrationApplicationService) RecordCredentialRefreshFailure(ctx context.Context, connection integrationmodel.IntegrationConnection, principal principalmodel.Principal, refreshErr error) error {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	before := connectionAuditShape(connection)
	connection.Status = "degraded"
	now := time.Now().UTC()
	event, notify, err := s.compileCredentialRefreshFailedNotification(connection, refreshErr, now)
	if err != nil {
		return err
	}
	var saved integrationmodel.IntegrationConnection
	if notify {
		saved, err = s.credentialNotifications.CommitIntegrationConnectionNotification(ctx, connection, event)
	} else {
		saved, err = s.upsertConnectionAndSyncBackground(ctx, connection)
	}
	if err != nil {
		return err
	}
	s.audit(ctx, "integration_credential_refresh_failed", "integration_connection", saved.Key, principal, "Provider credential refresh failed "+saved.Key, before, connectionAuditShape(saved), map[string]any{
		"workspace_id": saved.WorkspaceID, "connector_key": saved.ConnectorKey, "connection_key": saved.Key,
		"error_code": valueOrDefault(integrationErrorCode(refreshErr), "backend.integration.credential.refresh_failed"),
	})
	return nil
}

func (s *IntegrationApplicationService) RecordCredentialRefreshRecovery(ctx context.Context, connection integrationmodel.IntegrationConnection, principal principalmodel.Principal) error {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if connection.Status != "degraded" {
		return nil
	}
	before := connectionAuditShape(connection)
	connection.Status = "active"
	now := time.Now().UTC()
	event, notify, err := s.compileCredentialRefreshRecoveryNotification(connection, now)
	if err != nil {
		return err
	}
	var saved integrationmodel.IntegrationConnection
	if notify {
		saved, err = s.credentialNotifications.CommitIntegrationConnectionNotification(ctx, connection, event)
	} else {
		saved, err = s.upsertConnectionAndSyncBackground(ctx, connection)
	}
	if err != nil {
		return err
	}
	s.audit(ctx, "integration_credential_refresh_recovered", "integration_connection", saved.Key, principal, "Provider credential refresh recovered "+saved.Key, before, connectionAuditShape(saved), map[string]any{
		"workspace_id": saved.WorkspaceID, "connector_key": saved.ConnectorKey, "connection_key": saved.Key,
	})
	return nil
}

func (s *IntegrationApplicationService) DisableIntegrationConnection(ctx context.Context, connectionKey string, principal principalmodel.Principal) (integrationmodel.IntegrationConnection, error) {
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
	connection, ok, err := s.findConnection(ctx, connectionKey, workspaceID)
	if err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	if !ok {
		return integrationmodel.IntegrationConnection{}, notFound("backend.integration.connection.not_found")
	}
	before := connectionAuditShape(connection)
	connection.Status = "disabled"
	saved, err := s.upsertConnectionAndSyncBackground(ctx, connection)
	if err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	s.audit(ctx, "integration_connection_disabled", "integration_connection", saved.Key, principal, "Disabled integration connection "+saved.Key, before, connectionAuditShape(saved), map[string]any{
		"workspace_id": workspaceID, "connector_key": saved.ConnectorKey, "connection_key": saved.Key,
		"secret_ref_names": sortedStringKeys(saved.SecretRefs),
	})
	return saved, nil
}

func (s *IntegrationApplicationService) IntegrationConnectionForOutboxMessage(ctx context.Context, message integrationmodel.IntegrationOutboxMessage, principal principalmodel.Principal, connectorKey string) (integrationmodel.IntegrationConnection, error) {
	if err := integrationAuthorizeQuery(principal); err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	expectedConnector := strings.TrimSpace(connectorKey)
	if expectedConnector == "" {
		expectedConnector = strings.TrimSpace(message.ConnectorKey)
	}
	if strings.TrimSpace(message.ConnectionKey) == "" {
		return integrationmodel.IntegrationConnection{}, badRequest("backend.integration.outbox.connection_required")
	}
	workspaceID := strings.TrimSpace(message.WorkspaceID)
	if err := integrationAuthorizeWorkspaceQuery(workspaceID); err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	if workspaceID != principalWorkspaceID(principal) {
		return integrationmodel.IntegrationConnection{}, forbidden("auth.permission_denied")
	}
	connection, ok, err := s.findConnection(ctx, message.ConnectionKey, workspaceID)
	if err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	if !ok {
		return integrationmodel.IntegrationConnection{}, notFound("backend.integration.connection.not_found")
	}
	if expectedConnector != "" && strings.TrimSpace(connection.ConnectorKey) != expectedConnector {
		return integrationmodel.IntegrationConnection{}, badRequest("backend.integration.outbox.connection_connector_mismatch")
	}
	if !connectionCanSend(connection) {
		return integrationmodel.IntegrationConnection{}, badRequest("backend.integration.outbox.connection_unavailable")
	}
	return connection, nil
}

func (s *IntegrationApplicationService) IntegrationConnectionForWebhook(ctx context.Context, connectionKey string, principal principalmodel.Principal, connectorKey string) (integrationmodel.IntegrationConnection, error) {
	if err := integrationAuthorizeQuery(principal); err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	connectionKey = strings.TrimSpace(connectionKey)
	if connectionKey == "" {
		return integrationmodel.IntegrationConnection{}, badRequest("backend.integration.webhook.connection_required")
	}
	connection, ok, err := s.findConnection(ctx, connectionKey, principalWorkspaceID(principal))
	if err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	if !ok {
		return integrationmodel.IntegrationConnection{}, notFound("backend.integration.connection.not_found")
	}
	if expected := strings.TrimSpace(connectorKey); expected != "" && strings.TrimSpace(connection.ConnectorKey) != expected {
		return integrationmodel.IntegrationConnection{}, badRequest("backend.integration.webhook.connection_connector_mismatch")
	}
	if connection.Status == "disabled" {
		return integrationmodel.IntegrationConnection{}, badRequest("backend.integration.connection.disabled")
	}
	return connection, nil
}

func (s *IntegrationApplicationService) findConnection(ctx context.Context, key, workspaceID string) (integrationmodel.IntegrationConnection, bool, error) {
	connections, err := s.configRepo.ListConnections(ctx, workspaceID)
	if err != nil {
		return integrationmodel.IntegrationConnection{}, false, err
	}
	key = strings.TrimSpace(key)
	for _, connection := range connections {
		if connection.Key == key {
			normalized, err := s.normalizeConnection(ctx, connection)
			if err != nil {
				return integrationmodel.IntegrationConnection{}, false, err
			}
			return normalized, true, nil
		}
	}
	return integrationmodel.IntegrationConnection{}, false, nil
}

func connectionCanSend(connection integrationmodel.IntegrationConnection) bool {
	// Degraded connections remain executable so a successful provider call can
	// prove credential recovery and atomically clear the degraded state.
	return connection.Status == "active" || connection.Status == "verified" || connection.Status == "degraded"
}

func connectionCanTest(connection integrationmodel.IntegrationConnection) bool {
	switch strings.TrimSpace(connection.Status) {
	case "", "configured", "verified", "active", "degraded":
		return true
	default:
		return false
	}
}

func ConnectionCanTest(connection integrationmodel.IntegrationConnection) bool {
	return connectionCanTest(connection)
}

func ConnectionCanSend(connection integrationmodel.IntegrationConnection) bool {
	return connectionCanSend(connection)
}

func connectionAuditShape(connection integrationmodel.IntegrationConnection) map[string]any {
	configKeys := make([]string, 0, len(connection.Config))
	for key := range connection.Config {
		configKeys = append(configKeys, key)
	}
	sort.Strings(configKeys)
	return map[string]any{
		"key": connection.Key, "connector_key": connection.ConnectorKey, "provider_key": connection.ProviderKey,
		"name": connection.Name, "status": connection.Status, "config_keys": configKeys,
		"secret_ref_names": sortedStringKeys(connection.SecretRefs),
	}
}

func sortedStringKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
