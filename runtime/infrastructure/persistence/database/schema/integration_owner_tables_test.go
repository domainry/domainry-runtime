package schema_test

import (
	"path/filepath"
	"testing"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestFreshRuntimeSchemaDoesNotCreateIntegrationOwnedWebPushSubscriptions(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "runtime-schema.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'web_push_subscriptions'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("Runtime schema still creates Integration-owned web_push_subscriptions")
	}
}
