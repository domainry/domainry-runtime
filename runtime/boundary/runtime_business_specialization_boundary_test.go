package boundary

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type runtimeBusinessOwnershipBoundary struct {
	Version string `json:"version"`
	Groups  []struct {
		ID                     string   `json:"id"`
		Owner                  string   `json:"owner"`
		RuntimePolicy          string   `json:"runtime_policy"`
		Capabilities           []string `json:"capabilities"`
		ForbiddenRuntimeTokens []string `json:"forbidden_runtime_tokens"`
	} `json:"groups"`
}

func TestRuntimeProductionGoHasNoBusinessOwnedSpecialization(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	runtimeRoot := filepath.Join(repositoryRoot, "runtime")
	content, err := os.ReadFile(filepath.Join("testdata", "runtime-business-ownership-boundary.json"))
	if err != nil {
		t.Fatal(err)
	}
	inventory := runtimeBusinessOwnershipBoundary{}
	if err := json.Unmarshal(content, &inventory); err != nil {
		t.Fatal(err)
	}
	if inventory.Version != "v1" || len(inventory.Groups) == 0 {
		t.Fatalf("invalid business ownership inventory: %#v", inventory)
	}
	forbidden := []string{}
	for _, group := range inventory.Groups {
		if group.ID == "" || group.Owner != "business_project_source" || group.RuntimePolicy != "forbidden_specialization" ||
			len(group.Capabilities) == 0 || len(group.ForbiddenRuntimeTokens) == 0 {
			t.Fatalf("invalid business ownership group: %#v", group)
		}
		forbidden = append(forbidden, group.ForbiddenRuntimeTokens...)
	}
	err = filepath.WalkDir(runtimeRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		normalized := strings.ToLower(string(content))
		for _, token := range forbidden {
			if strings.Contains(normalized, token) {
				t.Errorf("production Runtime specialization token %q found in %s", token, filepath.ToSlash(path))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGymRemainsAnE2EFixtureAndNeverEntersTheRuntimeProductionPackage(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join("..", ".."))
	runtimeRoot := filepath.Join(repositoryRoot, "runtime")
	for _, fixture := range []string{
		filepath.Join(runtimeRoot, "bootstrap", "integrationtest", "gym_business_lifecycle_p6_e2e_test.go"),
		filepath.Join(runtimeRoot, "bootstrap", "integrationtest", "gym_business_analytics_p7_e2e_test.go"),
		filepath.Join(runtimeRoot, "bootstrap", "integrationtest", "testdata", "gym", "gym_capability_coverage_v1.json"),
	} {
		if info, err := os.Stat(fixture); err != nil || info.IsDir() {
			t.Fatalf("required Gym E2E fixture is missing: %s err=%v", filepath.ToSlash(fixture), err)
		}
	}
	err := filepath.WalkDir(runtimeRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(strings.ToLower(string(content)), "gym") {
			t.Errorf("Gym fixture token entered Runtime production package: %s", filepath.ToSlash(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
