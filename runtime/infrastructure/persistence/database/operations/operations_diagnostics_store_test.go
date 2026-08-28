package operations

import (
	"path/filepath"
	"testing"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestOperationsDiagnosticsSnapshotUsesBoundedRegisteredSections(t *testing.T) {
	runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "diagnostics.db"), MigrationBackupLastSuccessAt: "2026-07-19T10:00:00Z", MigrationRestoreDrillSuccessAt: "2026-07-18T10:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	defer runtimeStore.Close()
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	request := operationsmodel.OperationsDiagnosticsRequest{WorkspaceID: "workspace-a", InstanceID: "runtime-1", Sections: []string{"schema_migration", "db_pool", "worker_lease", "queue_lag", "dlq", "backup_age"}, Page: 1, PageSize: 10, Now: time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)}
	snapshot, err := NewOperationsStore(runtimeStore).OperationsDiagnosticsSnapshot(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Redacted || snapshot.CostUnits != 60 || len(snapshot.Sections) != 6 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	for _, section := range request.Sections {
		if snapshot.Sections[section].Status == "unavailable" {
			t.Fatalf("section %s unavailable: %#v", section, snapshot.Sections[section])
		}
	}
	backup := snapshot.Sections["backup_age"].Summary
	if backup["backup_age_seconds"] != float64(7200) || backup["restore_drill_age_seconds"] != float64(93600) {
		t.Fatalf("backup=%#v", backup)
	}
	if details := snapshot.Sections["db_pool"].Summary; details["driver"] != "sqlite" {
		t.Fatalf("pool=%#v", details)
	}
}
