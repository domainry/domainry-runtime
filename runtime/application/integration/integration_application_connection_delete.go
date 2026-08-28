package integration

import (
	"context"
	"fmt"
	"strings"

	connector "github.com/domainry/domainry-connector-sdk"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	integrationruntime "github.com/domainry/domainry-runtime/runtime/domain/integration/runtime"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func (s *IntegrationApplicationService) DeleteIntegrationConnection(ctx context.Context, connectionKey string, principal principalmodel.Principal) error {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !HasPermission(principal, PermissionConnectionManage) {
		return forbidden("auth.permission_denied")
	}
	workspaceID, key := principalWorkspaceID(principal), strings.TrimSpace(connectionKey)
	existing, ok, err := s.findConnection(ctx, key, workspaceID)
	if err != nil {
		return err
	}
	if !ok {
		return notFound("backend.integration.connection.not_found")
	}
	references, err := s.connectionReferences(ctx, key, principal)
	if err != nil {
		return err
	}
	if len(references) > 0 {
		return connectionReferenceConflict(references[0])
	}
	subscriptions, err := s.configRepo.ListWebhookSubscriptions(ctx, workspaceID, "", "", "", 500)
	if err != nil {
		return err
	}
	for _, subscription := range subscriptions {
		if strings.TrimSpace(subscription.ConnectionKey) == key {
			return connectionReferenceConflict(ConnectionReference{Kind: "webhook_subscription", Key: subscription.Key, Path: "connection_key"})
		}
	}
	if err := s.cleanupConnectorBackground(ctx, existing); err != nil {
		return err
	}
	repository, ok := s.configRepo.(integrationrepository.IntegrationConnectionDeleteRepository)
	if !ok {
		return internalError("delete integration connection", fmt.Errorf("integration connection delete repository is not configured"))
	}
	deleted, err := repository.DeleteConnection(ctx, workspaceID, key)
	if err != nil {
		return err
	}
	if !deleted {
		_, stillExists, findErr := s.findConnection(ctx, key, workspaceID)
		if findErr != nil {
			return findErr
		}
		if stillExists {
			return conflict("backend.integration.connection.referenced", "reference_kind", "webhook_subscription", "connection_key", key)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return notFound("backend.integration.connection.not_found")
	}
	s.audit(ctx, "integration_connection_deleted", "integration_connection", key, principal, "Deleted integration connection "+key, connectionAuditShape(existing), nil, map[string]any{
		"workspace_id": workspaceID, "connector_key": existing.ConnectorKey, "connection_key": key,
	})
	return nil
}

type connectorBackgroundCleanupRegistry interface {
	ConnectorBackgroundCleanupProvider(string, string) (connector.Adapter, connector.ProviderDescriptor, connector.BackgroundCleanupProcessor, bool)
}

func (s *IntegrationApplicationService) cleanupConnectorBackground(ctx context.Context, connection integrationmodel.IntegrationConnection) error {
	registry, ok := s.registry.(connectorBackgroundCleanupRegistry)
	if !ok {
		return nil
	}
	provider, descriptor, processor, ok := registry.ConnectorBackgroundCleanupProvider(connection.ConnectorKey, connection.ProviderKey)
	if !ok {
		return nil
	}
	bridge := &publicProviderAdapter{provider: provider, descriptor: descriptor}
	secrets, err := s.ResolveAdapterSecrets(ctx, connection)
	if err != nil {
		return err
	}
	secrets, err = bridge.scopeSecrets(secrets)
	if err != nil {
		return err
	}
	updates, err := processor.CleanupBackground(ctx, bridge.scopedConnection(connection), secrets, s.worker.Clock.Now().UTC(), toConnectorPrincipal(integrationruntime.IntegrationWorkerPrincipal(connection.WorkspaceID)))
	if err != nil {
		return err
	}
	return s.PersistAdapterSecretUpdates(ctx, connection, secrets, updates)
}

func connectionReferenceConflict(reference ConnectionReference) error {
	return conflict("backend.integration.connection.referenced", "reference_kind", reference.Kind, "reference_key", reference.Key, "reference_path", reference.Path)
}
