package integration

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestIntegrationSecretMaterialIsEncryptedScopedAndContextAware(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "secrets.db"), IntegrationSecretKey: "test-material-key"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	repository := NewIntegrationConfigStore(store)
	if err := repository.PutSecretMaterial(t.Context(), "workspace-a", "api-key", "plain-secret-value"); err != nil {
		t.Fatal(err)
	}
	resolved, err := repository.ResolveSecretMaterial(t.Context(), "workspace-a", "api-key")
	if err != nil || resolved != "plain-secret-value" {
		t.Fatalf("resolved=%q error=%v", resolved, err)
	}
	var ciphertext string
	if err := store.DB().QueryRowContext(t.Context(), "SELECT ciphertext FROM _integration_secret_materials WHERE workspace_id = ? AND secret_key = ?", "workspace-a", "api-key").Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ciphertext, "plain-secret-value") || !strings.HasPrefix(ciphertext, "v2:") {
		t.Fatalf("material was not encrypted: %q", ciphertext)
	}
	if _, err := repository.ResolveSecretMaterial(t.Context(), "workspace-b", "api-key"); err == nil {
		t.Fatal("cross-workspace secret resolution succeeded")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.ResolveSecretMaterial(ctx, "workspace-a", "api-key"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled resolution error=%v", err)
	}
}

func TestIntegrationSecretMaterialOnlineKeyRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "online-rotation.db")
	first, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: path, IntegrationSecretKey: "first-key", IntegrationActiveKeyID: "key-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureIntegrationTestSchema(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if err := NewIntegrationConfigStore(first).PutSecretMaterial(t.Context(), "workspace-a", "credential", "old-secret"); err != nil {
		t.Fatal(err)
	}
	_ = first.Close()

	second, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: path, IntegrationSecretKey: "second-key", IntegrationActiveKeyID: "key-2", IntegrationDecryptOnlyKeys: map[string]string{"key-1": "first-key"}})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	repository := NewIntegrationConfigStore(second)
	value, err := repository.ResolveSecretMaterial(t.Context(), "workspace-a", "credential")
	if err != nil || value != "old-secret" {
		t.Fatalf("decrypt-only overlap failed: value=%q err=%v", value, err)
	}
	if err := repository.PutSecretMaterial(t.Context(), "workspace-a", "new-credential", "new-secret"); err != nil {
		t.Fatal(err)
	}
	var ciphertext string
	if err := second.DB().QueryRowContext(t.Context(), "SELECT ciphertext FROM _integration_secret_materials WHERE workspace_id = ? AND secret_key = ?", "workspace-a", "new-credential").Scan(&ciphertext); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ciphertext, "v2:") {
		t.Fatalf("new ciphertext format invalid: %s", ciphertext)
	}
}

func TestIntegrationSecretMaterialCannotBeOpenedWithDifferentInstanceKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rotated-key.db")
	first, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: path, IntegrationSecretKey: "first-key"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureIntegrationTestSchema(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if err := NewIntegrationConfigStore(first).PutSecretMaterial(t.Context(), "workspace-primary", "credential", "secret"); err != nil {
		t.Fatal(err)
	}
	_ = first.Close()
	second, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: path, IntegrationSecretKey: "second-key"})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := NewIntegrationConfigStore(second).ResolveSecretMaterial(t.Context(), "workspace-primary", "credential"); err == nil {
		t.Fatal("ciphertext opened with a different instance key")
	}
}
