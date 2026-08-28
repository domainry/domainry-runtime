package boundary_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdempotencyEntrypointInventoryIsCurrent(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join(runtimeRoot(t), ".."))
	command := exec.Command("go", "run", "scripts/contracts/runtime_idempotency_entrypoint_inventory.go", "--check")
	command.Dir = repositoryRoot
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("idempotency gate violation: entry=inventory owner=cross-owner file=docs/architecture/runtime-idempotency-entrypoint-inventory.md missing_contract=current generated inventory: %v\n%s", err, output)
	}
	document := readArchitectureDocument(t, filepath.Join(repositoryRoot, "docs", "architecture", "runtime-idempotency-entrypoint-inventory.md"))
	for _, section := range []string{"## HTTP mutation routes", "## Application mutation commands", "## Process-owned workers", "## External side effects"} {
		if !strings.Contains(document, section) {
			t.Errorf("idempotency gate violation: entry=%s owner=cross-owner file=docs/architecture/runtime-idempotency-entrypoint-inventory.md missing_contract=inventory section", section)
		}
	}
	if strings.Contains(document, "review_required") {
		t.Error("idempotency gate violation: entry=review_required owner=cross-owner file=docs/architecture/runtime-idempotency-entrypoint-inventory.md missing_contract=reviewed owner decision")
	}
}
