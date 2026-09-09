package migration_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestEverySupportedRuntimeSchemaVersionUpgradesToCurrent(t *testing.T) {
	versions := SupportedRuntimeSchemaUpgradeVersions()
	if len(versions) == 0 {
		t.Fatal("supported runtime schema upgrade matrix is empty")
	}
	for _, version := range versions {
		t.Run(version, func(t *testing.T) {
			dir := t.TempDir()
			store, err := OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(dir, "runtime.db"), MigrationBackupDir: filepath.Join(dir, "backups")})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			fixturePath := filepath.Join("testdata", "runtime_schema_upgrades", version+".sql")
			fixture, err := os.ReadFile(fixturePath)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.DB().ExecContext(t.Context(), string(fixture)); err != nil {
				t.Fatalf("apply %s fixture: %v", version, err)
			}
			// Schema 001 predates workspace ownership on Audit. The upgrade matrix
			// models the required operator adjudication instead of asking Runtime to
			// guess which real tenant owns historical global rows.
			if version == "001_connector_runtime_lifecycle" {
				if _, err := store.DB().ExecContext(t.Context(), `ALTER TABLE _audit_events ADD COLUMN workspace_id TEXT NOT NULL DEFAULT 'workspace-primary'`); err != nil {
					t.Fatalf("adjudicate %s Audit workspace: %v", version, err)
				}
			}
			if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
				t.Fatalf("upgrade %s: %v", version, err)
			}
			if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
				t.Fatalf("repeat upgrade %s: %v", version, err)
			}
			if version == "023_dispatch_callback_receipts" {
				var zone, name string
				if err := store.DB().QueryRowContext(t.Context(), `SELECT time_zone, name FROM _application_schema_projection WHERE id = 'current'`).Scan(&zone, &name); err != nil || zone != "UTC" || name != "Legacy application" {
					t.Fatalf("application header upgrade zone=%q name=%q err=%v", zone, name, err)
				}
			}
			if version == "024_application_time_zone" {
				var receipts int
				if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _application_schema_upgrade_receipts`).Scan(&receipts); err != nil || receipts != 0 {
					t.Fatalf("definition upgrade receipts table after upgrade count=%d err=%v", receipts, err)
				}
			}
			var currentRows, dirty int
			if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*), COALESCE(MAX(dirty), 0) FROM _schema_migrations WHERE path = ?`, "runtime_schema_"+CurrentRuntimeSchemaVersion).Scan(&currentRows, &dirty); err != nil || currentRows != 1 || dirty != 0 {
				t.Fatalf("current ledger version=%s rows=%d dirty=%d err=%v", CurrentRuntimeSchemaVersion, currentRows, dirty, err)
			}
			var auditWorkspace, workflowLease string
			auditID := "audit-" + strings.SplitN(version, "_", 2)[0]
			workflowID := "workflow-" + strings.SplitN(version, "_", 2)[0]
			if err := store.DB().QueryRowContext(t.Context(), `SELECT workspace_id FROM _audit_events WHERE id = ?`, auditID).Scan(&auditWorkspace); err != nil || auditWorkspace != "workspace-primary" {
				t.Fatalf("audit fixture %s workspace=%q err=%v", auditID, auditWorkspace, err)
			}
			if err := store.DB().QueryRowContext(t.Context(), `SELECT lease_owner FROM _workflow_executions WHERE id = ?`, workflowID).Scan(&workflowLease); err != nil || workflowLease != "" {
				t.Fatalf("workflow fixture %s lease=%q err=%v", workflowID, workflowLease, err)
			}
		})
	}
}
