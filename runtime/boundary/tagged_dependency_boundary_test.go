package boundary_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"
)

func TestRuntimeConsumesDomainryModulesByImmutableVersion(t *testing.T) {
	path := filepath.Join(runtimeRoot(t), "..", "go.mod")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := modfile.Parse(path, raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, replacement := range parsed.Replace {
		if strings.HasPrefix(replacement.Old.Path, "github.com/domainry/") {
			t.Errorf("Domainry module %s uses replace instead of a release tag", replacement.Old.Path)
		}
	}
	for _, requirement := range parsed.Require {
		if !strings.HasPrefix(requirement.Mod.Path, "github.com/domainry/") {
			continue
		}
		version := requirement.Mod.Version
		if version == "" || version == "v0.0.0" {
			t.Errorf("Domainry module %s is not pinned to an immutable version: %s", requirement.Mod.Path, version)
		}
	}
}
