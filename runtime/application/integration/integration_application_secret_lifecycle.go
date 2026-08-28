package integration

import (
	"context"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"strings"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func (s *IntegrationApplicationService) DisableIntegrationSecret(ctx context.Context, secretKey string, principal principalmodel.Principal) (integrationmodel.IntegrationSecret, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return integrationmodel.IntegrationSecret{}, err
	}
	if err := ctx.Err(); err != nil {
		return integrationmodel.IntegrationSecret{}, err
	}
	if !HasPermission(principal, PermissionSecretManage) {
		return integrationmodel.IntegrationSecret{}, forbidden("auth.permission_denied")
	}
	workspaceID := principalWorkspaceID(principal)
	secret, ok, err := s.findSecret(ctx, secretKey, workspaceID)
	if err != nil {
		return integrationmodel.IntegrationSecret{}, err
	}
	if !ok {
		return integrationmodel.IntegrationSecret{}, notFound("backend.integration.secret.not_found")
	}
	before := secretAuditShape(secret)
	secret.Status, secret.DisabledAt = "disabled", time.Now().UTC().Format(time.RFC3339)
	saved, err := s.configRepo.UpsertSecret(ctx, secret.WorkspaceID, secret)
	if err != nil {
		return integrationmodel.IntegrationSecret{}, err
	}
	s.audit(ctx, "integration_secret_disabled", "integration_secret", saved.Key, principal, "Disabled integration secret "+saved.Key, before, secretAuditShape(saved), map[string]any{
		"workspace_id": workspaceID, "secret_key": saved.Key, "kind": saved.Kind, "status": saved.Status,
	})
	return saved, nil
}

func (s *IntegrationApplicationService) ExpireIntegrationSecret(ctx context.Context, secretKey string, principal principalmodel.Principal) (integrationmodel.IntegrationSecret, error) {
	return s.transitionIntegrationSecret(ctx, secretKey, "expired", principal)
}

func (s *IntegrationApplicationService) RevokeIntegrationSecret(ctx context.Context, secretKey string, principal principalmodel.Principal) (integrationmodel.IntegrationSecret, error) {
	return s.transitionIntegrationSecret(ctx, secretKey, "revoked", principal)
}

func (s *IntegrationApplicationService) transitionIntegrationSecret(ctx context.Context, secretKey, status string, principal principalmodel.Principal) (integrationmodel.IntegrationSecret, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return integrationmodel.IntegrationSecret{}, err
	}
	if err := ctx.Err(); err != nil {
		return integrationmodel.IntegrationSecret{}, err
	}
	if !HasPermission(principal, PermissionSecretManage) {
		return integrationmodel.IntegrationSecret{}, forbidden("auth.permission_denied")
	}
	secret, ok, err := s.findSecret(ctx, secretKey, principalWorkspaceID(principal))
	if err != nil {
		return integrationmodel.IntegrationSecret{}, err
	}
	if !ok {
		return integrationmodel.IntegrationSecret{}, notFound("backend.integration.secret.not_found")
	}
	before := secretAuditShape(secret)
	now := time.Now().UTC().Format(time.RFC3339)
	secret.Status = status
	if status == "expired" {
		secret.ExpiresAt = now
	} else if status == "revoked" {
		secret.RevokedAt = now
	}
	saved, err := s.configRepo.UpsertSecret(ctx, secret.WorkspaceID, secret)
	if err != nil {
		return integrationmodel.IntegrationSecret{}, err
	}
	s.audit(ctx, "integration_secret_"+status, "integration_secret", saved.Key, principal, "Updated integration secret lifecycle "+saved.Key, before, secretAuditShape(saved), map[string]any{
		"workspace_id": principalWorkspaceID(principal), "secret_key": saved.Key, "status": saved.Status,
	})
	return saved, nil
}

func (s *IntegrationApplicationService) findSecret(ctx context.Context, key, workspaceID string) (integrationmodel.IntegrationSecret, bool, error) {
	secrets, err := s.configRepo.ListSecrets(ctx, workspaceID)
	if err != nil {
		return integrationmodel.IntegrationSecret{}, false, err
	}
	key = strings.TrimSpace(key)
	for _, secret := range secrets {
		if secret.Key == key {
			return secret, true, nil
		}
	}
	return integrationmodel.IntegrationSecret{}, false, nil
}

func secretAuditShape(secret integrationmodel.IntegrationSecret) map[string]any {
	return map[string]any{
		"key": secret.Key, "workspace_id": secret.WorkspaceID, "kind": secret.Kind, "status": secret.Status,
		"description_set": strings.TrimSpace(secret.Description) != "", "value_ref_set": strings.TrimSpace(secret.ValueRef) != "",
		"fingerprint": secret.Fingerprint, "disabled": secret.Status == "disabled", "expires_at": secret.ExpiresAt,
		"rotated_at": secret.RotatedAt, "revoked_at": secret.RevokedAt, "last_tested_at": secret.LastTestedAt,
		"last_test_status": secret.LastTestStatus, "last_test_error_set": strings.TrimSpace(secret.LastTestError) != "",
	}
}
