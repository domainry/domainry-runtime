package database_test

import (
	"path/filepath"
	"strings"
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
func TestWorkspaceScopeMigrationReportsLegacyRowsWithoutBackfill(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "workspace-migration.db"), IntegrationSecretKey: "workspace-migration-key"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB().Exec(`CREATE TABLE legacy_workspace_rows (id TEXT PRIMARY KEY, workspace_id TEXT); INSERT INTO legacy_workspace_rows (id, workspace_id) VALUES ('missing', NULL), ('blank', '  '), ('legacy-default', 'default'), ('valid', 'workspace-a')`); err != nil {
		t.Fatal(err)
	}
	err = store.EnsureRuntimeSchema(t.Context())
	if err == nil || !strings.Contains(err.Error(), "table=legacy_workspace_rows classification=missing_workspace row_count=2") || !strings.Contains(err.Error(), "table=legacy_workspace_rows classification=legacy_default_workspace row_count=1") {
		t.Fatalf("unexpected workspace migration error: %v", err)
	}
	var reportTableCount int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = '_workspace_scope_migration_reports'`).Scan(&reportTableCount); err != nil || reportTableCount != 0 {
		t.Fatalf("workspace validation initialized retired report table: count=%d err=%v", reportTableCount, err)
	}
	var dirtyMigrationCount int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _schema_migrations WHERE path = ?`, "runtime_schema_"+database.CurrentRuntimeSchemaVersion).Scan(&dirtyMigrationCount); err != nil || dirtyMigrationCount != 0 {
		t.Fatalf("workspace preflight left a dirty migration ledger row: count=%d err=%v", dirtyMigrationCount, err)
	}
	var missing, blank, legacyDefault sqlNullString
	if err := store.DB().QueryRow(`SELECT workspace_id FROM legacy_workspace_rows WHERE id = 'missing'`).Scan(&missing); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow(`SELECT workspace_id FROM legacy_workspace_rows WHERE id = 'blank'`).Scan(&blank); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRow(`SELECT workspace_id FROM legacy_workspace_rows WHERE id = 'legacy-default'`).Scan(&legacyDefault); err != nil {
		t.Fatal(err)
	}
	if missing.Valid || blank.String != "  " || legacyDefault.String != "default" {
		t.Fatalf("migration report must not rewrite legacy workspace values: missing=%+v blank=%+v default=%+v", missing, blank, legacyDefault)
	}
}

type sqlNullString struct {
	String string
	Valid  bool
}

func (value *sqlNullString) Scan(source any) error {
	if source == nil {
		value.String, value.Valid = "", false
		return nil
	}
	value.String, value.Valid = source.(string), true
	return nil
}
