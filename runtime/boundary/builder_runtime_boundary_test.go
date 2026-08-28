package boundary_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuilderRuntimeBoundaryDocumentDeclaresOwnershipAndVersioning(t *testing.T) {
	root := runtimeRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "..", "docs", "architecture", "builder-runtime-boundary.md"))
	if err != nil {
		t.Fatal(err)
	}
	document := string(raw)
	for _, required := range []string{
		"## Deployment model",
		"## Data ownership",
		"## Connector ownership boundary",
		"## Runtime capability discovery contract",
		"contract_version",
		"contract_hash",
		"fail closed",
		"No process reads or writes the other process's database tables directly",
		"Builder never stores Provider credentials",
		"must not copy Connector backend",
	} {
		if !strings.Contains(document, required) {
			t.Errorf("builder/runtime boundary document is missing %q", required)
		}
	}
}
