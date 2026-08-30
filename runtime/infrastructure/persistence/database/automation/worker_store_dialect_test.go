package automation

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestAutomationInstructionLeaseContractAcrossDialects(t *testing.T) {
	cases := []struct{ name, driver, dsnEnv string }{
		{name: "sqlite", driver: "sqlite"},
		{name: "mysql", driver: "mysql", dsnEnv: "RUNTIME_MYSQL_TEST_DSN"},
		{name: "postgres", driver: "pgx", dsnEnv: "RUNTIME_POSTGRES_TEST_DSN"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Config{DatabaseDriver: test.driver, DatabaseDSN: os.Getenv(test.dsnEnv)}
			if test.driver == "sqlite" {
				cfg.DBPath = filepath.Join(t.TempDir(), "automation-lease.db")
			} else if cfg.DatabaseDSN == "" {
				t.Skipf("%s is not configured", test.dsnEnv)
			}
			store, err := database.OpenContext(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
				t.Fatal(err)
			}
			workspace := "automation-dialect-" + test.name + "-" + time.Now().UTC().Format("20060102150405.000000000")
			defer store.DB().ExecContext(t.Context(), "DELETE FROM "+store.Identifier("_automation_instruction_executions")+" WHERE "+store.Identifier("workspace_id")+" = "+store.Placeholder(1), workspace)
			assertAutomationLeaseRestartAndReclaim(t, store, workspace)
		})
	}
}

func assertAutomationLeaseRestartAndReclaim(t *testing.T, store *database.RuntimeStore, workspace string) {
	t.Helper()
	request := automationmodel.AutomationInstructionExecution{WorkspaceID: workspace, IdempotencyKey: "restart-reclaim", RuleKey: "rule", ObjectKey: "customer", RecordID: "customer_1", RecordVersion: "v1", Operation: "update", InstructionKey: "notify"}
	firstStore := NewAutomationWorkerStore(store)
	first, claimed, err := firstStore.ClaimInstruction(t.Context(), workspace, request, "worker-a", "2026-01-01T00:00:00Z", "2026-01-01T00:01:00Z")
	if err != nil || !claimed {
		t.Fatalf("initial claim: claimed=%v err=%v", claimed, err)
	}
	first, err = firstStore.HeartbeatInstruction(t.Context(), workspace, request.IdempotencyKey, first.LeaseOwner, first.FencingToken, "2026-01-01T00:03:00Z", "2026-01-01T00:01:00Z")
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	restartedStore := NewAutomationWorkerStore(store)
	if _, reclaimed, err := restartedStore.ClaimInstruction(t.Context(), workspace, request, "worker-b", "2026-01-01T00:02:00Z", "2026-01-01T00:04:00Z"); err != nil || reclaimed {
		t.Fatalf("restart ignored live heartbeat lease: reclaimed=%v err=%v", reclaimed, err)
	}

	start := make(chan struct{})
	winners := make(chan automationmodel.AutomationInstructionExecution, 100)
	errorsFound := make(chan error, 100)
	var group sync.WaitGroup
	for range 100 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			execution, won, claimErr := restartedStore.ClaimInstruction(t.Context(), workspace, request, "worker-concurrent", "2026-01-01T00:04:00Z", "2026-01-01T00:05:00Z")
			if claimErr != nil {
				errorsFound <- claimErr
			} else if won {
				winners <- execution
			}
		}()
	}
	close(start)
	group.Wait()
	close(winners)
	close(errorsFound)
	for claimErr := range errorsFound {
		t.Fatalf("concurrent reclaim: %v", claimErr)
	}
	var current automationmodel.AutomationInstructionExecution
	count := 0
	for winner := range winners {
		current, count = winner, count+1
	}
	if count != 1 || current.FencingToken != first.FencingToken+1 {
		t.Fatalf("reclaim winners=%d fencing=%d want=%d", count, current.FencingToken, first.FencingToken+1)
	}
	if _, err := restartedStore.CompleteInstruction(t.Context(), workspace, request.IdempotencyKey, first.LeaseOwner, first.FencingToken, "succeeded", nil, "", "2026-01-01T00:06:00Z"); err == nil {
		t.Fatal("stale owner completed reclaimed instruction")
	}
	if _, err := restartedStore.CompleteInstruction(t.Context(), workspace, request.IdempotencyKey, current.LeaseOwner, current.FencingToken, "succeeded", map[string]any{"dialect": store.Driver()}, "", "2026-01-01T00:06:00Z"); err != nil {
		t.Fatalf("current owner complete: %v", err)
	}
}
