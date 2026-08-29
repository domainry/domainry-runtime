package database_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestWorkflowAndIntegrationRepositoriesUseStructuredBuilders(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve persistence boundary path")
	}
	root := filepath.Dir(source)
	for _, owner := range []string{"workflow", "integration"} {
		err := filepath.WalkDir(filepath.Join(root, owner), func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") || entry.Name() == "provider_state_legacy_migration.go" {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(raw), "InsertStatement(") {
				t.Errorf("%s bypasses structured insert builders", path)
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, raw, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(node ast.Node) bool {
				literal, ok := node.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(literal.Value)
				if err != nil {
					return true
				}
				upper := strings.ToUpper(strings.TrimSpace(value))
				looksLikeSQL := (strings.HasPrefix(upper, "SELECT ") && strings.Contains(upper, " FROM ")) ||
					strings.HasPrefix(upper, "INSERT INTO ") ||
					(strings.HasPrefix(upper, "UPDATE ") && strings.Contains(upper, " SET ")) ||
					strings.HasPrefix(upper, "DELETE FROM ")
				if looksLikeSQL {
					t.Errorf("%s contains hand-written business SQL", path)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
