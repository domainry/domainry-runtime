package boundary_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportPersistenceRemainsKeysetOnly(t *testing.T) {
	root := filepath.Join(runtimeRoot(t), "infrastructure", "persistence", "database", "report")
	var violations []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		source, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		text := string(source)
		if strings.Contains(strings.ToUpper(text), `" OFFSET "`) || strings.Contains(strings.ToUpper(text), "` OFFSET ") || strings.Contains(text, "PageOffset") {
			violations = append(violations, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) > 0 {
		t.Fatalf("Report persistence must remain keyset-only: %s", strings.Join(violations, ", "))
	}
}
