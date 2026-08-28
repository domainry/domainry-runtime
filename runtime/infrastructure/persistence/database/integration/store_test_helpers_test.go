package integration

import (
	"path/filepath"
	"testing"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func openStoreForGeneratedListTest(t *testing.T) *database.RuntimeStore {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "integration.db"), IntegrationSecretKey: "test-integration-secret-key"})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func openRuntimeStore(t *testing.T) *database.RuntimeStore {
	return openStoreForGeneratedListTest(t)
}
