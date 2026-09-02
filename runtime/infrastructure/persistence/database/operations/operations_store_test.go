package operations

import (
	"path/filepath"
	"testing"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestOperationsStorePersistsReplayConflictAndWorkspaceIsolation(t *testing.T) {
	runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "operations.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer runtimeStore.Close()
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := NewOperationsStore(runtimeStore)
	now := time.Date(2026, 7, 19, 10, 0, 0, 0, time.UTC)
	receipt := operationsmodel.OperationsReceipt{Command: operationsmodel.OperationsCommand{
		ID: "operation-1", Kind: "backup.create", ActionKey: "runtime.operations.create_backup",
		Scope:          operationsmodel.OperationsScope{WorkspaceID: "workspace-a", ResourceType: "database"},
		IdempotencyKey: "backup-1", RequestFingerprint: "fingerprint-a", RequestedBy: "operator-a", Reason: "release", Status: operationsmodel.OperationsStatusCreated, CreatedAt: now, UpdatedAt: now,
	}, StatusURL: "/operations/operation-1"}

	persisted, decision, err := store.RegisterOperationsCommand(t.Context(), receipt)
	if err != nil || decision != operationsmodel.OperationsSubmissionAccepted || persisted.Command.ID != receipt.Command.ID {
		t.Fatalf("persisted=%#v decision=%s err=%v", persisted, decision, err)
	}
	replayed, decision, err := store.RegisterOperationsCommand(t.Context(), receipt)
	if err != nil || decision != operationsmodel.OperationsSubmissionReplay || replayed.Command.ID != receipt.Command.ID {
		t.Fatalf("replayed=%#v decision=%s err=%v", replayed, decision, err)
	}
	changed := receipt
	changed.Command.ID, changed.Command.RequestFingerprint = "operation-2", "fingerprint-b"
	if _, decision, err := store.RegisterOperationsCommand(t.Context(), changed); err != nil || decision != operationsmodel.OperationsSubmissionConflict {
		t.Fatalf("conflict decision=%s err=%v", decision, err)
	}
	if _, found, err := store.GetOperationsReceipt(t.Context(), operationsmodel.OperationsScope{WorkspaceID: "workspace-b"}, receipt.Command.ID); err != nil || found {
		t.Fatalf("cross-workspace found=%v err=%v", found, err)
	}
	listed, err := store.ListOperationsReceipts(t.Context(), operationsmodel.OperationsScope{WorkspaceID: "workspace-a"}, operationsmodel.OperationsStatusCreated, 10)
	if err != nil || len(listed) != 1 || listed[0].Command.ID != receipt.Command.ID {
		t.Fatalf("listed=%#v err=%v", listed, err)
	}
}

func TestOperationsStoreFencesLifecycleTransitionByExpectedStatus(t *testing.T) {
	runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "operations-transition.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer runtimeStore.Close()
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := NewOperationsStore(runtimeStore)
	now := time.Now().UTC()
	receipt := operationsmodel.OperationsReceipt{Command: operationsmodel.OperationsCommand{ID: "operation-transition", Kind: "restore.run", ActionKey: "runtime.operations.restore_backup", Scope: operationsmodel.OperationsScope{WorkspaceID: "workspace-a", ResourceType: "database"}, IdempotencyKey: "restore-1", RequestFingerprint: "same", RequestedBy: "operator", Reason: "drill", Status: operationsmodel.OperationsStatusCreated, CreatedAt: now, UpdatedAt: now}, StatusURL: "/operations/operation-transition"}
	if _, _, err := store.RegisterOperationsCommand(t.Context(), receipt); err != nil {
		t.Fatal(err)
	}
	receipt.Command.Status, receipt.Command.StartedAt, receipt.Command.UpdatedAt = operationsmodel.OperationsStatusStarted, &now, now
	changed, err := store.UpdateOperationsReceipt(t.Context(), receipt, operationsmodel.OperationsStatusCreated)
	if err != nil || !changed {
		t.Fatalf("first transition changed=%v err=%v", changed, err)
	}
	changed, err = store.UpdateOperationsReceipt(t.Context(), receipt, operationsmodel.OperationsStatusCreated)
	if err != nil || changed {
		t.Fatalf("stale transition changed=%v err=%v", changed, err)
	}
}

func TestOperationsStoreSearchFiltersAndSummarizesBeforeLimit(t *testing.T) {
	runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "operations-search.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer runtimeStore.Close()
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := NewOperationsStore(runtimeStore)
	base := time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC)
	register := func(id, workspaceID, requestedBy, reason string, status operationsmodel.OperationsStatus, failureClass operationsmodel.OperationsFailureClass, offset time.Duration) {
		t.Helper()
		current := base.Add(offset)
		receipt := operationsmodel.OperationsReceipt{Command: operationsmodel.OperationsCommand{
			ID: id, Kind: "workflow.process.retry", ActionKey: "runtime.workflows.retry_ops_workflow_process",
			Scope:          operationsmodel.OperationsScope{WorkspaceID: workspaceID, ResourceType: "workflow_process", ResourceID: "process-1"},
			IdempotencyKey: id, RequestFingerprint: id, RequestedBy: requestedBy, Reason: reason,
			Status: operationsmodel.OperationsStatusCreated, CreatedAt: current, UpdatedAt: current,
		}, StatusURL: "/operations/" + id}
		if _, _, registerErr := store.RegisterOperationsCommand(t.Context(), receipt); registerErr != nil {
			t.Fatal(registerErr)
		}
		if status != operationsmodel.OperationsStatusCreated {
			receipt.Command.Status = status
			receipt.Command.UpdatedAt = current.Add(time.Minute)
			receipt.FailureClass = failureClass
			if status == operationsmodel.OperationsStatusFailed {
				finishedAt := receipt.Command.UpdatedAt
				receipt.Command.FinishedAt = &finishedAt
			}
			if changed, updateErr := store.UpdateOperationsReceipt(t.Context(), receipt, operationsmodel.OperationsStatusCreated); updateErr != nil || !changed {
				t.Fatalf("changed=%v err=%v", changed, updateErr)
			}
		}
	}
	register("operation-failed-1", "workspace-a", "operator-a", "INC-42 recovery timeout", operationsmodel.OperationsStatusFailed, operationsmodel.OperationsFailureManualIntervention, time.Hour)
	register("operation-failed-2", "workspace-a", "operator-a", "INC-42 recovery blocked", operationsmodel.OperationsStatusFailed, operationsmodel.OperationsFailureRetryable, 2*time.Hour)
	register("operation-created", "workspace-a", "operator-a", "INC-42 queued", operationsmodel.OperationsStatusCreated, "", 3*time.Hour)
	register("operation-other-workspace", "workspace-b", "operator-a", "INC-42 hidden", operationsmodel.OperationsStatusFailed, operationsmodel.OperationsFailureManualIntervention, 4*time.Hour)

	page, err := store.SearchOperationsReceipts(t.Context(), operationsmodel.OperationsScope{WorkspaceID: "workspace-a"}, operationsmodel.OperationsReceiptFilter{
		Status: operationsmodel.OperationsStatusFailed, Kind: "workflow.process.retry", ResourceType: "workflow_process",
		ResourceID: "process-1", RequestedBy: "operator-a", Search: "inc-42", CreatedFrom: base.Format(time.RFC3339Nano),
		CreatedTo: base.Add(3 * time.Hour).Format(time.RFC3339Nano), Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Count != 2 || len(page.Items) != 1 || page.Summary.Failed != 2 || page.Summary.ManualIntervention != 1 {
		t.Fatalf("page=%#v", page)
	}
	if page.Items[0].Command.ID != "operation-failed-2" {
		t.Fatalf("newest item=%#v", page.Items[0])
	}
}
