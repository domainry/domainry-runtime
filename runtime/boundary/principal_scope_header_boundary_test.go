package boundary_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestClientHeadersCannotBecomePrincipalOrganizationScope(t *testing.T) {
	httpRoot := filepath.Join(runtimeRoot(t), "transport", "http")
	err := filepath.WalkDir(httpRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		fileset := token.NewFileSet()
		parsed, parseErr := parser.ParseFile(fileset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			method, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || (method.Sel.Name != "Get" && method.Sel.Name != "Values") {
				return true
			}
			header, ok := method.X.(*ast.SelectorExpr)
			if !ok || header.Sel.Name != "Header" {
				return true
			}
			literal, ok := call.Args[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			name, unquoteErr := strconv.Unquote(literal.Value)
			if unquoteErr != nil || !isForbiddenPrincipalScopeHeader(name) {
				return true
			}
			relative, _ := filepath.Rel(httpRoot, path)
			t.Errorf("client header %q may not supply Principal organization scope: %s:%d", name, filepath.ToSlash(relative), fileset.Position(call.Pos()).Line)
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func isForbiddenPrincipalScopeHeader(header string) bool {
	normalized := strings.ToLower(strings.TrimSpace(header))
	if !strings.HasPrefix(normalized, "x-") {
		return false
	}
	if strings.Contains(normalized, "scope") {
		return true
	}
	for _, owner := range []string{
		"department", "organization-unit", "org-unit", "manager", "subordinate",
		"team", "store", "territory", "warehouse",
	} {
		if strings.Contains(normalized, owner) &&
			(strings.HasSuffix(normalized, "-id") || strings.HasSuffix(normalized, "-ids")) {
			return true
		}
	}
	return false
}
