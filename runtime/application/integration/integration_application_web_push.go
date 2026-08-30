package integration

import (
	"context"
	"fmt"
	"strings"
	"time"

	integrationsdk "github.com/domainry/domainry-integration-sdk"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func (s *IntegrationApplicationService) ListWebPushSubscriptions(ctx context.Context, principal principalmodel.Principal) ([]integrationmodel.WebPushSubscription, error) {
	if err := authorizeWebPushSelfService(principal, false); err != nil {
		return nil, err
	}
	if s.ownerWebPushSubscriptions == nil {
		return nil, fmt.Errorf("backend.integration.notification.subscription_store_unavailable")
	}
	values, err := s.ownerWebPushSubscriptions.List(ctx, principalWorkspaceID(principal), principal.UserID)
	if err != nil {
		return nil, err
	}
	result := make([]integrationmodel.WebPushSubscription, 0, len(values))
	for _, value := range values {
		result = append(result, runtimeWebPushSubscription(value))
	}
	return result, nil
}

func (s *IntegrationApplicationService) UpsertWebPushSubscription(ctx context.Context, id string, request integrationmodel.WebPushSubscriptionUpsertRequest, principal principalmodel.Principal) (integrationmodel.WebPushSubscription, error) {
	if err := authorizeWebPushSelfService(principal, true); err != nil {
		return integrationmodel.WebPushSubscription{}, err
	}
	id, endpoint := strings.TrimSpace(id), strings.TrimSpace(request.Endpoint)
	if id == "" || len(id) > 200 || !strings.HasPrefix(endpoint, "https://") || strings.TrimSpace(request.P256DH) == "" || strings.TrimSpace(request.Auth) == "" {
		return integrationmodel.WebPushSubscription{}, badRequest("backend.integration.notification.subscription_invalid")
	}
	if request.ExpiresAt != "" {
		if expires, err := time.Parse(time.RFC3339, request.ExpiresAt); err != nil || !expires.After(time.Now().UTC()) {
			return integrationmodel.WebPushSubscription{}, badRequest("backend.integration.notification.subscription_expiry_invalid")
		}
	}
	value := integrationmodel.WebPushSubscription{ID: id, WorkspaceID: principalWorkspaceID(principal), UserID: principal.UserID, ExpiresAt: request.ExpiresAt}
	if s.ownerWebPushSubscriptions == nil {
		return value, fmt.Errorf("backend.integration.notification.subscription_store_unavailable")
	}
	owned, err := s.ownerWebPushSubscriptions.Upsert(ctx, value.WorkspaceID, value.UserID, value.ID, integrationsdk.WebPushSubscriptionInput{Endpoint: endpoint, P256DH: strings.TrimSpace(request.P256DH), Auth: strings.TrimSpace(request.Auth), ExpiresAt: request.ExpiresAt})
	if err != nil {
		return value, err
	}
	saved := runtimeWebPushSubscription(owned)
	s.audit(ctx, "web_push_subscription_upserted", "web_push_subscription", saved.ID, principal, "Created or updated Web Push subscription", nil, map[string]any{"status": saved.Status, "endpoint_hash": saved.EndpointHash}, map[string]any{"workspace_id": saved.WorkspaceID, "user_id": saved.UserID})
	return saved, nil
}

func (s *IntegrationApplicationService) RevokeWebPushSubscription(ctx context.Context, id string, principal principalmodel.Principal) (integrationmodel.WebPushSubscription, error) {
	if err := authorizeWebPushSelfService(principal, true); err != nil {
		return integrationmodel.WebPushSubscription{}, err
	}
	if s.ownerWebPushSubscriptions == nil {
		return integrationmodel.WebPushSubscription{}, fmt.Errorf("backend.integration.notification.subscription_store_unavailable")
	}
	owned, err := s.ownerWebPushSubscriptions.Revoke(ctx, principalWorkspaceID(principal), principal.UserID, strings.TrimSpace(id))
	saved := runtimeWebPushSubscription(owned)
	if err != nil {
		return saved, err
	}
	s.audit(ctx, "web_push_subscription_revoked", "web_push_subscription", saved.ID, principal, "Revoked Web Push subscription", nil, map[string]any{"status": saved.Status, "endpoint_hash": saved.EndpointHash}, map[string]any{"workspace_id": saved.WorkspaceID, "user_id": saved.UserID})
	return saved, nil
}

func (s *IntegrationApplicationService) WebPushReadiness(ctx context.Context, principal principalmodel.Principal) (integrationmodel.WebPushReadiness, error) {
	if err := authorizeWebPushSelfService(principal, false); err != nil {
		return integrationmodel.WebPushReadiness{}, err
	}
	if s.ownerWebPushSubscriptions == nil {
		return integrationmodel.WebPushReadiness{}, fmt.Errorf("backend.integration.notification.subscription_store_unavailable")
	}
	value, err := s.ownerWebPushSubscriptions.Readiness(ctx, principalWorkspaceID(principal))
	if err != nil {
		return integrationmodel.WebPushReadiness{}, err
	}
	return integrationmodel.WebPushReadiness{Ready: value.Ready, PublicKey: value.PublicKey, ConnectionKey: value.ConnectionKey, Status: value.Status, Reason: value.Reason}, nil
}

func authorizeWebPushSelfService(principal principalmodel.Principal, command bool) error {
	var err error
	if command {
		err = integrationAuthorizeCommand(principal)
	} else {
		err = integrationAuthorizeQuery(principal)
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(principal.UserID) == "" {
		return forbidden("auth.permission_denied")
	}
	return nil
}

func (s *IntegrationApplicationService) CleanupExpiredWebPushSubscriptions(ctx context.Context, principal principalmodel.Principal) (int, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return 0, err
	}
	if !HasPermission(principal, PermissionConnectionManage) {
		return 0, forbidden("auth.permission_denied")
	}
	if s.ownerWebPushSubscriptions == nil {
		return 0, fmt.Errorf("backend.integration.notification.subscription_store_unavailable")
	}
	count, err := s.ownerWebPushSubscriptions.CleanupExpired(ctx, principalWorkspaceID(principal))
	if err == nil {
		s.audit(ctx, "web_push_subscriptions_cleaned", "web_push_subscription", "expired", principal, "Cleaned expired Web Push subscriptions", nil, map[string]any{"count": count}, map[string]any{"workspace_id": principalWorkspaceID(principal)})
	}
	return count, err
}

func runtimeWebPushSubscription(value integrationsdk.WebPushSubscription) integrationmodel.WebPushSubscription {
	return integrationmodel.WebPushSubscription{ID: value.ID, WorkspaceID: value.WorkspaceID, UserID: value.UserID, EndpointHash: value.EndpointHash, Status: value.Status, ExpiresAt: value.ExpiresAt, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, RevokedAt: value.RevokedAt}
}
