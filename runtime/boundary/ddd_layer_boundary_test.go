package boundary_test

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestRuntimeDDDLayerImportBoundary(t *testing.T) {
	root := runtimeRoot(t)
	module := "github.com/domainry/domainry-runtime/runtime/"
	forbidden := map[string][]string{
		"domain":      {"application/", "bootstrap/", "infrastructure/", "transport/"},
		"application": {"bootstrap/", "infrastructure/", "transport/"},
		"transport":   {"bootstrap/", "infrastructure/"},
	}
	violations := []string{}
	walkProductionGo(t, root, func(path string, file *ast.File) {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		layer := strings.Split(filepath.ToSlash(rel), "/")[0]
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil || !strings.HasPrefix(importPath, module) {
				continue
			}
			dependency := strings.TrimPrefix(importPath, module)
			for _, prefix := range forbidden[layer] {
				if strings.HasPrefix(dependency, prefix) {
					violations = append(violations, filepath.ToSlash(rel)+" -> "+importPath)
				}
			}
		}
	})
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("DDD layer import violations:\n%s", strings.Join(violations, "\n"))
	}
}

func TestHTTPTransportDependsOnApplicationNotDomainOrCompositionServices(t *testing.T) {
	root := filepath.Join(runtimeRoot(t), "transport", "http")
	violations := []string{}
	walkProductionGo(t, root, func(path string, file *ast.File) {
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.TYPE {
				continue
			}
			for _, specification := range general.Specs {
				typeSpec, ok := specification.(*ast.TypeSpec)
				if !ok || (typeSpec.Name.Name != "Handler" && typeSpec.Name.Name != "Dependencies" && typeSpec.Name.Name != "Server") {
					continue
				}
				var rendered bytes.Buffer
				if err := format.Node(&rendered, token.NewFileSet(), typeSpec.Type); err != nil {
					t.Fatal(err)
				}
				body := rendered.String()
				for _, forbidden := range []string{"RuntimeServices", "DomainService"} {
					if strings.Contains(body, forbidden) {
						violations = append(violations, path+" declares "+typeSpec.Name.Name+" with "+forbidden)
					}
				}
			}
		}
	})
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("HTTP transport bypasses Application ownership:\n%s", strings.Join(violations, "\n"))
	}
}

func TestBootstrapRuntimeExposesOnlyProcessAPI(t *testing.T) {
	root := filepath.Join(runtimeRoot(t), "bootstrap")
	allowed := map[string]bool{
		"Routes":                             true,
		"Close":                              true,
		"CloseContext":                       true,
		"StartWorkflowWorker":                true,
		"StartIntegrationEventWorker":        true,
		"StartIntegrationOutboxWorker":       true,
		"StartNotificationPublicationWorker": true,
	}
	violations := []string{}
	walkProductionGo(t, root, func(path string, file *ast.File) {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || !function.Name.IsExported() || !runtimeReceiver(function.Recv) {
				continue
			}
			if !allowed[function.Name.Name] {
				violations = append(violations, filepath.Base(path)+":"+function.Name.Name)
			}
		}
	})
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("Bootstrap Runtime exposes non-process API:\n%s", strings.Join(violations, "\n"))
	}
}

func runtimeReceiver(receivers *ast.FieldList) bool {
	if receivers == nil || len(receivers.List) != 1 {
		return false
	}
	receiver := receivers.List[0].Type
	if pointer, ok := receiver.(*ast.StarExpr); ok {
		receiver = pointer.X
	}
	identifier, ok := receiver.(*ast.Ident)
	return ok && identifier.Name == "Runtime"
}
