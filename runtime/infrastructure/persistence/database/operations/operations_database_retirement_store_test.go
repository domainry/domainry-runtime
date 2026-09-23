package operations

import (
	"path/filepath"
	"testing"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestDatabaseRetirementStorePersistsTransitionsAndAccessObservations(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "retirement.db"), IntegrationSecretKey: "retirement-test-key"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewOperationsStore(store)
	now := time.Date(2026, 7, 19, 14, 0, 0, 0, time.UTC)
	retirement := operationsmodel.DatabaseRetirement{
		ID: "retire-old-table", Object: operationsmodel.DatabaseObjectIdentity{Engine: "sqlite", Database: "runtime", Kind: "table", Name: "old_table"},
		State: operationsmodel.DatabaseRetirementDiscovered, Evidence: operationsmodel.DatabaseRetirementEvidence{Owner: "record", Observation: operationsmodel.DatabaseAccessObservation{WindowStarted: now, WindowEnds: now.Add(24 * time.Hour)}}, UpdatedAt: now,
	}
	created, err := repository.RegisterDatabaseRetirement(t.Context(), retirement)
	if err != nil || !created {
		t.Fatalf("register retirement: created=%v err=%v", created, err)
	}
	var operationRows int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _operations WHERE id = ? AND system_purpose = ? AND owner = ? AND kind = ?`, retirement.ID, databaseRetirementSystemPurpose, databaseRetirementOwner, databaseRetirementKind).Scan(&operationRows); err != nil || operationRows != 1 {
		t.Fatalf("shared operation rows=%d err=%v", operationRows, err)
	}
	var legacyTables int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = '_operation_database_retirements'`).Scan(&legacyTables); err != nil || legacyTables != 0 {
		t.Fatalf("dedicated database retirement table remains=%d err=%v", legacyTables, err)
	}
	if err := repository.RecordDatabaseRetirementAccess(t.Context(), retirement.ID, "read", "runtime", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	observed, found, err := repository.GetDatabaseRetirement(t.Context(), retirement.ID)
	if err != nil || !found || observed.Evidence.Observation.ReadCount != 1 || observed.Evidence.Observation.SourceCounts["runtime"] != 1 || observed.Evidence.Observation.LastReadAt == nil {
		t.Fatalf("observed retirement=%+v found=%v err=%v", observed, found, err)
	}
	observed.State = operationsmodel.DatabaseRetirementBlocked
	observed.BlockedReason = "compatibility read observed"
	observed.UpdatedAt = now.Add(2 * time.Minute)
	changed, err := repository.TransitionDatabaseRetirement(t.Context(), observed, operationsmodel.DatabaseRetirementDiscovered)
	if err != nil || !changed {
		t.Fatalf("transition retirement: changed=%v err=%v", changed, err)
	}
	items, err := repository.ListDatabaseRetirements(t.Context(), operationsmodel.DatabaseRetirementBlocked, 10)
	if err != nil || len(items) != 1 || items[0].BlockedReason == "" {
		t.Fatalf("list blocked retirements=%+v err=%v", items, err)
	}
}

func TestDatabaseRetirementStoreRejectsHighCardinalityAccessSource(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "retirement-source.db"), IntegrationSecretKey: "retirement-test-key"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repository := NewOperationsStore(store)
	if err := repository.RecordDatabaseRetirementAccess(t.Context(), "retire-1", "read", "request-019f-high-cardinality", time.Now().UTC()); err == nil {
		t.Fatal("unbounded access source accepted")
	}
}
