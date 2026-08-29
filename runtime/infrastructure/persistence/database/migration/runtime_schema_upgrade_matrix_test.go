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
			if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
				t.Fatalf("upgrade %s: %v", version, err)
			}
			if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
				t.Fatalf("repeat upgrade %s: %v", version, err)
			}
			var currentRows, dirty int
			if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*), COALESCE(MAX(dirty), 0) FROM _schema_materializations WHERE version = ?`, CurrentRuntimeSchemaVersion).Scan(&currentRows, &dirty); err != nil || currentRows != 1 || dirty != 0 {
				t.Fatalf("current ledger version=%s rows=%d dirty=%d err=%v", CurrentRuntimeSchemaVersion, currentRows, dirty, err)
			}
			var auditWorkspace, workflowLease string
			auditID := "audit-" + strings.SplitN(version, "_", 2)[0]
			workflowID := "workflow-" + strings.SplitN(version, "_", 2)[0]
			if err := store.DB().QueryRowContext(t.Context(), `SELECT workspace_id FROM _audit_events WHERE id = ?`, auditID).Scan(&auditWorkspace); err != nil || auditWorkspace != "default" {
				t.Fatalf("audit fixture %s workspace=%q err=%v", auditID, auditWorkspace, err)
			}
			if err := store.DB().QueryRowContext(t.Context(), `SELECT lease_owner FROM _workflow_executions WHERE id = ?`, workflowID).Scan(&workflowLease); err != nil || workflowLease != "" {
				t.Fatalf("workflow fixture %s lease=%q err=%v", workflowID, workflowLease, err)
			}
		})
	}
}
