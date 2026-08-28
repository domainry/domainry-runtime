package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestWorkflowExecutionLeaseContractAcrossDialects(t *testing.T) {
	cases := []struct{ name, driver, dsnEnv string }{
		{name: "sqlite", driver: "sqlite"},
		{name: "mysql", driver: "mysql", dsnEnv: "RUNTIME_MYSQL_TEST_DSN"},
		{name: "postgres", driver: "pgx", dsnEnv: "RUNTIME_POSTGRES_TEST_DSN"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.Config{DatabaseDriver: test.driver, DatabaseDSN: os.Getenv(test.dsnEnv)}
			if test.driver == "sqlite" {
				cfg.DBPath = filepath.Join(t.TempDir(), "workflow-execution-lease.db")
			} else if cfg.DatabaseDSN == "" {
				t.Skipf("%s is not configured", test.dsnEnv)
			}
			store, err := database.OpenContext(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
				t.Fatal(err)
			}
			contractID := fmt.Sprintf("workflow-%s-%d", test.name, time.Now().UnixNano())
			assertWorkflowExecutionDialectLease(t, store, contractID)
		})
	}
}

func assertWorkflowExecutionDialectLease(t *testing.T, store *database.RuntimeStore, id string) {
	t.Helper()
	workspace := id
	repository := NewWorkflowWorkerStore(store)
	execution := workflowmodel.WorkflowExecution{ID: id, WorkflowKey: "scheduled", Name: "Scheduled", Trigger: "scheduled", Status: "failed", Action: map[string]any{}, Payload: map[string]any{}, Result: map[string]any{}, ActorID: "system", Attempt: 1, MaxAttempts: 3, NextRunAt: "2026-07-19T00:00:00Z", Message: "retry", CreatedAt: "2026-07-19T00:00:00Z", UpdatedAt: "2026-07-19T00:00:00Z"}
	if err := repository.InsertExecution(t.Context(), workspace, execution); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = store.DB().ExecContext(t.Context(), "DELETE FROM "+store.TableIdentifier("_workflow_executions")+" WHERE "+store.Identifier("workspace_id")+" = "+store.Placeholder(1), workspace)
	})
	start := make(chan struct{})
	winners := make(chan workflowmodel.WorkflowExecution, 100)
	errorsFound := make(chan error, 100)
	var group sync.WaitGroup
	for index := range 100 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			candidate := execution
			candidate.Status, candidate.LeaseOwner, candidate.LeaseExpiresAt, candidate.FencingToken = "running", fmt.Sprintf("runtime-%d", index), "2026-07-19T00:01:00Z", 1
			won, err := repository.UpdateExecutionWhere(t.Context(), workspace, candidate, map[string]any{"status": "failed", "updated_at": execution.UpdatedAt, "lease_owner": "", "fencing_token": int64(0)})
			if err != nil {
				errorsFound <- err
			} else if won {
				winners <- candidate
			}
		}()
	}
	close(start)
	group.Wait()
	close(winners)
	close(errorsFound)
	for err := range errorsFound {
		t.Fatal(err)
	}
	var first workflowmodel.WorkflowExecution
	count := 0
	for winner := range winners {
		first, count = winner, count+1
	}
	if count != 1 || first.FencingToken != 1 {
		t.Fatalf("workflow execution winners=%d first=%#v", count, first)
	}
	second := first
	second.LeaseOwner, second.LeaseExpiresAt, second.FencingToken = "runtime-restarted", "2026-07-19T00:03:00Z", 2
	if won, err := repository.UpdateExecutionWhere(t.Context(), workspace, second, map[string]any{"status": "running", "lease_owner": first.LeaseOwner, "fencing_token": first.FencingToken, "lease_expires_at": first.LeaseExpiresAt}); err != nil || !won {
		t.Fatalf("workflow execution reclaim won=%v err=%v", won, err)
	}
	stale := first
	stale.Status, stale.LeaseOwner, stale.LeaseExpiresAt = "succeeded", "", ""
	if won, err := repository.UpdateExecutionWhere(t.Context(), workspace, stale, map[string]any{"status": "running", "lease_owner": first.LeaseOwner, "fencing_token": first.FencingToken}); err != nil || won {
		t.Fatalf("stale workflow terminal write won=%v err=%v", won, err)
	}
}
