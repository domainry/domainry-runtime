package boundary_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReliabilityFaultPointsCoverTransactionAndWorkerWindowsWithoutConfigToggle(t *testing.T) {
	root := runtimeRoot(t)
	required := map[string][]string{
		filepath.Join(root, "infrastructure", "persistence", "database", "operations", "operations_store.go"): {"FaultTransactionBeforeBegin", "FaultTransactionAfterBegin", "FaultTransactionBeforeWrite", "FaultTransactionAfterWrite", "FaultTransactionBeforeCommit", "FaultTransactionAfterCommit"},
		filepath.Join(root, "application", "integration", "integration_application_workers.go"):               {"FaultWorkerClaim", "FaultWorkerHeartbeat", "FaultProviderBeforeSend", "FaultProviderAfterSend", "FaultWorkerBeforeComplete", "FaultWorkerAfterComplete"},
	}
	for path, markers := range required {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		source := string(data)
		for _, marker := range markers {
			if !strings.Contains(source, marker) {
				t.Errorf("fault injection coverage violation: file=%s missing=%s", path, marker)
			}
		}
	}
	for _, path := range []string{filepath.Join(root, "platform", "config", "config.go"), filepath.Join(root, "bootstrap", "runtime", "config.go")} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		source := strings.ToLower(string(data))
		for _, marker := range []string{"fault_inject", "fault_point", "enable_fault", "fault_scenario"} {
			if strings.Contains(source, marker) {
				t.Errorf("production fault toggle forbidden: file=%s marker=%s", path, marker)
			}
		}
	}
}
