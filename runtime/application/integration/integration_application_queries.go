package integration

import (
	"context"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"strings"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func (s *IntegrationApplicationService) ListIntegrationSecrets(ctx context.Context, principal principalmodel.Principal) ([]integrationmodel.IntegrationSecret, error) {
	if err := integrationAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	if !HasPermission(principal, PermissionSecretManage) {
		return nil, forbidden("auth.permission_denied")
	}
	return s.configRepo.ListSecrets(ctx, principalWorkspaceID(principal))
}

func (s *IntegrationApplicationService) ListIntegrationAPIKeys(ctx context.Context, principal principalmodel.Principal) ([]integrationmodel.IntegrationAPIKey, error) {
	if err := integrationAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	if !principal.HasPermission("workspace.admin") {
		return nil, forbidden("auth.permission_denied")
	}
	return s.configRepo.ListAPIKeys(ctx, principalWorkspaceID(principal))
}

func (s *IntegrationApplicationService) ListIntegrationEvents(ctx context.Context, provider, status string, limit int, principal principalmodel.Principal) ([]integrationmodel.IntegrationEvent, error) {
	if err := integrationAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	if !HasPermission(principal, PermissionAuditView) {
		return nil, forbidden("auth.permission_denied")
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	return s.eventRepo.ListEvents(ctx, principalWorkspaceID(principal), strings.TrimSpace(provider), strings.TrimSpace(status), limit)
}

func (s *IntegrationApplicationService) InspectIntegrationEvent(ctx context.Context, eventID string, principal principalmodel.Principal) (integrationmodel.IntegrationEvent, error) {
	if err := integrationAuthorizeQuery(principal); err != nil {
		return integrationmodel.IntegrationEvent{}, err
	}
	event, found, err := s.eventRepo.GetEvent(ctx, principalWorkspaceID(principal), strings.TrimSpace(eventID))
	if err != nil {
		return integrationmodel.IntegrationEvent{}, err
	}
	if !found {
		return integrationmodel.IntegrationEvent{}, notFound("backend.integration.event.not_found")
	}
	return event, nil
}

func (s *IntegrationApplicationService) ListIntegrationWebhookSubscriptions(ctx context.Context, connectorKey, eventType, status string, limit int, principal principalmodel.Principal) ([]integrationmodel.IntegrationWebhookSubscription, error) {
	if err := integrationAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	if !HasPermission(principal, PermissionCatalogView) {
		return nil, forbidden("auth.permission_denied")
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	return s.configRepo.ListWebhookSubscriptions(ctx, principalWorkspaceID(principal), strings.TrimSpace(connectorKey), strings.TrimSpace(eventType), strings.TrimSpace(status), limit)
}

func (s *IntegrationApplicationService) ListIntegrationExternalIdentities(ctx context.Context, principal principalmodel.Principal) ([]integrationmodel.IntegrationExternalIdentity, error) {
	if err := integrationAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	if !principal.HasPermission("workspace.admin") {
		return nil, forbidden("auth.permission_denied")
	}
	return s.configRepo.ListExternalIdentities(ctx, principalWorkspaceID(principal))
}

func (s *IntegrationApplicationService) ListIntegrationConnections(ctx context.Context, principal principalmodel.Principal) ([]integrationmodel.IntegrationConnection, error) {
	if err := integrationAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	if !HasPermission(principal, PermissionCatalogView) {
		return nil, forbidden("auth.permission_denied")
	}
	connections, err := s.configRepo.ListConnections(ctx, principalWorkspaceID(principal))
	if err != nil {
		return nil, err
	}
	for index := range connections {
		connections[index], err = s.normalizeConnection(ctx, connections[index])
		if err != nil {
			return nil, err
		}
	}
	return connections, nil
}
