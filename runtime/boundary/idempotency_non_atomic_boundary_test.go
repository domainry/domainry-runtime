package boundary_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type nonAtomicIdempotencyPattern struct {
	check string
	write string
}

func TestNonAtomicIdempotencyPatternsOnlyDecrease(t *testing.T) {
	root := runtimeRoot(t)
	patterns := []nonAtomicIdempotencyPattern{
		{check: "CachedObjectResult", write: "SaveExecution"},
		{check: "CachedRecordResult", write: "SaveExecution"},
	}
	reviewed := map[string]bool{}
	found := map[string]bool{}
	fileSet := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		parsed, parseErr := parser.ParseFile(fileSet, path, raw, 0)
		if parseErr != nil {
			return parseErr
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			calls := map[string]bool{}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch callee := call.Fun.(type) {
				case *ast.Ident:
					calls[callee.Name] = true
				case *ast.SelectorExpr:
					calls[callee.Sel.Name] = true
				}
				return true
			})
			for _, pattern := range patterns {
				if !calls[pattern.check] || !calls[pattern.write] {
					continue
				}
				key := relative + ":" + function.Name.Name
				found[key] = true
				if !reviewed[key] {
					owner := strings.Split(filepath.ToSlash(relative), "/")[0]
					t.Errorf("idempotency gate violation: entry=%s owner=%s file=%s missing_contract=atomic claim; calls=%s,%s", function.Name.Name, owner, path, pattern.check, pattern.write)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for path := range reviewed {
		if !found[path] {
			t.Errorf("idempotency gate violation: entry=%s owner=cross-owner file=%s missing_contract=remove retired baseline", path, path)
		}
	}
}
