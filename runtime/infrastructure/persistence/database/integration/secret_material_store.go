package integration

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/secrets"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func (r IntegrationConfigStore) PutSecretMaterial(ctx context.Context, workspaceID, secretKey, value string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var err error
	workspaceID, err = requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return err
	}
	secretKey = strings.TrimSpace(secretKey)
	if secretKey == "" || value == "" {
		return fmt.Errorf("integration secret material missing identity or value")
	}
	ciphertext, err := r.encryptSecretMaterial(ctx, workspaceID, secretKey, value)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	createdAt := now
	executor := database.ActionExecutionExecutor(r.db)
	if actionExecutor := database.ActionExecutionTransaction(ctx); actionExecutor != nil {
		executor = actionExecutor
	}
	err = executor.QueryRowContext(ctx, "SELECT "+r.store.Identifier("created_at")+" FROM "+r.store.TableIdentifier("integration_secret_materials")+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(1)+" AND "+r.store.Identifier("secret_key")+" = "+r.store.Placeholder(2), workspaceID, secretKey).Scan(&createdAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read integration secret material: %w", err)
	}
	columns := []string{"id", "workspace_id", "secret_key", "ciphertext", "created_at", "updated_at"}
	args := []any{"integration_secret_material:" + workspaceID + ":" + secretKey, workspaceID, secretKey, ciphertext, createdAt, now}
	return r.replaceRow(ctx, "integration_secret_materials", "secret_key", workspaceID, secretKey, columns, args, "integration secret material")
}

func (r IntegrationConfigStore) ResolveSecretMaterial(ctx context.Context, workspaceID, secretKey string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var err error
	workspaceID, err = requireIntegrationWorkspaceID(workspaceID)
	if err != nil {
		return "", err
	}
	secretKey = strings.TrimSpace(secretKey)
	var ciphertext string
	if err := r.db.QueryRowContext(ctx, "SELECT "+r.store.Identifier("ciphertext")+" FROM "+r.store.TableIdentifier("integration_secret_materials")+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(1)+" AND "+r.store.Identifier("secret_key")+" = "+r.store.Placeholder(2), workspaceID, secretKey).Scan(&ciphertext); err != nil {
		return "", fmt.Errorf("resolve integration secret material: %w", err)
	}
	return r.decryptSecretMaterial(ctx, workspaceID, secretKey, ciphertext)
}

func (r IntegrationConfigStore) encryptSecretMaterial(ctx context.Context, workspaceID, secretKey, value string) (string, error) {
	return (secrets.Cipher{Keys: r.store.SecretKeyProvider(), Purpose: "integration-secret"}).Encrypt(ctx, workspaceID, secretKey, []byte(value))
}

func (r IntegrationConfigStore) decryptSecretMaterial(ctx context.Context, workspaceID, secretKey, encoded string) (string, error) {
	if strings.HasPrefix(encoded, secrets.EnvelopeVersion+":") {
		plain, err := (secrets.Cipher{Keys: r.store.SecretKeyProvider(), Purpose: "integration-secret"}).Decrypt(ctx, workspaceID, secretKey, encoded)
		if err != nil {
			return "", err
		}
		return string(plain), nil
	}
	aead, _ := r.secretAEAD()
	if !strings.HasPrefix(encoded, "v1:") {
		return "", fmt.Errorf("integration secret ciphertext version unsupported")
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(encoded, "v1:"))
	if err != nil || len(raw) < aead.NonceSize() {
		return "", fmt.Errorf("integration secret ciphertext invalid")
	}
	plain, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte(workspaceID+"\x00"+secretKey))
	if err != nil {
		return "", fmt.Errorf("decrypt integration secret material: %w", err)
	}
	return string(plain), nil
}

func (r IntegrationConfigStore) secretAEAD() (cipher.AEAD, error) {
	key := r.store.SecretMaterialKey()
	block, _ := aes.NewCipher(key[:])
	return cipher.NewGCM(block)
}
