package boundary_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeOperationsInventoryTracksOwnersAndUnifiedJobGaps(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join(runtimeRoot(t), ".."))
	path := filepath.Join(repositoryRoot, "docs", "architecture", "runtime-operations-inventory.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	document := string(content)
	for _, required := range []string{
		"## Unified command contract",
		"## Existing owner operations",
		"GET /operations/catalog",
		"PUT /operations/controls/{controlKind}/{owner}",
		"POST /operations/leases/{owner}/{resourceID}/force-release",
		"POST /operations/diagnostics/snapshots",
		"POST /operations/break-glass",
		"_operation_controls",
		"_operation_break_glass_grants",
		"## Remaining migration work",
		"Scheduler",
		"Workflow",
		"Automation",
		"Integration",
		"Metadata / Migration",
		"Backup / Restore",
		"Retention",
		"terminal shared receipt",
		"prevents duplicate owner calls after a terminal replay",
	} {
		if !strings.Contains(document, required) {
			t.Errorf("operations inventory missing %q", required)
		}
	}
}
