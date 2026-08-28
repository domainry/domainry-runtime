package boundary_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOperationsReliabilityReleaseEvidenceContractIsGoverned(t *testing.T) {
	root := runtimeRoot(t)
	repositoryRoot := filepath.Clean(filepath.Join(root, ".."))
	requiredFiles := []string{
		"scripts/operations/verify_runtime_operations_reliability.sh",
		"scripts/operations/contracts/runtime-operations-reliability-test-matrix.md",
		"scripts/operations/contracts/runtime-operations-reliability-baseline.md",
		"scripts/operations/contracts/runtime-reliability-flaky-tests.json",
	}
	for _, relative := range requiredFiles {
		if _, err := os.Stat(filepath.Join(repositoryRoot, relative)); err != nil {
			t.Errorf("Operations reliability evidence contract missing %s: %v", relative, err)
		}
	}

	scriptBytes, err := os.ReadFile(filepath.Join(repositoryRoot, requiredFiles[0]))
	if err != nil {
		t.Fatal(err)
	}
	script := string(scriptBytes)
	for _, marker := range []string{
		"RUNTIME_REQUIRE_REAL_DIALECTS=1",
		"go test -race",
		"runtime.operations.reliability.v1",
		"runtime_schema_version",
		"config_contract_sha256",
		"actual_rpo_seconds",
		"actual_rto_seconds",
	} {
		if !strings.Contains(script, marker) {
			t.Errorf("Operations reliability release gate missing %q", marker)
		}
	}

	registryBytes, err := os.ReadFile(filepath.Join(repositoryRoot, requiredFiles[3]))
	if err != nil {
		t.Fatal(err)
	}
	var registry struct {
		Version string `json:"version"`
		Entries []struct {
			Test      string `json:"test"`
			Owner     string `json:"owner"`
			Reason    string `json:"reason"`
			ExpiresOn string `json:"expires_on"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(registryBytes, &registry); err != nil {
		t.Fatal(err)
	}
	if registry.Version != "runtime.reliability.flaky.v1" {
		t.Fatalf("flaky registry version=%q", registry.Version)
	}
	for _, entry := range registry.Entries {
		if strings.TrimSpace(entry.Test) == "" || strings.TrimSpace(entry.Owner) == "" || strings.TrimSpace(entry.Reason) == "" || strings.TrimSpace(entry.ExpiresOn) == "" {
			t.Errorf("flaky entry must declare test, owner, reason and expiry: %+v", entry)
		}
	}
}

func TestHighRiskOwnerRoutesRegisterUnifiedTerminalReceipts(t *testing.T) {
	root := runtimeRoot(t)
	repositoryRoot := filepath.Clean(filepath.Join(root, ".."))
	files := map[string][]string{
		"runtime/transport/http/scheduler/scheduler_handler.go": {
			"scheduler.job.run", "scheduler.run.retry", "scheduler.run.cancel", "scheduler.dead_letter.resolve",
		},
		"runtime/transport/http/workflows/workflows_handler.go": {
			"workflow.execution.retry", "workflow.execution.resolve", "ExecuteOwnerOperation",
		},
		"runtime/transport/http/workflows/processes.go": {
			"workflow.process.retry", "workflow.process.cancel", "workflow.process.resolve",
		},
		"runtime/transport/http/integrations/event_handlers.go": {
			"integration.event.retry", "integration.event.replay",
		},
		"runtime/transport/http/integrations/outbox_handlers.go":            {"integration.outbox.retry"},
		"runtime/transport/http/operations/operations_lifecycle_cleanup.go": {"retention.cleanup", "ExecuteOwnerOperation"},
		"runtime/transport/http/operations/operations_handler.go":           {"idempotency.receipt.", "ExecuteOwnerOperation"},
	}
	for relative, markers := range files {
		contents, err := os.ReadFile(filepath.Join(repositoryRoot, relative))
		if err != nil {
			t.Fatal(err)
		}
		for _, marker := range markers {
			if !strings.Contains(string(contents), marker) {
				t.Errorf("high-risk owner route %s missing unified receipt marker %q", relative, marker)
			}
		}
	}

	wiringFiles := []string{
		"runtime/bootstrap/transport/http_integration_agent_handler_wiring.go",
	}
	for _, relative := range wiringFiles {
		contents, err := os.ReadFile(filepath.Join(repositoryRoot, relative))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(contents), "Operations: a.operations") {
			t.Errorf("production owner wiring %s does not inject the Operations ledger", relative)
		}
	}
}
