package schema_test

import (
	"path/filepath"
	"strings"
	"testing"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestIdempotencyReceiptMigrationBlocksRowsWithoutInitializedWorkspaceOwnership(t *testing.T) {
	store := openIdempotencyMigrationStore(t, "backfill.db")
	defer store.Close()
	createLegacyActionExecutionTable(t, store)
	if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO _action_executions (id, workspace_id, object_key, record_id, action_key, idempotency_key, status, result_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, "legacy-one", "", "", "", "", "", "", `{}`, "2026-07-19T00:00:00Z", "2026-07-19T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureEvidenceSchema(t.Context()); err == nil || !strings.Contains(err.Error(), "has no initialized workspace ownership") {
		t.Fatalf("migration error=%v", err)
	}
	var reportTableCount int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = '_idempotency_migration_reports'`).Scan(&reportTableCount); err != nil || reportTableCount != 0 {
		t.Fatalf("clean idempotency migration initialized historical report table: count=%d err=%v", reportTableCount, err)
	}
	var indexCount int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'uniq_business_action_execution_scope'`).Scan(&indexCount); err != nil || indexCount != 0 {
		t.Fatalf("unique index count=%d err=%v", indexCount, err)
	}
}

func TestIdempotencyReceiptMigrationReportsDuplicatesAndNeverDeletesRows(t *testing.T) {
	store := openIdempotencyMigrationStore(t, "duplicates.db")
	defer store.Close()
	createLegacyActionExecutionTable(t, store)
	insert := `INSERT INTO _action_executions (id, workspace_id, object_key, record_id, action_key, idempotency_key, status, result_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	for _, id := range []string{"duplicate-a", "duplicate-b"} {
		if _, err := store.DB().ExecContext(t.Context(), insert, id, "workspace-a", "customer", "", "customer.notify", "same-key", "", `{}`, "2026-07-19T00:00:00Z", "2026-07-19T00:00:00Z"); err != nil {
			t.Fatal(err)
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		err := store.EnsureEvidenceSchema(t.Context())
		if err == nil || !strings.Contains(err.Error(), "duplicate_scopes=1") || !strings.Contains(err.Error(), "receipt_ids=duplicate-a|duplicate-b") {
			t.Fatalf("attempt=%d migration error=%v", attempt, err)
		}
	}
	var receiptCount, reportTableCount, indexCount int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _action_executions`).Scan(&receiptCount); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = '_idempotency_migration_reports'`).Scan(&reportTableCount); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'uniq_business_action_execution_scope'`).Scan(&indexCount); err != nil {
		t.Fatal(err)
	}
	if receiptCount != 2 || reportTableCount != 0 || indexCount != 0 {
		t.Fatalf("receipts=%d report_table=%d index=%d", receiptCount, reportTableCount, indexCount)
	}
}

func openIdempotencyMigrationStore(t *testing.T, name string) *database.RuntimeStore {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), name)})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func createLegacyActionExecutionTable(t *testing.T, store *database.RuntimeStore) {
	t.Helper()
	query := `CREATE TABLE _action_executions (
id TEXT PRIMARY KEY,
workspace_id TEXT NOT NULL DEFAULT '',
object_key TEXT NOT NULL DEFAULT '',
record_id TEXT NOT NULL DEFAULT '',
action_key TEXT NOT NULL DEFAULT '',
idempotency_key TEXT NOT NULL DEFAULT '',
status TEXT NOT NULL DEFAULT '',
result_json TEXT NOT NULL DEFAULT '{}',
actor_id TEXT,
role_key TEXT,
created_at TEXT NOT NULL,
updated_at TEXT NOT NULL
)`
	if _, err := store.DB().ExecContext(t.Context(), query); err != nil {
		t.Fatal(err)
	}
}
