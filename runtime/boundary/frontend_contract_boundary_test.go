package boundary_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLegacyTemplateUIContractDoesNotSpread freezes the known legacy payload
// locations while the v1 manifest migrator is being introduced. Every count is
// an upper bound: cleanup may reduce it, but new declarations or new owning
// files must fail this test.
func TestLegacyTemplateUIContractDoesNotSpread(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join(runtimeRoot(t), "..", "..", "domainry-plane"))
	legacyRoots := []string{
		filepath.Join(repositoryRoot, "internal", "controlplane", "blueprint", "compiled"),
	}
	allowlist := map[string]map[string]int{
		`json:"theme`:             {"internal/controlplane/blueprint/compiled/manifest.go": 1},
		`json:"ui_blueprint`:      {"internal/controlplane/blueprint/compiled/manifest_ui.go": 1},
		`json:"composition`:       {"internal/controlplane/blueprint/compiled/manifest_ui.go": 1},
		`json:"rhythm`:            {"internal/controlplane/blueprint/compiled/manifest_ui.go": 1},
		`json:"presentation`:      {"internal/controlplane/blueprint/compiled/manifest_ui.go": 1},
		`json:"surface_style`:     {"internal/controlplane/blueprint/compiled/manifest_ui.go": 1},
		`json:"card_style`:        {"internal/controlplane/blueprint/compiled/manifest_ui.go": 1},
		`json:"radius`:            {"internal/controlplane/blueprint/compiled/manifest_ui.go": 1},
		`json:"content_bias`:      {"internal/controlplane/blueprint/compiled/manifest_ui.go": 1},
		`json:"action_prominence`: {"internal/controlplane/blueprint/compiled/manifest_ui.go": 1},
		`json:"motion`:            {"internal/controlplane/blueprint/compiled/manifest_ui.go": 1},
		`json:"primitive`:         {"internal/controlplane/blueprint/compiled/manifest_ui.go": 2},
		`json:"slot,`:             {"internal/controlplane/blueprint/compiled/manifest_ui.go": 1},
		`json:"slot_intent`:       {"internal/controlplane/blueprint/compiled/manifest_ui.go": 1},
		`json:"density`:           {"internal/controlplane/blueprint/compiled/manifest_ui.go": 4},
		`json:"empty_state`:       {"internal/controlplane/blueprint/compiled/manifest_ui.go": 1},
	}
	// The pure visual payload was removed after this migration guard was
	// introduced. Keep the markers but retire every legacy location so any
	// reintroduction, including in the old files, fails immediately.
	for marker := range allowlist {
		allowlist[marker] = map[string]int{}
	}

	actual := map[string]map[string]int{}
	for _, root := range legacyRoots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			rel, relErr := filepath.Rel(repositoryRoot, path)
			if relErr != nil {
				return relErr
			}
			rel = filepath.ToSlash(rel)
			for marker := range allowlist {
				count := strings.Count(string(content), marker)
				if count == 0 {
					continue
				}
				if actual[marker] == nil {
					actual[marker] = map[string]int{}
				}
				actual[marker][rel] = count
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	for marker, files := range actual {
		for path, count := range files {
			maximum, allowed := allowlist[marker][path]
			if !allowed {
				t.Errorf("legacy frontend marker %q spread to %s", marker, path)
				continue
			}
			if count > maximum {
				t.Errorf("legacy frontend marker %q grew in %s: maximum=%d actual=%d", marker, path, maximum, count)
			}
		}
	}
}

func TestSourceOwnedFrontendBoundaryDecisionIsPresent(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join(runtimeRoot(t), "..", "..", "domainry-plane"))
	path := filepath.Join(repositoryRoot, "docs", "adr", "0001-source-owned-frontend-contract-boundary.md")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, required := range []string{
		"Runtime manifest 只表达可执行的业务合约",
		"Builder 交付物不是前端设计的事实源",
		"schema_usage=page_structure",
		"旧 Surface/Component/Menu 是迁移源，不是目标模型",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("source-owned frontend ADR is incomplete: missing %q", required)
		}
	}
}

func TestCanonicalRuntimeManifestHasNoLegacyFrontendCollections(t *testing.T) {
	runtimeRepositoryRoot := filepath.Clean(filepath.Join(runtimeRoot(t), ".."))
	planeRepositoryRoot := filepath.Clean(filepath.Join(runtimeRepositoryRoot, "..", "domainry-plane"))
	checks := map[string][]string{
		filepath.Join(planeRepositoryRoot, "internal", "controlplane", "blueprint", "compiled", "manifest.go"): {
			"Surfaces", "Components", `json:"surfaces`, `json:"components`,
		},
		filepath.Join(runtimeRepositoryRoot, "runtime", "domain", "manifest", "model", "manifest_manifest.go"): {
			"Surfaces", "Components", `json:"surfaces`, `json:"components`, `json:"surface_key`, `json:"layout`,
		},
	}
	for path, forbidden := range checks {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, marker := range forbidden {
			if strings.Contains(string(content), marker) {
				t.Errorf("canonical Runtime contract %s still contains legacy frontend marker %q", path, marker)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(runtimeRepositoryRoot, "runtime", "domain", "domain")); !os.IsNotExist(err) {
		t.Fatalf("redundant domain/domain package must be removed, stat error=%v", err)
	}
}
