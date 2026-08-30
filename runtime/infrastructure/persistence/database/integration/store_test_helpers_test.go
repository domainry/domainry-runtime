package integration

import (
	"context"
	"path/filepath"
	"testing"

	integrationmodule "github.com/domainry/domainry-integration/module"
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

// ensureIntegrationTestSchema assembles Runtime and source-owned Integration
// migrations through the host registrar and its sole _schema_migrations ledger.
func ensureIntegrationTestSchema(ctx context.Context, store *database.RuntimeStore) error {
	if err := store.EnsureRuntimeSchema(ctx); err != nil {
		return err
	}
	migrations, err := integrationmodule.SchemaMigrations(store.Driver(), store.Schema())
	if err != nil {
		return err
	}
	return store.ApplyORMOwnedMigrations(ctx, "integration", migrations)
}
