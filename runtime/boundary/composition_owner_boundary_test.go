package boundary_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestIntegrationBootstrapCompositionContainsWiringOnly(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve runtime test path")
	}
	compositionDir := filepath.Join(filepath.Dir(filepath.Dir(current)), "bootstrap", "composition")
	allowedProduction := stringSet("integration_requirements_wiring.go", "integration_runtime_application_wiring.go")
	entries, err := os.ReadDir(compositionDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || !strings.Contains(name, "integration") {
			continue
		}
		if !allowedProduction[name] {
			t.Errorf("unreviewed Integration file returned to bootstrap/composition: %s", name)
		}
	}
	for name := range allowedProduction {
		if _, err := os.Stat(filepath.Join(compositionDir, name)); err != nil {
			t.Errorf("reviewed Integration boundary file missing: %s: %v", name, err)
		}
	}
}

func TestAutomationBootstrapCompositionContainsWiringAndRuntimeContractOnly(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve runtime test path")
	}
	compositionDir := filepath.Join(filepath.Dir(filepath.Dir(current)), "bootstrap", "composition")
	allowedProduction := stringSet("automation_application_wiring.go")
	entries, err := os.ReadDir(compositionDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || !strings.Contains(name, "automation") {
			continue
		}
		if strings.HasSuffix(name, "_test.go") || !allowedProduction[name] {
			t.Errorf("unreviewed Automation file returned to bootstrap/composition: %s", name)
		}
	}
	for name := range allowedProduction {
		if _, err := os.Stat(filepath.Join(compositionDir, name)); err != nil {
			t.Errorf("reviewed Automation boundary file missing: %s: %v", name, err)
		}
	}
}

func TestApplicationServiceNamesAreNotTypeAliases(t *testing.T) {
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve runtime test path")
	}
	compositionDir := filepath.Join(filepath.Dir(filepath.Dir(current)), "bootstrap", "composition")
	entries, err := os.ReadDir(compositionDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(compositionDir, entry.Name())
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			t.Errorf("parse %s: %v", entry.Name(), parseErr)
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			typeSpec, isType := node.(*ast.TypeSpec)
			if isType && strings.HasSuffix(typeSpec.Name.Name, "ApplicationService") && typeSpec.Assign.IsValid() {
				t.Errorf("Application Service must be a real application type, not an alias: %s declares %s", entry.Name(), typeSpec.Name.Name)
			}
			return true
		})
	}
}

func assertAliasOnlyGoFile(t *testing.T, path string) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok {
			t.Errorf("pure compatibility file %s contains function %s", filepath.Base(path), function.Name.Name)
		}
	}
}

func stringSet(values ...string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}
