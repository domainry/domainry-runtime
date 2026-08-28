package boundary_test

import (
	"go/ast"
	"path/filepath"
	"strconv"
	"testing"
)

func TestRuntimeProductionCodeUsesStructuredLogger(t *testing.T) {
	forbiddenSelectors := map[string]map[string]bool{
		"fmt": {
			"Print": true, "Printf": true, "Println": true,
		},
		"log": {
			"Print": true, "Printf": true, "Println": true,
			"Fatal": true, "Fatalf": true, "Fatalln": true,
			"Panic": true, "Panicf": true, "Panicln": true,
		},
	}

	walkProductionGo(t, runtimeRoot(t), func(path string, file *ast.File) {
		standardLoggerAliases := map[string]string{}
		standardOutputAliases := map[string]bool{}
		for _, imported := range file.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil || (importPath != "fmt" && importPath != "log" && importPath != "os") {
				continue
			}
			alias := filepath.Base(importPath)
			if imported.Name != nil {
				alias = imported.Name.Name
			}
			if importPath == "os" {
				standardOutputAliases[alias] = true
			} else {
				standardLoggerAliases[alias] = importPath
			}
		}

		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if identifier, ok := call.Fun.(*ast.Ident); ok && (identifier.Name == "print" || identifier.Name == "println") {
				t.Errorf("Runtime production code must use platform/logging (zap), not builtin %s: %s", identifier.Name, path)
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			identifier, ok := selector.X.(*ast.Ident)
			if !ok {
				return true
			}
			importPath := standardLoggerAliases[identifier.Name]
			if forbiddenSelectors[importPath][selector.Sel.Name] {
				t.Errorf("Runtime production code must use platform/logging (zap), not %s.%s: %s", importPath, selector.Sel.Name, path)
			}
			if importPath == "fmt" && isStandardOutputWrite(call, selector.Sel.Name, standardOutputAliases) {
				t.Errorf("Runtime production code must use platform/logging (zap), not fmt.%s to stdout/stderr: %s", selector.Sel.Name, path)
			}
			return true
		})
	})
}

func isStandardOutputWrite(call *ast.CallExpr, selector string, osAliases map[string]bool) bool {
	if selector != "Fprint" && selector != "Fprintf" && selector != "Fprintln" || len(call.Args) == 0 {
		return false
	}
	destination, ok := call.Args[0].(*ast.SelectorExpr)
	if !ok || (destination.Sel.Name != "Stdout" && destination.Sel.Name != "Stderr") {
		return false
	}
	identifier, ok := destination.X.(*ast.Ident)
	return ok && osAliases[identifier.Name]
}
