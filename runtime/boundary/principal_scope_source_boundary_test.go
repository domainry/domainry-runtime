package boundary_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestHTTPTransportCannotReadClientReportedAuthorizationScope(t *testing.T) {
	root := filepath.Join(runtimeRoot(t), "transport", "http")
	forbiddenHeaders := map[string]struct{}{
		"x-team-ids":      {},
		"x-store-ids":     {},
		"x-territory-ids": {},
		"x-warehouse-ids": {},
	}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return walkErr
		}
		source, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		fileSet := token.NewFileSet()
		file, parseErr := parser.ParseFile(fileSet, path, source, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, unquoteErr := strconv.Unquote(literal.Value)
			if unquoteErr != nil {
				return true
			}
			if _, forbidden := forbiddenHeaders[strings.ToLower(strings.TrimSpace(value))]; forbidden {
				position := fileSet.Position(literal.Pos())
				t.Errorf("untrusted authorization scope header %q is forbidden in Runtime HTTP transport: %s:%d", value, filepath.ToSlash(path), position.Line)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
