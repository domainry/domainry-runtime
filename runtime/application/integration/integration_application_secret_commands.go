package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"strings"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func (s *IntegrationApplicationService) UpsertIntegrationSecret(ctx context.Context, secretKey string, req integrationmodel.IntegrationSecretUpsertRequest, principal principalmodel.Principal) (integrationmodel.IntegrationSecret, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return integrationmodel.IntegrationSecret{}, err
	}
	if err := ctx.Err(); err != nil {
		return integrationmodel.IntegrationSecret{}, err
	}
	if !HasPermission(principal, PermissionSecretManage) {
		return integrationmodel.IntegrationSecret{}, forbidden("auth.permission_denied")
	}
	key := strings.TrimSpace(secretKey)
	if key == "" {
		key = strings.TrimSpace(req.Key)
	}
	if key == "" {
		return integrationmodel.IntegrationSecret{}, badRequest("backend.integration.secret.missing_key")
	}
	kind, err := normalizeSecretKind(req.Kind)
	if err != nil {
		return integrationmodel.IntegrationSecret{}, err
	}
	status, err := normalizeSecretStatus(req.Status)
	if err != nil {
		return integrationmodel.IntegrationSecret{}, err
	}
	expiresAt, err := normalizeSecretExpiry(req.ExpiresAt)
	if err != nil {
		return integrationmodel.IntegrationSecret{}, err
	}
	if status == "active" && SecretExpired(expiresAt, time.Now().UTC()) {
		status = "expired"
	}
	valueRef, fingerprint, err := secretMaterial(req)
	if err != nil {
		return integrationmodel.IntegrationSecret{}, err
	}
	workspaceID := principalWorkspaceID(principal)
	if strings.TrimSpace(req.Value) != "" {
		if err := s.PersistSecretMaterial(ctx, workspaceID, key, strings.TrimSpace(req.Value)); err != nil {
			return integrationmodel.IntegrationSecret{}, err
		}
		valueRef = "material:" + key
	}
	existing, existed, err := s.findSecret(ctx, key, workspaceID)
	if err != nil {
		return integrationmodel.IntegrationSecret{}, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	secret := integrationmodel.IntegrationSecret{
		Key: key, WorkspaceID: workspaceID, Kind: kind, Status: status,
		Description: strings.TrimSpace(req.Description), ValueRef: valueRef, Fingerprint: fingerprint,
		ExpiresAt: expiresAt, CreatedBy: strings.TrimSpace(principal.UserID),
	}
	if status == "disabled" {
		secret.DisabledAt = now
	}
	if status == "revoked" {
		secret.RevokedAt = now
	}
	saved, err := s.configRepo.UpsertSecret(ctx, secret.WorkspaceID, secret)
	if err != nil {
		return integrationmodel.IntegrationSecret{}, err
	}
	var before map[string]any
	if existed {
		before = secretAuditShape(existing)
	}
	s.audit(ctx, "integration_secret_upserted", "integration_secret", saved.Key, principal, "Upserted integration secret "+saved.Key, before, secretAuditShape(saved), map[string]any{
		"workspace_id": workspaceID, "secret_key": saved.Key, "kind": saved.Kind, "status": saved.Status,
	})
	return saved, nil
}

func (s *IntegrationApplicationService) RotateIntegrationSecret(ctx context.Context, secretKey string, req integrationmodel.IntegrationSecretUpsertRequest, principal principalmodel.Principal) (integrationmodel.IntegrationSecret, error) {
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
	existing, ok, err := s.findSecret(ctx, secretKey, workspaceID)
	if err != nil {
		return integrationmodel.IntegrationSecret{}, err
	}
	if !ok {
		return integrationmodel.IntegrationSecret{}, notFound("backend.integration.secret.not_found")
	}
	valueRef, fingerprint, err := secretMaterial(req)
	if err != nil {
		return integrationmodel.IntegrationSecret{}, err
	}
	rawMaterial := strings.TrimSpace(req.Value)
	if rawMaterial != "" {
		valueRef = "material:" + existing.Key
	}
	status := "active"
	if strings.TrimSpace(req.Status) != "" {
		status, err = normalizeSecretStatus(req.Status)
		if err != nil {
			return integrationmodel.IntegrationSecret{}, err
		}
	}
	previous := existing
	before := secretAuditShape(previous)
	existing.Status, existing.ValueRef, existing.Fingerprint = status, valueRef, fingerprint
	existing.DisabledAt, existing.RevokedAt = "", ""
	now := time.Now().UTC()
	existing.RotatedAt = now.Format(time.RFC3339)
	if strings.TrimSpace(req.ExpiresAt) != "" {
		existing.ExpiresAt, err = normalizeSecretExpiry(req.ExpiresAt)
		if err != nil {
			return integrationmodel.IntegrationSecret{}, err
		}
	}
	if status == "active" && SecretExpired(existing.ExpiresAt, now) {
		existing.Status = "expired"
	}
	if strings.TrimSpace(req.Kind) != "" {
		existing.Kind, err = normalizeSecretKind(req.Kind)
		if err != nil {
			return integrationmodel.IntegrationSecret{}, err
		}
	}
	if strings.TrimSpace(req.Description) != "" {
		existing.Description = strings.TrimSpace(req.Description)
	}
	event, notify, err := s.compileCredentialRecoveryNotification(previous, existing, now)
	if err != nil {
		return integrationmodel.IntegrationSecret{}, err
	}
	var saved integrationmodel.IntegrationSecret
	if notify {
		saved, err = s.credentialNotifications.CommitIntegrationSecretNotification(ctx, existing, rawMaterial, event)
	} else {
		if rawMaterial != "" {
			if err := s.PersistSecretMaterial(ctx, workspaceID, existing.Key, rawMaterial); err != nil {
				return integrationmodel.IntegrationSecret{}, err
			}
		}
		saved, err = s.configRepo.UpsertSecret(ctx, existing.WorkspaceID, existing)
	}
	if err != nil {
		return integrationmodel.IntegrationSecret{}, err
	}
	s.audit(ctx, "integration_secret_rotated", "integration_secret", saved.Key, principal, "Rotated integration secret "+saved.Key, before, secretAuditShape(saved), map[string]any{
		"workspace_id": workspaceID, "secret_key": saved.Key, "kind": saved.Kind, "status": saved.Status,
	})
	return saved, nil
}

func normalizeSecretStatus(value string) (string, error) {
	switch status := strings.TrimSpace(value); status {
	case "":
		return "active", nil
	case "active", "disabled", "expired", "revoked":
		return status, nil
	case "rotating":
		return "active", nil
	default:
		return "", badRequest("backend.integration.secret.invalid_status")
	}
}

func normalizeSecretExpiry(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "", badRequest("backend.integration.secret.expires_at_invalid")
	}
	return parsed.UTC().Format(time.RFC3339), nil
}

func normalizeSecretKind(value string) (string, error) {
	kind := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), "-", "_")
	switch kind {
	case "":
		return "api_key", nil
	case "api_key", "bearer_token", "basic_auth_password", "oauth_client_secret", "webhook_secret", "signing_secret", "refresh_token", "private_key", "certificate", "database_password", "connection_string", "service_account", "identifier", "generic_secret":
		return kind, nil
	case "bearer":
		return "bearer_token", nil
	case "basic_auth", "password":
		return "basic_auth_password", nil
	case "oauth_secret", "client_secret":
		return "oauth_client_secret", nil
	case "webhook":
		return "webhook_secret", nil
	case "signing":
		return "signing_secret", nil
	case "db_password":
		return "database_password", nil
	default:
		return "", badRequest("backend.integration.secret.invalid_kind")
	}
}

func secretMaterial(req integrationmodel.IntegrationSecretUpsertRequest) (string, string, error) {
	valueRef, rawValue := strings.TrimSpace(req.ValueRef), strings.TrimSpace(req.Value)
	if valueRef == "" && rawValue == "" {
		return "", "", badRequest("backend.integration.secret.missing_material")
	}
	if valueRef != "" && !strings.HasPrefix(valueRef, "env:") && !strings.HasPrefix(valueRef, "secret:") {
		return "", "", badRequest("backend.integration.secret.value_ref_must_be_reference")
	}
	source := rawValue
	if source == "" {
		source = valueRef
	}
	sum := sha256.Sum256([]byte(source))
	return valueRef, hex.EncodeToString(sum[:]), nil
}
