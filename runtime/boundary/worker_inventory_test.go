package boundary_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var namedWorkerLoop = regexp.MustCompile(`StartNamedLoop\([^,]+,\s*"([^"]+)"`)

func TestRuntimeWorkerInventoryCoversProductionWorkers(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join(runtimeRoot(t), ".."))
	documentPath := filepath.Join(repositoryRoot, "docs", "architecture", "runtime-worker-inventory.md")
	raw, err := os.ReadFile(documentPath)
	if err != nil {
		t.Fatal(err)
	}
	document := string(raw)
	for _, workerID := range []string{
		"scheduler", "workflow_execution", "workflow_deadline", "automation_instruction",
		"integration_event", "_publication_outbox", "integration_invocation_reconciliation",
		"integration_credential_expiry", "notification_publication", "notification_inbox",
		"notification_channel", "data_exchange", "idempotency_cleanup", "metadata_snapshot",
		"lifecycle_cleanup",
	} {
		if !strings.Contains(document, "`"+workerID+"`") {
			t.Errorf("Runtime worker %s is missing from runtime-worker-inventory.md", workerID)
		}
	}
	for _, heading := range []string{"Durable store / claim", "Lease TTL / heartbeat", "Retry / DLQ", "Cancel / recovery", "Current gap"} {
		if !strings.Contains(document, heading) {
			t.Errorf("Runtime worker inventory is missing required classification %q", heading)
		}
	}
}

func TestRuntimeNamedLoopsAreRegisteredInWorkerInventory(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join(runtimeRoot(t), ".."))
	raw, err := os.ReadFile(filepath.Join(repositoryRoot, "docs", "architecture", "runtime-worker-inventory.md"))
	if err != nil {
		t.Fatal(err)
	}
	document := string(raw)
	err = filepath.WalkDir(filepath.Join(repositoryRoot, "runtime"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		source, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, match := range namedWorkerLoop.FindAllStringSubmatch(string(source), -1) {
			if match[1] == "worker" && filepath.ToSlash(path) == filepath.ToSlash(filepath.Join(repositoryRoot, "runtime", "platform", "worker", "lifecycle.go")) {
				continue
			}
			if !strings.Contains(document, "`"+match[1]+"`") {
				t.Errorf("named worker loop %s from %s is missing from runtime-worker-inventory.md", match[1], path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeRawTickerBaselineIsExact(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join(runtimeRoot(t), ".."))
	expected := map[string]bool{
		"runtime/transport/http/notifications/notifications_inbox_stream_handler.go": false,
		"runtime/transport/http/records/records_stream_handler.go":                   false,
	}
	err := filepath.WalkDir(filepath.Join(repositoryRoot, "runtime"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if !strings.Contains(string(raw), "time.NewTicker(") {
			return nil
		}
		relative, relErr := filepath.Rel(repositoryRoot, path)
		if relErr != nil {
			return relErr
		}
		relative = filepath.ToSlash(relative)
		if _, ok := expected[relative]; !ok {
			t.Errorf("unregistered production ticker %s; use domainry-foundation/worker.StartNamedLoop or update the reviewed inventory", relative)
			return nil
		}
		expected[relative] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for path, found := range expected {
		if !found {
			t.Errorf("reviewed ticker baseline %s no longer exists; remove the stale inventory exception", path)
		}
	}
}
