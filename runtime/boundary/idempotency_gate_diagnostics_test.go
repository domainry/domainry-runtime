package boundary_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestIdempotencyGateDiagnosticsAreActionable(t *testing.T) {
	boundaryRoot := filepath.Join(runtimeRoot(t), "boundary")
	entries, err := os.ReadDir(boundaryRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "idempotency_") || !strings.HasSuffix(entry.Name(), "_test.go") || entry.Name() == "idempotency_gate_diagnostics_test.go" {
			continue
		}
		path := filepath.Join(boundaryRoot, entry.Name())
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (selector.Sel.Name != "Error" && selector.Sel.Name != "Errorf" && selector.Sel.Name != "Fatal" && selector.Sel.Name != "Fatalf") {
				return true
			}
			literal, ok := call.Args[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			message, err := strconv.Unquote(literal.Value)
			if err != nil || !strings.Contains(message, "idempotency") {
				return true
			}
			for _, field := range []string{"entry=", "owner=", "file=", "missing_contract="} {
				if !strings.Contains(message, field) {
					t.Errorf("idempotency diagnostics violation: entry=%s owner=boundary file=%s missing_contract=%s", selector.Sel.Name, path, field)
				}
			}
			return true
		})
	}
}
