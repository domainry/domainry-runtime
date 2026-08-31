package capability

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestRuntimeAuthoringCapabilitySourcePathsAreCurrentAndExist(t *testing.T) {
	t.Parallel()

	repoRoot := capabilityTestRepoRoot(t)
	contract := RuntimeAuthoringCapabilities()
	for _, domain := range contract.Domains {
		for _, definition := range domain.Capabilities {
			for _, source := range definition.Sources {
				// Owner evidence lives in independently buildable modules and is
				// addressed by module import path rather than Runtime filesystem path.
				if source.Kind == "ui" || source.Kind == "owner_module" || source.Kind == "owner_sdk" || source.Kind == "owner_catalog" {
					continue
				}
				path := filepath.Join(repoRoot, filepath.FromSlash(source.Path))
				info, err := os.Stat(path)
				if err != nil {
					t.Errorf("capability %q source %q symbol %q does not exist: %v", definition.Key, source.Path, source.Symbol, err)
					continue
				}
				if info.IsDir() {
					t.Errorf("capability %q source %q is a directory", definition.Key, source.Path)
				}
			}
		}
	}
}

func capabilityTestRepoRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve capability source test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "../../.."))
}
