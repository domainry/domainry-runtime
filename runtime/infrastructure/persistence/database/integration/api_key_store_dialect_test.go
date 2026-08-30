package integration

import (
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	"os"
	"path/filepath"
	"testing"
	"time"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestAPIKeyStoreRuntimeSchemaContract(t *testing.T) {
	tests := []struct {
		name   string
		driver string
		dsnEnv string
	}{
		{name: "sqlite", driver: "sqlite"},
		{name: "mysql", driver: "mysql", dsnEnv: "RUNTIME_MYSQL_TEST_DSN"},
		{name: "postgres", driver: "pgx", dsnEnv: "RUNTIME_POSTGRES_TEST_DSN"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Config{DatabaseDriver: test.driver}
			if test.driver == "sqlite" {
				cfg.DBPath = filepath.Join(t.TempDir(), "api-key-schema.db")
			} else {
				cfg.DatabaseDSN = os.Getenv(test.dsnEnv)
				if cfg.DatabaseDSN == "" {
					t.Skipf("%s is not configured", test.dsnEnv)
				}
			}
			store, err := database.OpenContext(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := ensureIntegrationTestSchema(t.Context(), store); err != nil {
				t.Fatal(err)
			}
			repository := NewIntegrationConfigStore(store)
			workspaceID := "api-key-schema-" + test.name + "-" + time.Now().UTC().Format("20060102150405.000000000")
			created, err := repository.UpsertAPIKey(t.Context(), workspaceID, integrationmodel.IntegrationAPIKey{
				Key: "runtime", WorkspaceID: workspaceID, Name: "Runtime", TokenPrefix: "vrd_", TokenHash: workspaceID + "-hash",
				ActorID: "admin", RoleKey: "admin", Scopes: []string{"records.read"}, Status: "active", CreatedBy: "admin",
			})
			if err != nil {
				t.Fatal(err)
			}
			if created.Key != "runtime" || created.CreatedAt == "" {
				t.Fatalf("created API key = %#v", created)
			}
			listed, err := repository.ListAPIKeys(t.Context(), workspaceID)
			if err != nil || len(listed) != 1 || listed[0].TokenHash != created.TokenHash {
				t.Fatalf("listed API keys = %#v, err=%v", listed, err)
			}
			found, ok, err := repository.FindAPIKeyByTokenHash(t.Context(), workspaceID, created.TokenHash)
			if err != nil || !ok || found.Key != created.Key {
				t.Fatalf("found API key = %#v, ok=%v, err=%v", found, ok, err)
			}
			lastUsedAt := time.Now().UTC().Format(time.RFC3339Nano)
			updated, err := repository.UpdateAPIKeyLastUsed(t.Context(), workspaceID, created.Key, lastUsedAt)
			if err != nil || updated.LastUsedAt != lastUsedAt {
				t.Fatalf("updated API key = %#v, err=%v", updated, err)
			}
		})
	}
}
