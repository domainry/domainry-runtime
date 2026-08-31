package boundary_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentOwnsLifecycleExecution(t *testing.T) {
	root := runtimeRoot(t)
	removed := filepath.Join(root, "infrastructure", "persistence", "agentlifecycle")
	if info, err := os.Stat(removed); err == nil && info.IsDir() {
		t.Fatal("Agent lifecycle implementation returned to Runtime")
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	forbidden := map[string]string{
		"AgentLifecycleRepository": "Agent lifecycle repository access",
		"agent.state":              "Agent lifecycle resource implementation",
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
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
		relative, _ := filepath.Rel(root, path)
		for fragment, reason := range forbidden {
			if strings.Contains(string(content), fragment) {
				t.Errorf("Runtime production contains %s %q in %s", reason, fragment, filepath.ToSlash(relative))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
