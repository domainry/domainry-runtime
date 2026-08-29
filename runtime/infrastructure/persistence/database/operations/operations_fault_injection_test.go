package operations

import (
	"path/filepath"
	"testing"
	"time"

	worker "github.com/domainry/domainry-foundation/worker"
	workertestkit "github.com/domainry/domainry-foundation/worker/testkit"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestOperationsTransactionFaultWindowsRollbackBeforeCommitAndRecoverAfterCommit(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		effect    workertestkit.FaultEffect
		committed bool
	}{{"before_begin", workertestkit.FaultEffect{Point: worker.FaultTransactionBeforeBegin, Err: errInjected}, false}, {"connection_loss", workertestkit.DBConnectionLoss(), false}, {"lock_timeout", workertestkit.DBLockTimeout(), false}, {"after_write", workertestkit.FaultEffect{Point: worker.FaultTransactionAfterWrite, Err: errInjected}, false}, {"crash_before_commit", workertestkit.CrashBeforeCommit(), false}, {"deadlock", workertestkit.DBDeadlock(), false}, {"crash_after_commit", workertestkit.CrashAfterCommit(), true}} {
		t.Run(testCase.name, func(t *testing.T) {
			runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "fault.db")})
			if err != nil {
				t.Fatal(err)
			}
			defer runtimeStore.Close()
			if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
				t.Fatal(err)
			}
			injector := workertestkit.NewScriptedFaultInjector(testCase.effect)
			store := NewOperationsStoreWithFaults(runtimeStore, injector)
			now := time.Now().UTC()
			receipt := operationsmodel.OperationsReceipt{Command: operationsmodel.OperationsCommand{ID: "operation-fault", Kind: "retention.cleanup", Permission: "workspace.admin", Scope: operationsmodel.OperationsScope{WorkspaceID: "workspace-a", ResourceType: "retention_policy", ResourceID: "policy"}, IdempotencyKey: "fault-key", RequestFingerprint: "fingerprint", RequestedBy: "operator", Reason: "fault test", Status: operationsmodel.OperationsStatusCreated, CreatedAt: now, UpdatedAt: now}, StatusURL: "/operations/operation-fault"}
			if _, _, err := store.RegisterOperationsCommand(t.Context(), receipt); err == nil {
				t.Fatal("fault did not surface")
			}
			persisted, found, err := store.GetOperationsReceipt(t.Context(), operationsmodel.OperationsScope{WorkspaceID: "workspace-a"}, receipt.Command.ID)
			if err != nil || found != testCase.committed {
				t.Fatalf("persisted=%#v found=%v committed=%v err=%v", persisted, found, testCase.committed, err)
			}
			if testCase.committed {
				replay, decision, err := store.RegisterOperationsCommand(t.Context(), receipt)
				if err != nil || decision != operationsmodel.OperationsSubmissionReplay || replay.Command.ID != receipt.Command.ID {
					t.Fatalf("replay=%#v decision=%s err=%v", replay, decision, err)
				}
			}
		})
	}
}

func TestOperationsSlowQueryFaultIsBoundedAndStillCommits(t *testing.T) {
	runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "slow.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer runtimeStore.Close()
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := NewOperationsStoreWithFaults(runtimeStore, workertestkit.NewScriptedFaultInjector(workertestkit.DBSlowQuery(5*time.Millisecond)))
	now := time.Now().UTC()
	receipt := operationsmodel.OperationsReceipt{Command: operationsmodel.OperationsCommand{ID: "operation-slow", Kind: "retention.cleanup", Permission: "workspace.admin", Scope: operationsmodel.OperationsScope{WorkspaceID: "workspace-a", ResourceType: "retention_policy"}, IdempotencyKey: "slow", RequestFingerprint: "fingerprint", RequestedBy: "operator", Reason: "slow query", Status: operationsmodel.OperationsStatusCreated, CreatedAt: now, UpdatedAt: now}, StatusURL: "/operations/operation-slow"}
	started := time.Now()
	_, decision, err := store.RegisterOperationsCommand(t.Context(), receipt)
	if err != nil || decision != operationsmodel.OperationsSubmissionAccepted || time.Since(started) < 5*time.Millisecond {
		t.Fatalf("decision=%s elapsed=%s err=%v", decision, time.Since(started), err)
	}
}

var errInjected = &injectedFaultError{}

type injectedFaultError struct{}

func (*injectedFaultError) Error() string { return "fault.injected" }
