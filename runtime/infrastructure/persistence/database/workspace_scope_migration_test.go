package database_test

import (
	"path/filepath"
	"testing"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestFreshRuntimeDoesNotInitializeHistoricalMigrationReportTables(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "fresh-runtime.db"), IntegrationSecretKey: "fresh-runtime-key"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for attempt := 0; attempt < 2; attempt++ {
		if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"_workspace_scope_migration_reports", "_idempotency_migration_reports"} {
		var count int
		if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("fresh Runtime initialized retired migration table %s", table)
		}
	}
}
