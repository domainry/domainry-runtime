package operations

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

// TestOperationsContractAcrossRealDialects proves that the durable command
// ledger has the same replay, conflict, workspace-isolation and multi-instance
// semantics on every supported database. Local runs always cover SQLite. A
// production gate sets RUNTIME_REQUIRE_REAL_DIALECTS=1 so absent PostgreSQL or
// MySQL infrastructure is a failure instead of a misleading skipped success.
func TestOperationsContractAcrossRealDialects(t *testing.T) {
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
				cfg.DBPath = filepath.Join(t.TempDir(), "operations-dialect.db")
			} else {
				cfg.DatabaseDSN = strings.TrimSpace(os.Getenv(testCase.dsnEnv))
				if cfg.DatabaseDSN == "" {
					if strings.TrimSpace(os.Getenv("RUNTIME_REQUIRE_REAL_DIALECTS")) == "1" {
						t.Fatalf("%s is required by the production dialect gate", testCase.dsnEnv)
					}
					t.Skip(testCase.dsnEnv + " is not configured")
				}
			}

			first := openOperationsDialectStore(t, cfg)
			defer first.Close()
			if err := first.EnsureRuntimeSchema(t.Context()); err != nil {
				t.Fatal(err)
			}
			second := openOperationsDialectStore(t, cfg)
			defer second.Close()

			suffix := fmt.Sprintf("%s-%d", testCase.name, time.Now().UTC().UnixNano())
			workspaceA, workspaceB := "operations-a-"+suffix, "operations-b-"+suffix
			defer deleteOperationsDialectRows(t, first, workspaceA, workspaceB)
			assertConcurrentOperationsReplayAndIsolation(t, NewOperationsStore(first), NewOperationsStore(second), workspaceA, workspaceB)
		})
	}
}

func openOperationsDialectStore(t *testing.T, cfg config.Config) *database.RuntimeStore {
	t.Helper()
	store, err := database.OpenContext(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func assertConcurrentOperationsReplayAndIsolation(t *testing.T, first, second OperationsStore, workspaceA, workspaceB string) {
	t.Helper()
	now := time.Now().UTC()
	receipt := operationsmodel.OperationsReceipt{Command: operationsmodel.OperationsCommand{
		ID: "operation-" + workspaceA, Kind: "backup.create", Permission: "runtime.backup.create",
		Scope:          operationsmodel.OperationsScope{WorkspaceID: workspaceA, ResourceType: "database"},
		IdempotencyKey: "shared-command", RequestFingerprint: "same-fingerprint", RequestedBy: "operator", Reason: "dialect contract", Status: operationsmodel.OperationsStatusCreated, CreatedAt: now, UpdatedAt: now,
	}, StatusURL: "/operations/operation-" + workspaceA}

	start := make(chan struct{})
	decisions := make(chan operationsmodel.OperationsSubmissionDecision, 2)
	errorsFound := make(chan error, 2)
	var wait sync.WaitGroup
	for _, repository := range []OperationsStore{first, second} {
		wait.Add(1)
		go func(store OperationsStore) {
			defer wait.Done()
			<-start
			persisted, decision, err := store.RegisterOperationsCommand(t.Context(), receipt)
			if err != nil {
				errorsFound <- err
				return
			}
			if persisted.Command.ID != receipt.Command.ID {
				errorsFound <- fmt.Errorf("persisted operation %q, want %q", persisted.Command.ID, receipt.Command.ID)
				return
			}
			decisions <- decision
		}(repository)
	}
	close(start)
	wait.Wait()
	close(errorsFound)
	close(decisions)
	for err := range errorsFound {
		t.Fatalf("concurrent registration: %v", err)
	}
	counts := map[operationsmodel.OperationsSubmissionDecision]int{}
	for decision := range decisions {
		counts[decision]++
	}
	if counts[operationsmodel.OperationsSubmissionAccepted] != 1 || counts[operationsmodel.OperationsSubmissionReplay] != 1 {
		t.Fatalf("decisions=%v, want one accepted and one replay", counts)
	}

	changed := receipt
	changed.Command.ID = "conflict-" + workspaceA
	changed.Command.RequestFingerprint = "different-fingerprint"
	if _, decision, err := second.RegisterOperationsCommand(t.Context(), changed); err != nil || decision != operationsmodel.OperationsSubmissionConflict {
		t.Fatalf("fingerprint conflict decision=%q err=%v", decision, err)
	}
	if _, found, err := first.GetOperationsReceipt(t.Context(), operationsmodel.OperationsScope{WorkspaceID: workspaceB}, receipt.Command.ID); err != nil || found {
		t.Fatalf("workspace B observed workspace A operation: found=%v err=%v", found, err)
	}
	if persisted, found, err := second.GetOperationsReceipt(t.Context(), receipt.Command.Scope, receipt.Command.ID); err != nil || !found || persisted.Command.ID != receipt.Command.ID {
		t.Fatalf("second instance cannot observe durable operation: persisted=%+v found=%v err=%v", persisted, found, err)
	}
}

func deleteOperationsDialectRows(t *testing.T, store *database.RuntimeStore, workspaceIDs ...string) {
	t.Helper()
	for _, workspaceID := range workspaceIDs {
		if _, err := store.DB().ExecContext(t.Context(), "DELETE FROM "+store.TableIdentifier("runtime_operations")+" WHERE "+store.Identifier("workspace_id")+" = "+store.Placeholder(1), workspaceID); err != nil {
			t.Errorf("delete dialect evidence for %s: %v", workspaceID, err)
		}
	}
}
