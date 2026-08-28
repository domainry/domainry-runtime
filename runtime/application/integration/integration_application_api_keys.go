package integration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"sort"
	"strconv"
	"strings"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func (s *IntegrationApplicationService) CreateIntegrationAPIKey(ctx context.Context, req integrationmodel.IntegrationAPIKeyCreateRequest, principal principalmodel.Principal) (integrationmodel.IntegrationAPIKeyCreateResult, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return integrationmodel.IntegrationAPIKeyCreateResult{}, err
	}
	if !principal.HasPermission("workspace.admin") {
		return integrationmodel.IntegrationAPIKeyCreateResult{}, forbidden("auth.permission_denied")
	}
	actorID, roleKey := strings.TrimSpace(req.ActorID), strings.TrimSpace(req.RoleKey)
	if actorID == "" {
		return integrationmodel.IntegrationAPIKeyCreateResult{}, badRequest("backend.integration.api_key.missing_actor")
	}
	if roleKey == "" {
		return integrationmodel.IntegrationAPIKeyCreateResult{}, badRequest("backend.integration.api_key.missing_role")
	}
	actor := s.resolvePrincipal(ctx, actorID, roleKey, "")
	if !actor.Known {
		return integrationmodel.IntegrationAPIKeyCreateResult{}, badRequest("backend.integration.api_key.unknown_role")
	}
	scopes, err := normalizeAPIKeyScopes(req.Scopes)
	if err != nil {
		return integrationmodel.IntegrationAPIKeyCreateResult{}, err
	}
	if !apiKeyScopesAllowed(actor, scopes) {
		return integrationmodel.IntegrationAPIKeyCreateResult{}, badRequest("backend.integration.api_key.invalid_scope")
	}
	if strings.TrimSpace(req.ExpiresAt) != "" {
		if _, err := time.Parse(time.RFC3339, strings.TrimSpace(req.ExpiresAt)); err != nil {
			return integrationmodel.IntegrationAPIKeyCreateResult{}, badRequest("backend.integration.api_key.invalid_expires_at")
		}
	}
	token, tokenPrefix, tokenHash := generateAPIKeyToken()
	key := strings.TrimSpace(req.Key)
	if key == "" {
		key = sanitizeKey(valueOrDefault(req.Name, "api_key") + "_" + fmt.Sprint(time.Now().UTC().UnixNano()))
	}
	apiKey := integrationmodel.IntegrationAPIKey{
		Key: key, WorkspaceID: principalWorkspaceID(principal), Name: strings.TrimSpace(req.Name),
		TokenPrefix: tokenPrefix, TokenHash: tokenHash, ActorID: actorID, RoleKey: roleKey, Scopes: scopes,
		Status: "active", ExpiresAt: strings.TrimSpace(req.ExpiresAt), CreatedBy: strings.TrimSpace(principal.UserID),
	}
	saved, err := s.configRepo.UpsertAPIKey(ctx, apiKey.WorkspaceID, apiKey)
	if err != nil {
		return integrationmodel.IntegrationAPIKeyCreateResult{}, err
	}
	s.audit(ctx, "integration_api_key_created", "integration_api_key", saved.Key, principal, "Created integration API key "+saved.Key, nil, apiKeyAuditShape(saved), map[string]any{
		"workspace_id": principalWorkspaceID(principal), "api_key": saved.Key, "actor_id": saved.ActorID,
		"role_key": saved.RoleKey, "scope_count": len(saved.Scopes), "expires_set": strings.TrimSpace(saved.ExpiresAt) != "",
	})
	return integrationmodel.IntegrationAPIKeyCreateResult{APIKey: saved, Token: token}, nil
}

func (s *IntegrationApplicationService) PrincipalFromIntegrationAPIKey(ctx context.Context, token, workspaceID, requestID string) (principalmodel.Principal, integrationmodel.IntegrationAPIKey, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if err := integrationAuthorizeWorkspaceQuery(workspaceID); err != nil {
		return principalmodel.Principal{}, integrationmodel.IntegrationAPIKey{}, err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return principalmodel.Principal{}, integrationmodel.IntegrationAPIKey{}, forbidden("auth.token_required")
	}
	apiKey, ok, err := s.configRepo.FindAPIKeyByTokenHash(ctx, workspaceID, apiKeyTokenHash(token))
	if err != nil {
		return principalmodel.Principal{}, integrationmodel.IntegrationAPIKey{}, err
	}
	if !ok || apiKey.Status != "active" {
		return principalmodel.Principal{}, integrationmodel.IntegrationAPIKey{}, forbidden("auth.invalid_token")
	}
	if apiKey.WorkspaceID != workspaceID {
		return principalmodel.Principal{}, integrationmodel.IntegrationAPIKey{}, forbidden("auth.invalid_token")
	}
	if strings.TrimSpace(apiKey.ExpiresAt) != "" {
		expiresAt, err := time.Parse(time.RFC3339, strings.TrimSpace(apiKey.ExpiresAt))
		if err != nil || !expiresAt.After(time.Now().UTC()) {
			return principalmodel.Principal{}, integrationmodel.IntegrationAPIKey{}, forbidden("auth.session_expired")
		}
	}
	principal := s.resolvePrincipal(ctx, apiKey.ActorID, apiKey.RoleKey, "")
	if !principal.Known {
		return principalmodel.Principal{}, integrationmodel.IntegrationAPIKey{}, forbidden("auth.invalid_token")
	}
	if err := s.checkAPIKeyRateLimit(ctx, apiKey); err != nil {
		s.audit(ctx, "integration_api_key_rate_limited", "integration_api_key", apiKey.Key, principal, "Rate limited integration API key "+apiKey.Key, nil, apiKeyAuditShape(apiKey), map[string]any{
			"workspace_id": apiKey.WorkspaceID, "api_key": apiKey.Key, "actor_id": apiKey.ActorID, "role_key": apiKey.RoleKey,
		})
		return principalmodel.Principal{}, integrationmodel.IntegrationAPIKey{}, err
	}
	principal.WorkspaceID, principal.RequestID = apiKey.WorkspaceID, requestID
	if principal.AccessBundle == nil {
		return principalmodel.Principal{}, integrationmodel.IntegrationAPIKey{}, forbidden("identity.access_bundle_required")
	}
	restricted := identitysdk.RestrictAccess(*principal.AccessBundle, apiKey.Scopes)
	principal.AccessBundle = &restricted
	if updated, err := s.configRepo.UpdateAPIKeyLastUsed(ctx, apiKey.WorkspaceID, apiKey.Key, time.Now().UTC().Format(time.RFC3339)); err == nil {
		apiKey = updated
	}
	s.audit(ctx, "integration_api_key_used", "integration_api_key", apiKey.Key, principal, "Used integration API key "+apiKey.Key, nil, apiKeyAuditShape(apiKey), map[string]any{
		"workspace_id": apiKey.WorkspaceID, "api_key": apiKey.Key, "actor_id": apiKey.ActorID,
		"role_key": apiKey.RoleKey, "scope_count": len(apiKey.Scopes),
	})
	return principal, apiKey, nil
}

func normalizeAPIKeyScopes(scopes []string) ([]string, error) {
	if len(scopes) == 0 {
		return []string{"*"}, nil
	}
	seen, out := map[string]bool{}, []string{}
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			return nil, badRequest("backend.integration.api_key.invalid_scope")
		}
		if !seen[scope] {
			seen[scope] = true
			out = append(out, scope)
		}
	}
	sort.Strings(out)
	return out, nil
}

func apiKeyScopesAllowed(principal principalmodel.Principal, scopes []string) bool {
	for _, scope := range scopes {
		if strings.TrimSpace(scope) == "*" || validRateLimitScope(scope) || strings.TrimSpace(scope) == "integration.entrypoint.invoke" {
			continue
		}
		if !principal.HasPermission(scope) {
			return false
		}
	}
	return true
}

func validRateLimitScope(scope string) bool {
	scope = strings.TrimSpace(scope)
	if !strings.HasPrefix(scope, "rate_limit:") {
		return false
	}
	parts := strings.Split(strings.ToLower(strings.TrimSpace(strings.TrimPrefix(scope, "rate_limit:"))), "/")
	if len(parts) != 2 {
		return false
	}
	limit, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil || limit <= 0 {
		return false
	}
	window := strings.TrimSpace(parts[1])
	switch window {
	case "s", "sec", "second", "seconds", "m", "min", "minute", "minutes", "h", "hr", "hour", "hours":
		return true
	default:
		parsed, err := time.ParseDuration(window)
		return err == nil && parsed > 0
	}
}

func (s *IntegrationApplicationService) DisableIntegrationAPIKey(ctx context.Context, key string, principal principalmodel.Principal) (integrationmodel.IntegrationAPIKey, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return integrationmodel.IntegrationAPIKey{}, err
	}
	if !principal.HasPermission("workspace.admin") {
		return integrationmodel.IntegrationAPIKey{}, forbidden("auth.permission_denied")
	}
	workspaceID := principalWorkspaceID(principal)
	apiKey, ok, err := s.findAPIKey(ctx, key, workspaceID)
	if err != nil {
		return integrationmodel.IntegrationAPIKey{}, err
	}
	if !ok {
		return integrationmodel.IntegrationAPIKey{}, notFound("backend.integration.api_key.not_found")
	}
	before := apiKeyAuditShape(apiKey)
	apiKey.Status, apiKey.DisabledAt = "disabled", time.Now().UTC().Format(time.RFC3339)
	saved, err := s.configRepo.UpsertAPIKey(ctx, apiKey.WorkspaceID, apiKey)
	if err != nil {
		return integrationmodel.IntegrationAPIKey{}, err
	}
	s.audit(ctx, "integration_api_key_disabled", "integration_api_key", saved.Key, principal, "Disabled integration API key "+saved.Key, before, apiKeyAuditShape(saved), map[string]any{
		"workspace_id": workspaceID, "api_key": saved.Key, "actor_id": saved.ActorID, "role_key": saved.RoleKey,
	})
	return saved, nil
}

func (s *IntegrationApplicationService) RotateIntegrationAPIKey(ctx context.Context, key string, principal principalmodel.Principal) (integrationmodel.IntegrationAPIKeyRotateResult, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return integrationmodel.IntegrationAPIKeyRotateResult{}, err
	}
	if !principal.HasPermission("workspace.admin") {
		return integrationmodel.IntegrationAPIKeyRotateResult{}, forbidden("auth.permission_denied")
	}
	workspaceID := principalWorkspaceID(principal)
	apiKey, ok, err := s.findAPIKey(ctx, key, workspaceID)
	if err != nil {
		return integrationmodel.IntegrationAPIKeyRotateResult{}, err
	}
	if !ok {
		return integrationmodel.IntegrationAPIKeyRotateResult{}, notFound("backend.integration.api_key.not_found")
	}
	token, tokenPrefix, tokenHash := generateAPIKeyToken()
	before := apiKeyAuditShape(apiKey)
	apiKey.TokenPrefix, apiKey.TokenHash, apiKey.Status, apiKey.DisabledAt = tokenPrefix, tokenHash, "active", ""
	saved, err := s.configRepo.UpsertAPIKey(ctx, apiKey.WorkspaceID, apiKey)
	if err != nil {
		return integrationmodel.IntegrationAPIKeyRotateResult{}, err
	}
	s.audit(ctx, "integration_api_key_rotated", "integration_api_key", saved.Key, principal, "Rotated integration API key "+saved.Key, before, apiKeyAuditShape(saved), map[string]any{
		"workspace_id": workspaceID, "api_key": saved.Key, "actor_id": saved.ActorID, "role_key": saved.RoleKey,
	})
	return integrationmodel.IntegrationAPIKeyRotateResult{APIKey: saved, Token: token}, nil
}

func (s *IntegrationApplicationService) findAPIKey(ctx context.Context, key, workspaceID string) (integrationmodel.IntegrationAPIKey, bool, error) {
	keys, err := s.configRepo.ListAPIKeys(ctx, workspaceID)
	if err != nil {
		return integrationmodel.IntegrationAPIKey{}, false, err
	}
	key = strings.TrimSpace(key)
	for _, candidate := range keys {
		if candidate.Key == key {
			return candidate, true, nil
		}
	}
	return integrationmodel.IntegrationAPIKey{}, false, nil
}

func generateAPIKeyToken() (string, string, string) {
	// crypto/rand.Text returns a cryptographically random token and has no
	// recoverable error path, so callers cannot accidentally leave an
	// untestable failure branch around the operating-system random source.
	token := APIKeyTokenPrefix + rand.Text()
	prefix := token[:len(APIKeyTokenPrefix)+12]
	return token, prefix, apiKeyTokenHash(token)
}

func apiKeyTokenHash(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

func apiKeyAuditShape(apiKey integrationmodel.IntegrationAPIKey) map[string]any {
	scopes := apiKey.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	return map[string]any{
		"key": apiKey.Key, "workspace_id": apiKey.WorkspaceID, "name": apiKey.Name, "token_prefix": apiKey.TokenPrefix,
		"actor_id": apiKey.ActorID, "role_key": apiKey.RoleKey, "scopes": scopes, "status": apiKey.Status,
		"expires_at": apiKey.ExpiresAt, "last_used_at": apiKey.LastUsedAt, "created_by": apiKey.CreatedBy,
		"created_at": apiKey.CreatedAt, "updated_at": apiKey.UpdatedAt, "disabled_at": apiKey.DisabledAt,
	}
}
