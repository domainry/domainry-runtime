package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	"os"
	"strings"
	"time"
)

func ResolveSecretRef(secretRef string) (string, error) {
	secretRef = strings.TrimSpace(secretRef)
	if !strings.HasPrefix(secretRef, "env:") {
		return "", badRequest("backend.integration.secret_ref_must_be_env")
	}
	envKey := strings.TrimSpace(strings.TrimPrefix(secretRef, "env:"))
	if envKey == "" {
		return "", badRequest("backend.integration.missing_env_key")
	}
	value := strings.TrimSpace(os.Getenv(envKey))
	if value == "" {
		return "", badRequest("backend.integration.secret_not_configured")
	}
	return value, nil
}

// ResolveAdapterSecrets resolves every connection-scoped secret reference at
// the Runtime integration boundary. Adapters receive only material selected by
// their Provider descriptor and never resolve process environment themselves.
func (s *IntegrationApplicationService) ResolveAdapterSecrets(ctx context.Context, connection integrationmodel.IntegrationConnection) (map[string]string, error) {
	if err := integrationAuthorizeWorkspaceQuery(connection.WorkspaceID); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	needsStoredSecret := false
	for _, reference := range connection.SecretRefs {
		reference = strings.TrimSpace(reference)
		switch {
		case strings.HasPrefix(reference, "env:"):
		case strings.HasPrefix(reference, "secret:"):
			needsStoredSecret = true
		default:
			return nil, badRequest("backend.integration.secret_ref.must_be_reference")
		}
	}
	byKey := map[string]integrationmodel.IntegrationSecret{}
	if needsStoredSecret {
		secrets, err := s.configRepo.ListSecrets(ctx, connection.WorkspaceID)
		if err != nil {
			return nil, err
		}
		byKey = make(map[string]integrationmodel.IntegrationSecret, len(secrets))
		for _, secret := range secrets {
			byKey[secret.Key] = secret
		}
	}

	resolved := make(map[string]string, len(connection.SecretRefs))
	for name, reference := range connection.SecretRefs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		reference = strings.TrimSpace(reference)
		if strings.HasPrefix(reference, "env:") {
			value, err := ResolveSecretRef(reference)
			if err != nil {
				return nil, err
			}
			resolved[name] = value
			continue
		}
		// The validation pass above guarantees every non-environment reference
		// is a persisted secret reference.
		secretKey := strings.TrimSpace(strings.TrimPrefix(reference, "secret:"))
		secret, ok := byKey[secretKey]
		if !ok || secret.Status != "active" || SecretExpired(secret.ExpiresAt, time.Now().UTC()) {
			return nil, badRequest("backend.integration.secret.unavailable", "secret_key", secretKey)
		}
		value, err := s.ResolveSecretMaterial(ctx, connection.WorkspaceID, secret)
		if err != nil {
			return nil, err
		}
		resolved[name] = value
	}
	return resolved, nil
}

func SecretExpired(expiresAt string, now time.Time) bool {
	if strings.TrimSpace(expiresAt) == "" {
		return false
	}
	parsed, err := time.Parse(time.RFC3339, expiresAt)
	return err != nil || !parsed.After(now)
}

func (s *IntegrationApplicationService) ResolveSecretMaterial(ctx context.Context, workspaceID string, secret integrationmodel.IntegrationSecret) (string, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	if err := integrationAuthorizeWorkspaceQuery(workspaceID); err != nil {
		return "", err
	}
	if strings.HasPrefix(secret.ValueRef, "env:") {
		return ResolveSecretRef(secret.ValueRef)
	}
	if !strings.HasPrefix(secret.ValueRef, "material:") {
		return "", badRequest("backend.integration.secret.material_unavailable", "secret_key", secret.Key)
	}
	repository, ok := s.configRepo.(integrationrepository.IntegrationSecretMaterialRepository)
	if !ok {
		return "", fmt.Errorf("backend.integration.secret.material_repository_unavailable")
	}
	return repository.ResolveSecretMaterial(ctx, workspaceID, secret.Key)
}

func (s *IntegrationApplicationService) PersistSecretMaterial(ctx context.Context, workspaceID, secretKey, value string) error {
	workspaceID = strings.TrimSpace(workspaceID)
	if err := integrationAuthorizeWorkspaceCommand(workspaceID); err != nil {
		return err
	}
	if strings.TrimSpace(value) == "" {
		return nil
	}
	repository, ok := s.configRepo.(integrationrepository.IntegrationSecretMaterialRepository)
	if !ok {
		return fmt.Errorf("backend.integration.secret.material_repository_unavailable")
	}
	return repository.PutSecretMaterial(ctx, workspaceID, secretKey, value)
}

// PersistAdapterSecretUpdates applies provider-issued credential rotation only
// when the material observed by the provider still matches persisted state.
// This prevents a stale concurrent refresh from overwriting newer material.
func (s *IntegrationApplicationService) PersistAdapterSecretUpdates(ctx context.Context, connection integrationmodel.IntegrationConnection, previous, updates map[string]string) error {
	if err := integrationAuthorizeWorkspaceCommand(connection.WorkspaceID); err != nil {
		return err
	}
	if len(updates) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	repository, ok := s.configRepo.(integrationrepository.IntegrationSecretMaterialRepository)
	if !ok {
		return fmt.Errorf("integration secret material repository is not configured")
	}
	workspaceID := strings.TrimSpace(connection.WorkspaceID)
	for name, material := range updates {
		if err := ctx.Err(); err != nil {
			return err
		}
		material = strings.TrimSpace(material)
		if material == "" {
			continue
		}
		reference := strings.TrimSpace(connection.SecretRefs[name])
		if !strings.HasPrefix(reference, "secret:") {
			return badRequest("backend.integration.secret.rotation_requires_runtime_secret", "secret_ref_name", name)
		}
		secretKey := strings.TrimSpace(strings.TrimPrefix(reference, "secret:"))
		secret, found, err := s.findSecret(ctx, secretKey, workspaceID)
		if err != nil {
			return err
		}
		if !found || secret.Status != "active" {
			return badRequest("backend.integration.secret.unavailable", "secret_key", secretKey)
		}
		if old := strings.TrimSpace(previous[name]); old != "" {
			current, err := s.ResolveSecretMaterial(ctx, workspaceID, secret)
			if err != nil {
				return err
			}
			if current != old {
				continue
			}
		}
		if err := repository.PutSecretMaterial(ctx, workspaceID, secretKey, material); err != nil {
			return err
		}
		sum := sha256.Sum256([]byte(material))
		secret.ValueRef = "material:" + secretKey
		secret.Fingerprint = hex.EncodeToString(sum[:])
		secret.Status, secret.DisabledAt = "active", ""
		secret.RotatedAt = time.Now().UTC().Format(time.RFC3339)
		secret.RevokedAt = ""
		if _, err := s.configRepo.UpsertSecret(ctx, secret.WorkspaceID, secret); err != nil {
			return err
		}
	}
	return nil
}

func RedactSecretText(value string, secrets map[string]string) string {
	redacted := value
	for _, secret := range secrets {
		if strings.TrimSpace(secret) != "" {
			redacted = strings.ReplaceAll(redacted, secret, "[REDACTED]")
		}
	}
	return redacted
}

// RedactProviderResponse preserves protocol cursors while suppressing
// credential-shaped output and resolved secret values.
func RedactProviderResponse(value map[string]any, secrets map[string]string) map[string]any {
	if value == nil {
		return nil
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		if isProviderCredentialKey(key) {
			out[key] = "[REDACTED]"
			continue
		}
		out[key] = redactProviderResponseValue(item, secrets)
	}
	return out
}

func redactProviderResponseValue(value any, secrets map[string]string) any {
	switch typed := value.(type) {
	case string:
		return RedactSecretText(typed, secrets)
	case map[string]any:
		return RedactProviderResponse(typed, secrets)
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = redactProviderResponseValue(item, secrets)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(typed))
		for index, item := range typed {
			out[index] = RedactProviderResponse(item, secrets)
		}
		return out
	default:
		return value
	}
}

func isProviderCredentialKey(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(key), "-", "_"), " ", "_"))
	for _, marker := range []string{
		"password", "secret", "api_key", "apikey", "access_key", "private_key",
		"credential", "authorization", "cookie", "session_id", "access_token",
		"refresh_token", "id_token",
	} {
		if normalized == marker || strings.HasSuffix(normalized, "_"+marker) {
			return true
		}
	}
	return false
}
