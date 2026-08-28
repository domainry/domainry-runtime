package schema_test

import (
	"path/filepath"
	"strings"
	"testing"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestIdempotencyReceiptMigrationBackfillsBeforeCreatingUniqueIndex(t *testing.T) {
	store := openIdempotencyMigrationStore(t, "backfill.db")
	defer store.Close()
	createLegacyActionExecutionTable(t, store)
	if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO business_action_executions (id, workspace_id, object_key, record_id, action_key, idempotency_key, status, result_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, "legacy-one", "", "", "", "", "", "", `{}`, "2026-07-19T00:00:00Z", "2026-07-19T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureEvidenceSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	var workspaceID, objectKey, recordID, actionKey, idempotencyKey, fingerprint, status string
	if err := store.DB().QueryRowContext(t.Context(), `SELECT workspace_id, object_key, record_id, action_key, idempotency_key, request_fingerprint, status FROM business_action_executions WHERE id = ?`, "legacy-one").Scan(&workspaceID, &objectKey, &recordID, &actionKey, &idempotencyKey, &fingerprint, &status); err != nil {
		t.Fatal(err)
	}
	if workspaceID != "default" || !strings.HasPrefix(objectKey, "legacy:object_key:") || !strings.HasPrefix(actionKey, "legacy:action_key:") || !strings.HasPrefix(idempotencyKey, "legacy:idempotency_key:") || !strings.HasPrefix(fingerprint, "legacy:request_fingerprint:") || status != "succeeded" {
		t.Fatalf("backfill workspace=%q object=%q record=%q action=%q key=%q fingerprint=%q status=%q", workspaceID, objectKey, recordID, actionKey, idempotencyKey, fingerprint, status)
	}
	if recordID != "" {
		t.Fatalf("optional object-level record scope was rewritten: %q", recordID)
	}
	var reportTableCount int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = '_idempotency_migration_reports'`).Scan(&reportTableCount); err != nil || reportTableCount != 0 {
		t.Fatalf("clean idempotency migration initialized historical report table: count=%d err=%v", reportTableCount, err)
	}
	var indexCount int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'uniq_business_action_execution_scope'`).Scan(&indexCount); err != nil || indexCount != 1 {
		t.Fatalf("unique index count=%d err=%v", indexCount, err)
	}
}

func TestIdempotencyReceiptMigrationReportsDuplicatesAndNeverDeletesRows(t *testing.T) {
	store := openIdempotencyMigrationStore(t, "duplicates.db")
	defer store.Close()
	createLegacyActionExecutionTable(t, store)
	insert := `INSERT INTO business_action_executions (id, workspace_id, object_key, record_id, action_key, idempotency_key, status, result_json, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
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
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM business_action_executions`).Scan(&receiptCount); err != nil {
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
	query := `CREATE TABLE business_action_executions (
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
