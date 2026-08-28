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

func TestKnownPrincipalLiteralsDeclareWorkspaceOrSystemScope(t *testing.T) {
	root := runtimeRoot(t)
	files, err := productionGoFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range files {
		set := token.NewFileSet()
		file, err := parser.ParseFile(set, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		aliases := map[string]bool{}
		for _, spec := range file.Imports {
			importPath, _ := strconv.Unquote(spec.Path.Value)
			if importPath != "github.com/domainry/domainry-runtime/runtime/domain/principal/model" {
				continue
			}
			alias := "identitymodel"
			if spec.Name != nil {
				alias = spec.Name.Name
			}
			aliases[alias] = true
		}
		if len(aliases) == 0 {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok || !isIdentityPrincipalLiteral(literal.Type, aliases) {
				return true
			}
			known, workspace, system := false, false, false
			for _, element := range literal.Elts {
				field, ok := element.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				name, _ := field.Key.(*ast.Ident)
				if name == nil {
					continue
				}
				switch name.Name {
				case "Known":
					value, _ := field.Value.(*ast.Ident)
					known = value != nil && value.Name == "true"
				case "WorkspaceID":
					workspace = true
				case "SystemScope":
					system = true
				}
			}
			if known && !workspace && !system {
				position := set.Position(literal.Pos())
				relative, _ := filepath.Rel(root, position.Filename)
				t.Errorf("known Principal literal must declare WorkspaceID or explicit SystemScope: %s:%d", filepath.ToSlash(relative), position.Line)
			}
			return true
		})
	}
}

func productionGoFiles(root string) ([]string, error) {
	files := []string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if strings.Contains(filepath.ToSlash(path), "/testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) == ".go" && !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func isIdentityPrincipalLiteral(expression ast.Expr, aliases map[string]bool) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Principal" {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && aliases[pkg.Name]
}
