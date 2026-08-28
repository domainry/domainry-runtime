package boundary_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeOpenAPIRemainsCodeFirst(t *testing.T) {
	root := runtimeRoot(t)
	documentPath := filepath.Join(root, "..", "docs", "architecture", "runtime-openapi-governance.md")
	raw, err := os.ReadFile(documentPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "code-first OpenAPI generation") {
		t.Fatal("runtime OpenAPI governance must retain an explicit code-first decision")
	}
	forbidden := []string{"openapi.yaml", "openapi.yml", "swagger.yaml", "swagger.yml", "openapi.json", "swagger.json"}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || strings.HasSuffix(path, "server_openapi.go") {
			return nil
		}
		name := strings.ToLower(entry.Name())
		for _, candidate := range forbidden {
			if name == candidate {
				t.Errorf("static Runtime OpenAPI source %s violates the code-first decision", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
