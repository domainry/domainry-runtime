package operations

import (
	"path/filepath"
	"testing"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestOperationsControlStorePersistsAndFencesRevision(t *testing.T) {
	runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "operations-control.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer runtimeStore.Close()
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := NewOperationsStore(runtimeStore)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	control := operationsmodel.OperationsControl{SystemPurpose: operationsmodel.OperationsSystemPurposeRuntimeControl, Kind: operationsmodel.OperationsControlWorkerPause, Owner: "workflow", State: operationsmodel.OperationsControlActive, Reason: "incident", UpdatedBy: "admin", Revision: 1, UpdatedAt: now}
	if changed, err := store.PutOperationsControl(t.Context(), control, 0); err != nil || !changed {
		t.Fatalf("insert changed=%v err=%v", changed, err)
	}
	if changed, err := store.PutOperationsControl(t.Context(), control, 0); err != nil || changed {
		t.Fatalf("duplicate changed=%v err=%v", changed, err)
	}
	control.State, control.Revision = operationsmodel.OperationsControlInactive, 2
	if changed, err := store.PutOperationsControl(t.Context(), control, 1); err != nil || !changed {
		t.Fatalf("update changed=%v err=%v", changed, err)
	}
	control.Revision = 3
	if changed, err := store.PutOperationsControl(t.Context(), control, 1); err != nil || changed {
		t.Fatalf("stale changed=%v err=%v", changed, err)
	}
	persisted, found, err := store.GetOperationsControl(t.Context(), control.SystemPurpose, control.Kind, control.Owner)
	if err != nil || !found || persisted.State != operationsmodel.OperationsControlInactive || persisted.Revision != 2 {
		t.Fatalf("persisted=%#v found=%v err=%v", persisted, found, err)
	}
	listed, err := store.ListOperationsControls(t.Context(), control.SystemPurpose, control.Kind, 10)
	if err != nil || len(listed) != 1 || listed[0].Owner != "workflow" {
		t.Fatalf("listed=%#v err=%v", listed, err)
	}
}
