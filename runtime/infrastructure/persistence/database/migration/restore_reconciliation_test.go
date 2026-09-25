package migration

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestRestoreReconciliationReleasesOnlyExpiredInFlightLeases(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "restored.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	for _, statement := range []string{
		`CREATE TABLE _automation_runs (id TEXT PRIMARY KEY, run_kind TEXT NOT NULL, status TEXT NOT NULL, lease_owner TEXT NOT NULL, lease_expires_at BIGINT NOT NULL, fencing_token BIGINT NOT NULL, updated_at BIGINT NOT NULL)`,
		`CREATE TABLE _operations (id TEXT PRIMARY KEY, owner TEXT NOT NULL, status TEXT NOT NULL, lease_owner TEXT NOT NULL, lease_expires_at BIGINT NOT NULL, fencing_token BIGINT NOT NULL, updated_at BIGINT NOT NULL)`,
		`CREATE TABLE _publication_outbox (id TEXT PRIMARY KEY, status TEXT NOT NULL, lease_owner TEXT NOT NULL, lease_expires_at BIGINT NOT NULL, fencing_token BIGINT NOT NULL, updated_at BIGINT NOT NULL)`,
		`INSERT INTO _automation_runs VALUES ('expired','instruction','processing','old-worker',1790121600000,1,1790121600000)`,
		`INSERT INTO _automation_runs VALUES ('terminal','instruction','succeeded','old-worker',1790121600000,7,1790121600000)`,
		`INSERT INTO _operations VALUES ('expired','workflow','started','old-worker',1790121600000,2,1790121600000)`,
		`INSERT INTO _operations VALUES ('terminal','workflow','succeeded','old-worker',1790121600000,8,1790121600000)`,
		`INSERT INTO _publication_outbox VALUES ('expired','sending','old-worker',1790121600000,3,1790121600000)`,
		`INSERT INTO _publication_outbox VALUES ('terminal','sent','old-worker',1790121600000,9,1790121600000)`,
	} {
		if _, err := database.ExecContext(t.Context(), statement); err != nil {
			t.Fatal(err)
		}
	}
	restoredAt := time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)
	results, err := ReconcileRestoredDatabase(t.Context(), database, "sqlite", "", restoredAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("reconciliation results=%+v", results)
	}
	for _, result := range results {
		if result.Skipped || result.RowsAffected != 1 {
			t.Fatalf("reconciliation result=%+v", result)
		}
	}
	for _, table := range []string{"_automation_runs", "_operations", "_publication_outbox"} {
		assertReconciledLease(t, database, table, "expired", "", 1)
		assertReconciledLease(t, database, table, "terminal", "old-worker", 0)
	}
}

func assertReconciledLease(t *testing.T, database *sql.DB, table, id, owner string, tokenDelta int64) {
	t.Helper()
	baseToken := map[string]int64{"_automation_runs": 1, "_operations": 2, "_publication_outbox": 3}[table]
	if id == "terminal" {
		baseToken = map[string]int64{"_automation_runs": 7, "_operations": 8, "_publication_outbox": 9}[table]
	}
	var gotOwner string
	var expiresAt, token, updatedAt int64
	if err := database.QueryRowContext(t.Context(), "SELECT lease_owner, lease_expires_at, fencing_token, updated_at FROM "+table+" WHERE id=?", id).Scan(&gotOwner, &expiresAt, &token, &updatedAt); err != nil {
		t.Fatal(err)
	}
	if gotOwner != owner || token != baseToken+tokenDelta {
		t.Fatalf("%s/%s lease owner=%q token=%d", table, id, gotOwner, token)
	}
	if id == "expired" && (expiresAt != 0 || updatedAt != 1790208000000) {
		t.Fatalf("%s/%s expiry=%d updated_at=%d", table, id, expiresAt, updatedAt)
	}
	if id == "terminal" && (expiresAt != 1790121600000 || updatedAt != 1790121600000) {
		t.Fatalf("terminal %s changed: expiry=%d updated_at=%d", table, expiresAt, updatedAt)
	}
}

func TestRestoreReconciliationSkipsUnselectedCapabilityTables(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "minimal.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.ExecContext(t.Context(), `CREATE TABLE _operations (id TEXT PRIMARY KEY, owner TEXT NOT NULL, status TEXT NOT NULL, lease_owner TEXT NOT NULL, lease_expires_at BIGINT NOT NULL, fencing_token BIGINT NOT NULL, updated_at BIGINT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	results, err := ReconcileRestoredDatabase(t.Context(), database, "sqlite", "", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || !results[0].Skipped || results[1].Skipped || !results[2].Skipped {
		t.Fatalf("minimal reconciliation results=%+v", results)
	}
}
