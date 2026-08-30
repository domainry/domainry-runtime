package boundary_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestRuntimeServicesFacadeContainsOnlyApplicationsAndSchemaReader(t *testing.T) {
	path := filepath.Join(runtimeRoot(t), "bootstrap", "composition", "runtime_services.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, specification := range general.Specs {
			typeSpec, ok := specification.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != "RuntimeServices" {
				continue
			}
			structure := typeSpec.Type.(*ast.StructType)
			fields := make([]string, 0, len(structure.Fields.List))
			for _, field := range structure.Fields.List {
				if len(field.Names) != 1 {
					t.Fatal("RuntimeServices must not contain anonymous or grouped service fields")
				}
				fields = append(fields, field.Names[0].Name)
			}
			sort.Strings(fields)
			if strings.Join(fields, ",") != "applications,schema" {
				t.Fatalf("RuntimeServices must expose only immutable RuntimeApplications and schema reader; fields=%v", fields)
			}
			return
		}
	}
	t.Fatal("RuntimeServices facade not found")
}

func TestBootstrapCompositionBusinessBehaviorCannotReturn(t *testing.T) {
	root := filepath.Join(runtimeRoot(t), "bootstrap", "composition")
	policyImportOwners := map[string]bool{
		"changeplan_application_wiring.go":             true,
		"integration_dependency_application_wiring.go": true,
		"record_query_policy_domain_wiring.go":         true,
		"runtime_services_appschema_projection.go":      true,
	}
	forbiddenTemplates := []string{"lead", "opportunity", "restaurant_" + "order", "kitchen_" + "ticket"}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		name := filepath.Base(path)
		if strings.HasSuffix(name, "_orchestration.go") {
			t.Errorf("Bootstrap orchestration belongs in application owner: %s", path)
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		text := string(content)
		if strings.Contains(text, "time.Now(") {
			t.Errorf("Bootstrap business time decision belongs in application/domain owner: %s", path)
		}
		for _, key := range forbiddenTemplates {
			if strings.Contains(text, `"`+key+`"`) {
				t.Errorf("Bootstrap template object %q belongs in its Template owner: %s", key, path)
			}
		}
		if (strings.Contains(text, "/policy\"") || strings.Contains(text, "/validation\"")) && !policyImportOwners[name] {
			t.Errorf("Domain policy/validation construction must use reviewed owner wiring, not %s", path)
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, content, 0)
		if parseErr != nil {
			return parseErr
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if ok && function.Recv == nil && strings.HasPrefix(function.Name.Name, "Configure") {
				t.Errorf("post-construction mutation API %s must become RuntimeServicesDependencies or a stable binding", function.Name.Name)
			}
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.TYPE {
				continue
			}
			for _, specification := range general.Specs {
				typeSpec := specification.(*ast.TypeSpec)
				for _, suffix := range []string{"Executor", "Validator", "ApplicationService"} {
					if strings.HasSuffix(typeSpec.Name.Name, suffix) {
						t.Errorf("Bootstrap type %s must move to its Application/Domain owner", typeSpec.Name.Name)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestBootstrapTestkitIsAnExactFocusedFixtureBoundary(t *testing.T) {
	root := filepath.Join(runtimeRoot(t), "bootstrap", "testkit")
	expected := stringSet(
		"identity_sdk.go", "manifest_identity_sdk.go",
		"runtime_services.go", "runtime_services_fixture.go",
	)
	seen := map[string]bool{}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		seen[name] = true
		if !expected[name] {
			t.Errorf("unreviewed Testkit fixture: %s", name)
		}
		content, readErr := os.ReadFile(filepath.Join(root, name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		text := string(content)
		if strings.Contains(text, "runtime/application/") || strings.Contains(text, "/policy\"") || strings.Contains(text, "/validation\"") || strings.Contains(text, "/service\"") {
			t.Errorf("Testkit may adapt repository contracts only; Domain/Application behavior found in %s", name)
		}
	}
	assertReviewedFilesStillExist(t, "Testkit fixture", expected, seen)
}

func TestBootstrapIntegrationTestContainsOnlyIntegrationTests(t *testing.T) {
	root := filepath.Join(runtimeRoot(t), "bootstrap", "integrationtest")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			if name == "testdata" {
				continue
			}
			t.Errorf("Bootstrap integrationtest must remain one test-only package; nested directory is not reviewed: %s", name)
			continue
		}
		if strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			t.Errorf("Bootstrap integrationtest must not contain production Go code: %s", name)
		}
	}
	reviewedFixtures := stringSet(
		"gym/gym_capability_coverage_v1.json",
		"gym/gym_empty_foundation_manifest.json",
	)
	seenFixtures := map[string]bool{}
	err = filepath.Walk(filepath.Join(root, "testdata"), func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		relative, relErr := filepath.Rel(filepath.Join(root, "testdata"), path)
		if relErr != nil {
			return relErr
		}
		relative = filepath.ToSlash(relative)
		seenFixtures[relative] = true
		if !reviewedFixtures[relative] {
			t.Errorf("unreviewed Bootstrap integration fixture: %s", relative)
		}
		if strings.HasSuffix(relative, ".go") {
			t.Errorf("Bootstrap integration fixture must not contain Go source: %s", relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertReviewedFilesStillExist(t, "Bootstrap integration fixture", reviewedFixtures, seenFixtures)
}
