package boundary_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	operationsprojection "github.com/domainry/domainry-runtime/runtime/domain/operations/projection"
)

func TestEveryOperationsApplicationErrorHasMachineRunbook(t *testing.T) {
	root := filepath.Join(runtimeRoot(t), "application", "operations")
	pattern := regexp.MustCompile(`backend\.operations\.[a-zA-Z0-9_.]+`)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, code := range pattern.FindAllString(string(data), -1) {
			link, found := operationsprojection.OperationsRunbookForError(code)
			if !found || link.URL == "" || len(link.NextActions) == 0 {
				t.Errorf("operations runbook violation: file=%s error_code=%s", path, code)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
