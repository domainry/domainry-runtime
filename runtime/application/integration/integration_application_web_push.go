package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func (s *IntegrationApplicationService) ListWebPushSubscriptions(ctx context.Context, principal principalmodel.Principal) ([]integrationmodel.WebPushSubscription, error) {
	if err := authorizeWebPushSelfService(principal, false); err != nil {
		return nil, err
	}
	repository, ok := s.deliveryRepo.(integrationrepository.WebPushSubscriptionRepository)
	if !ok {
		return nil, fmt.Errorf("backend.integration.notification.subscription_store_unavailable")
	}
	return repository.ListWebPushSubscriptions(ctx, principalWorkspaceID(principal), principal.UserID)
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
	hash := sha256.Sum256([]byte(endpoint))
	value := integrationmodel.WebPushSubscription{ID: id, WorkspaceID: principalWorkspaceID(principal), UserID: principal.UserID, EndpointHash: hex.EncodeToString(hash[:]), Endpoint: endpoint, P256DH: strings.TrimSpace(request.P256DH), Auth: strings.TrimSpace(request.Auth), ExpiresAt: request.ExpiresAt}
	repository, ok := s.deliveryRepo.(integrationrepository.WebPushSubscriptionRepository)
	if !ok {
		return value, fmt.Errorf("backend.integration.notification.subscription_store_unavailable")
	}
	saved, err := repository.UpsertWebPushSubscription(ctx, value.WorkspaceID, value)
	if err != nil {
		return value, err
	}
	s.audit(ctx, "web_push_subscription_upserted", "web_push_subscription", saved.ID, principal, "Created or updated Web Push subscription", nil, map[string]any{"status": saved.Status, "endpoint_hash": saved.EndpointHash}, map[string]any{"workspace_id": saved.WorkspaceID, "user_id": saved.UserID})
	return saved, nil
}

func (s *IntegrationApplicationService) RevokeWebPushSubscription(ctx context.Context, id string, principal principalmodel.Principal) (integrationmodel.WebPushSubscription, error) {
	if err := authorizeWebPushSelfService(principal, true); err != nil {
		return integrationmodel.WebPushSubscription{}, err
	}
	repository, ok := s.deliveryRepo.(integrationrepository.WebPushSubscriptionRepository)
	if !ok {
		return integrationmodel.WebPushSubscription{}, fmt.Errorf("backend.integration.notification.subscription_store_unavailable")
	}
	saved, err := repository.RevokeWebPushSubscription(ctx, principalWorkspaceID(principal), strings.TrimSpace(id), principal.UserID)
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
	connections, err := s.configRepo.ListConnections(ctx, principalWorkspaceID(principal))
	if err != nil {
		return integrationmodel.WebPushReadiness{}, err
	}
	for _, connection := range connections {
		if connection.ConnectorKey != "notification" || connection.ProviderKey != "web_push" {
			continue
		}
		publicKey := strings.TrimSpace(fmt.Sprint(connection.Config["vapid_public_key"]))
		result := integrationmodel.WebPushReadiness{PublicKey: publicKey, ConnectionKey: connection.Key, Status: connection.Status}
		if publicKey == "" {
			result.Reason = "public_key_missing"
			return result, nil
		}
		secretRef := strings.TrimSpace(connection.SecretRefs["vapid_private_key"])
		if !strings.HasPrefix(secretRef, "secret:") {
			result.Reason = "private_key_unbound"
			return result, nil
		}
		secret, found, findErr := s.findSecret(ctx, strings.TrimSpace(strings.TrimPrefix(secretRef, "secret:")), principalWorkspaceID(principal))
		if findErr != nil {
			return integrationmodel.WebPushReadiness{}, findErr
		}
		if !found || secret.Status != "active" {
			result.Reason = "private_key_unavailable"
			return result, nil
		}
		if connection.Status != "active" && connection.Status != "verified" {
			result.Reason = "connection_not_ready"
			return result, nil
		}
		result.Ready = true
		return result, nil
	}
	return integrationmodel.WebPushReadiness{Status: "unconfigured", Reason: "connection_missing"}, nil
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
	repository, ok := s.deliveryRepo.(integrationrepository.WebPushSubscriptionRepository)
	if !ok {
		return 0, fmt.Errorf("backend.integration.notification.subscription_store_unavailable")
	}
	count, err := repository.CleanupExpiredWebPushSubscriptions(ctx, principalWorkspaceID(principal))
	if err == nil {
		s.audit(ctx, "web_push_subscriptions_cleaned", "web_push_subscription", "expired", principal, "Cleaned expired Web Push subscriptions", nil, map[string]any{"count": count}, map[string]any{"workspace_id": principalWorkspaceID(principal)})
	}
	return count, err
}

func (s *IntegrationApplicationService) hydrateWebPushSubscription(ctx context.Context, workspaceID string, payload map[string]any) (map[string]any, error) {
	id := strings.TrimSpace(fmt.Sprint(payload["subscription_id"]))
	repository, ok := s.deliveryRepo.(integrationrepository.WebPushSubscriptionRepository)
	if !ok {
		return nil, fmt.Errorf("backend.integration.notification.subscription_store_unavailable")
	}
	subscription, found, err := repository.GetWebPushSubscription(ctx, workspaceID, id)
	if err != nil {
		return nil, err
	}
	if !found || subscription.Status != "active" || subscription.Endpoint == "" {
		return nil, badRequest("backend.integration.notification.subscription_unavailable")
	}
	if subscription.ExpiresAt != "" {
		if expires, parseErr := time.Parse(time.RFC3339, subscription.ExpiresAt); parseErr == nil && !expires.After(time.Now().UTC()) {
			_ = repository.ExpireWebPushSubscription(ctx, workspaceID, id)
			return nil, badRequest("backend.integration.notification.subscription_expired")
		}
	}
	hydrated := cloneMap(payload)
	hydrated["endpoint"], hydrated["p256dh"], hydrated["auth"] = subscription.Endpoint, subscription.P256DH, subscription.Auth
	return hydrated, nil
}
