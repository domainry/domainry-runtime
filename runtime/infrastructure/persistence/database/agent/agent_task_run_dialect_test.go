package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

// TestAgentTaskRunContractAcrossRealDialects proves that Agent Task durable
// creation, idempotent replay, workspace isolation, claim serialization and
// fencing use the same contract on every supported database.
func TestAgentTaskRunContractAcrossRealDialects(t *testing.T) {
	cases := []struct {
		name   string
		driver string
		dsnEnv string
	}{
		{name: "sqlite", driver: "sqlite"},
		{name: "postgres", driver: "postgres", dsnEnv: "RUNTIME_POSTGRES_TEST_DSN"},
		{name: "mysql", driver: "mysql", dsnEnv: "RUNTIME_MYSQL_TEST_DSN"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := config.Config{DatabaseDriver: testCase.driver}
			if testCase.driver == "sqlite" {
				cfg.DBPath = filepath.Join(t.TempDir(), "agent-task-dialect.db")
			} else {
				cfg.DatabaseDSN = strings.TrimSpace(os.Getenv(testCase.dsnEnv))
				if cfg.DatabaseDSN == "" {
					if strings.TrimSpace(os.Getenv("RUNTIME_REQUIRE_REAL_DIALECTS")) == "1" {
						t.Fatalf("%s is required by the production dialect gate", testCase.dsnEnv)
					}
					t.Skip(testCase.dsnEnv + " is not configured")
				}
			}

			first := openAgentDialectStore(t, cfg)
			defer first.Close()
			second := openAgentDialectStore(t, cfg)
			defer second.Close()
			assertAgentTaskDialectContract(t, NewAgentTaskRunStore(first), NewAgentTaskRunStore(second), testCase.name)
		})
	}
}

func openAgentDialectStore(t *testing.T, cfg config.Config) *database.RuntimeStore {
	t.Helper()
	store, err := database.OpenContext(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	return store
}

func assertAgentTaskDialectContract(t *testing.T, first, second *AgentTaskRunStore, dialect string) {
	t.Helper()
	for _, repository := range []*AgentTaskRunStore{first, second} {
		if err := repository.EnsureSchema(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	suffix := fmt.Sprintf("%s-%d", dialect, now.UnixNano())
	workspace := "agent-dialect-" + suffix
	run := agentTaskRunFixture(now, "run-"+suffix, "idem-"+suffix)
	run.WorkspaceID = workspace
	run.Identity.Initiator.WorkspaceID = workspace
	defer func() {
		if _, err := first.db.ExecContext(context.Background(), "DELETE FROM "+first.store.TableIdentifier("agent_task_runs")+" WHERE "+first.store.Identifier("workspace_id")+" = "+first.store.Placeholder(1), workspace); err != nil {
			t.Errorf("delete Agent Task dialect evidence: %v", err)
		}
	}()

	created, replayed, err := first.Create(t.Context(), run)
	if err != nil || replayed || created.ID != run.ID {
		t.Fatalf("create=%#v replayed=%v err=%v", created, replayed, err)
	}
	if replay, duplicate, err := second.Create(t.Context(), run); err != nil || !duplicate || replay.ID != run.ID {
		t.Fatalf("replay=%#v duplicate=%v err=%v", replay, duplicate, err)
	}
	if _, found, err := second.Get(t.Context(), workspace+"-other", run.ID); err != nil || found {
		t.Fatalf("cross-workspace read found=%v err=%v", found, err)
	}

	start := make(chan struct{})
	errorsFound := make(chan error, 20)
	var claims atomic.Int64
	var wait sync.WaitGroup
	for index := 0; index < 20; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			repository := first
			if index%2 == 1 {
				repository = second
			}
			claim, found, err := repository.ClaimNext(t.Context(), workspace, fmt.Sprintf("worker-%d", index), now, time.Minute)
			if err != nil {
				errorsFound <- err
				return
			}
			if found {
				if claim.Run.ID != run.ID || claim.Lease.FencingToken != 1 {
					errorsFound <- fmt.Errorf("claim=%#v", claim)
					return
				}
				claims.Add(1)
			}
		}(index)
	}
	close(start)
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Fatal(err)
	}
	if claims.Load() != 1 {
		t.Fatalf("claim owners=%d, want 1", claims.Load())
	}

	loaded, found, err := second.Get(t.Context(), workspace, run.ID)
	if err != nil || !found || loaded.Status != agentmodel.AgentTaskRunRunning || loaded.Attempt != 1 || loaded.Lease.FencingToken != 1 {
		t.Fatalf("loaded=%#v found=%v err=%v", loaded, found, err)
	}
}
