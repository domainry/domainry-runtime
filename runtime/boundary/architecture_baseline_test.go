package boundary_test

import (
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	runtimeServicesMethodBaseline             = 4
	runtimeRouteBaseline                      = 4
	legacyStorageMethodBaseline               = 0
	compositionProductionFileBaseline         = 32
	compositionTestFileBaseline               = 10
	compositionApplicationServiceTypeBaseline = 0
)

func TestRuntimeArchitectureBaselinesOnlyDecrease(t *testing.T) {
	root := runtimeRoot(t)
	serviceRoot := filepath.Join(root, "bootstrap", "composition")
	recordRoot := filepath.Join(root, "domain", "record")

	assertAtMost(t, "RuntimeServices receiver methods", countReceiverMethods(t, serviceRoot, "RuntimeServices"), runtimeServicesMethodBaseline)
	assertAtMost(t, "ctx-first RecordRepository methods", countInterfaceMethods(t, filepath.Join(recordRoot, "repository", "record_repository.go"), "RecordRepository"), 15)
	assertAtMost(t, "HTTPRouter.Routes registrations", countRouteRegistrations(t, filepath.Join(root, "transport", "http", "http_router.go")), runtimeRouteBaseline)
	assertAtMost(t, "legacy storage exported methods without ctx", countStorageMethodsWithoutContext(t, filepath.Join(root, "infrastructure", "persistence")), legacyStorageMethodBaseline)
	assertAtMost(t, "bootstrap/composition production files", countProductionGoFiles(t, serviceRoot), compositionProductionFileBaseline)
	assertAtMost(t, "bootstrap/composition test files", countTestGoFiles(t, serviceRoot), compositionTestFileBaseline)
	assertAtMost(t, "bootstrap/composition Application Service types", countNamedStructTypes(t, serviceRoot, "ApplicationService"), compositionApplicationServiceTypeBaseline)
	if entries, err := os.ReadDir(filepath.Join(root, "domain", "core")); err == nil && len(entries) != 0 {
		t.Fatalf("domain/core must stay empty after composition migration: %d entries", len(entries))
	}
}

func countNamedStructTypes(t *testing.T, root, suffix string) int {
	t.Helper()
	count := 0
	walkAllGo(t, root, func(_ string, file *ast.File) {
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.TYPE {
				continue
			}
			for _, specification := range general.Specs {
				typeSpec, ok := specification.(*ast.TypeSpec)
				if !ok || !strings.HasSuffix(typeSpec.Name.Name, suffix) {
					continue
				}
				if _, ok := typeSpec.Type.(*ast.StructType); ok {
					count++
				}
			}
		}
	})
	return count
}

func TestRuntimeBusinessPackageInventoryIsCurrent(t *testing.T) {
	root := runtimeRoot(t)
	inventoryPath := filepath.Join("testdata", "runtime-domain-package-migration-inventory.md")
	content, err := os.ReadFile(inventoryPath)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("Service source fingerprint: `%x`", sourceFingerprint(t, filepath.Join(root, "bootstrap", "composition")))
	if !strings.Contains(string(content), want) {
		t.Fatalf("runtime domain package inventory is stale; run go run scripts/contracts/runtime_business_package_inventory.go")
	}
}

func TestRuntimeRepositoryOwnsOnlyItsPublicRuntimePackages(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join(runtimeRoot(t), ".."))
	for _, packageName := range []string{"runtimeext", "runtimehost"} {
		if _, err := os.Stat(filepath.Join(repositoryRoot, packageName)); !os.IsNotExist(err) {
			t.Fatalf("legacy root package directory %q must stay absent: %v", packageName, err)
		}
		if info, err := os.Stat(filepath.Join(repositoryRoot, "pkg", packageName)); err != nil || !info.IsDir() {
			t.Fatalf("public package pkg/%s is missing or not a directory: %v", packageName, err)
		}
	}
	if _, err := os.Stat(filepath.Join(repositoryRoot, "pkg", "connector")); !os.IsNotExist(err) {
		t.Fatalf("Plane-owned Connector SDK package must stay absent after extraction: %v", err)
	}
	for _, sourceRoot := range []string{"runtime", "pkg"} {
		err := filepath.WalkDir(filepath.Join(repositoryRoot, sourceRoot), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || (!strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, ".tmpl")) {
				return err
			}
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			for _, packageName := range []string{"runtimeext", "connector", "runtimehost"} {
				legacyImport := "github.com/domainry/domainry-plane/" + packageName
				if strings.Contains(string(content), legacyImport) {
					t.Errorf("%s still references legacy public import %q", path, legacyImport)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestRuntimeTargetPackageNamesMatchOwners(t *testing.T) {
	root := runtimeRoot(t)
	targets := map[string]string{
		"domain/action":                                          "action",
		"domain/action/model":                                    "actionmodel",
		"domain/agent":                                           "agent",
		"domain/agent/model":                                     "agentmodel",
		"platform/apperror":                                      "apperror",
		"platform/collection":                                    "collection",
		"platform/filelock":                                      "filelock",
		"domain/audit/model":                                     "auditmodel",
		"domain/audit/repository":                                "repository",
		"domain/audit/service":                                   "service",
		"domain/automation":                                      "automation",
		"domain/automation/model":                                "automationmodel",
		"domain/businessseed/model":                              "businessseedmodel",
		"domain/businessseed/repository":                         "repository",
		"domain/capability":                                      "capability",
		"domain/changeplan":                                      "changeplan",
		"domain/changeplan/model":                                "changeplanmodel",
		"domain/definition/model":                                "definitionmodel",
		"domain/deployment":                                      "deployment",
		"domain/deployment/model":                                "deploymentmodel",
		"domain/integration":                                     "integration",
		"domain/integration/model":                               "integrationmodel",
		"domain/localization/model":                              "localizationmodel",
		"domain/manifest/model":                                  "manifestmodel",
		"domain/manifest/repository":                             "repository",
		"platform/mutation":                                      "mutation",
		"platform/safehttp":                                      "safehttp",
		"domain/notification":                                    "notification",
		"domain/pipeline":                                        "pipeline",
		"domain/principal/model":                                 "principalmodel",
		"domain/record":                                          "record",
		"domain/record/model":                                    "recordmodel",
		"domain/report/model":                                    "reportmodel",
		"domain/report/service":                                  "service",
		"domain/scheduler":                                       "scheduler",
		"domain/surfacecontext/model":                            "surfacecontextmodel",
		"application/surfacecontext":                             "surfacecontext",
		"application/principal":                                  "principal",
		"domain/transaction/model":                               "transactionmodel",
		"domain/workflow":                                        "workflow",
		"domain/workflow/model":                                  "workflowmodel",
		"domain/workflow/repository":                             "repository",
		"bootstrap/integrationtest":                              "integrationtest",
		"bootstrap/composition":                                  "composition",
		"application/seed/automation":                            "automationseed",
		"application/seed/business":                              "businessseed",
		"application/seed/deployment":                            "deploymentseed",
		"application/seed/globalcapability":                      "globalcapabilityseed",
		"bootstrap/testkit":                                      "testkit",
		"domain/manifest":                                        "manifest",
		"domain/appschema":                                        "appschema",
		"domain/appschema/model":                                  "appschemamodel",
		"infrastructure/persistence/database":                    "database",
		"infrastructure/persistence/database/action":             "action",
		"infrastructure/persistence/database/agent":              "agent",
		"infrastructure/persistence/database/automation":         "automation",
		"infrastructure/persistence/database/bootstrap":          "bootstrap",
		"infrastructure/persistence/database/changeplan":         "changeplan",
		"infrastructure/persistence/database/deployment":         "deployment",
		"infrastructure/persistence/database/dialecttest":        "dialecttest",
		"infrastructure/persistence/database/failure":            "failure",
		"infrastructure/persistence/database/frontendcapability": "frontendcapability",
		"infrastructure/persistence/database/integration":        "integration",
		"infrastructure/persistence/database/appschema":           "appschema",
		"infrastructure/persistence/database/migration":          "migration",
		"infrastructure/persistence/database/query":              "query",
		"infrastructure/persistence/database/ratelimit":          "ratelimit",
		"infrastructure/persistence/database/record":             "record",
		"infrastructure/persistence/database/schema":             "schema",
		"infrastructure/persistence/database/workflow":           "workflow",
		"infrastructure/persistence/driver":                      "driver",
		"infrastructure/persistence/mysql":                       "mysql",
		"infrastructure/persistence/postgres":                    "postgres",
		"infrastructure/persistence/sqlite":                      "sqlite",
		"bootstrap":                                              "bootstrap",
		"platform/localization":                                  "localization",
		"platform/resilience":                                    "resilience",
		"transport/http":                                         "http",
		"transport/http/agentdialog":                             "agentdialog",
		"transport/http/automation":                              "automation",
		"transport/http/businessreferences":                      "businessreferences",
		"transport/http/businesssystem":                          "businesssystem",
		"transport/http/capabilities":                            "capabilities",
		"transport/http/changeplans":                             "changeplans",
		"transport/http/discovery":                               "discovery",
		"transport/http/frontendcapability":                      "frontendcapability",
		"transport/http/integrations":                            "integrations",
		"transport/http/appschema":                                "appschema",
		"transport/http/openapi":                                 "openapi",
		"transport/http/records":                                 "records",
		"transport/http/reports":                                 "reports",
		"transport/http/scheduler":                               "scheduler",
		"transport/http/surfacecontext":                          "surfacecontext",
		"transport/http/uploads":                                 "uploads",
		"transport/http/workflows":                               "workflows",
	}
	for relative, want := range targets {
		directory := filepath.Join(root, filepath.FromSlash(relative))
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatalf("read target package %s: %v", relative, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
				continue
			}
			path := filepath.Join(directory, entry.Name())
			parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.PackageClauseOnly)
			if err != nil {
				t.Fatalf("parse package declaration %s: %v", path, err)
			}
			allowed := parsed.Name.Name == want || strings.HasSuffix(entry.Name(), "_test.go") && parsed.Name.Name == want+"_test"
			if !allowed {
				t.Errorf("%s declares package %s, want %s", filepath.ToSlash(path), parsed.Name.Name, want)
			}
		}
	}
}

func TestRuntimeBusinessOwnerPackagesDoNotImportComposition(t *testing.T) {
	root := runtimeRoot(t)
	businessRoot := filepath.Join(root, "domain")
	compositionImport := "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	walkProductionGo(t, businessRoot, func(path string, file *ast.File) {
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("unquote import in %s: %v", path, err)
			}
			if importPath == compositionImport || strings.HasPrefix(importPath, compositionImport+"/") {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("domain owner package imports bootstrap composition: %s -> %s", filepath.ToSlash(rel), importPath)
			}
		}
	})
}

func TestRuntimeDomainOwnersDoNotImportOtherOwnerBehaviorPackages(t *testing.T) {
	root := runtimeRoot(t)
	domainRoot := filepath.Join(root, "domain")
	moduleDomain := "github.com/domainry/domainry-runtime/runtime/domain/"
	forbiddenRoles := map[string]bool{"service": true, "repository": true, "runtime": true}
	violations := []string{}
	walkProductionGo(t, domainRoot, func(path string, file *ast.File) {
		relative, err := filepath.Rel(domainRoot, path)
		if err != nil {
			t.Fatal(err)
		}
		parts := strings.Split(filepath.ToSlash(relative), "/")
		if len(parts) < 2 {
			return
		}
		owner := parts[0]
		for _, specification := range file.Imports {
			importPath, err := strconv.Unquote(specification.Path.Value)
			if err != nil || !strings.HasPrefix(importPath, moduleDomain) {
				continue
			}
			dependency := strings.Split(strings.TrimPrefix(importPath, moduleDomain), "/")
			if len(dependency) < 2 || dependency[0] == owner || !forbiddenRoles[dependency[1]] {
				continue
			}
			violations = append(violations, filepath.ToSlash(relative)+" -> "+importPath)
		}
	})
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("Domain owner imports another owner's behavior package:\n%s", strings.Join(violations, "\n"))
	}
}

func TestRuntimeDomainDependenciesAreConstructorWired(t *testing.T) {
	root := filepath.Join(runtimeRoot(t), "domain")
	violations := []string{}
	walkProductionGo(t, root, func(path string, file *ast.File) {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || !strings.HasPrefix(function.Name.Name, "Set") {
				continue
			}
			if function.Type.Params != nil && len(function.Type.Params.List) > 0 && isContextType(function.Type.Params.List[0].Type) {
				continue
			}
			relative, _ := filepath.Rel(runtimeRoot(t), path)
			violations = append(violations, filepath.ToSlash(relative)+":"+function.Name.Name)
		}
	})
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("Domain dependency wiring must be constructor-only:\n%s", strings.Join(violations, "\n"))
	}
}

func TestRuntimeDatabaseRootHasNoContextAdapters(t *testing.T) {
	root := filepath.Join(runtimeRoot(t), "infrastructure", "persistence", "database")
	matches, err := filepath.Glob(filepath.Join(root, "context_*.go"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("database root still contains domain context adapters: %v", matches)
	}
}

func TestRuntimeDatabaseRootContainsOnlyTechnicalSubstrate(t *testing.T) {
	root := filepath.Join(runtimeRoot(t), "infrastructure", "persistence", "database")
	allowed := map[string]bool{
		"action_execution_context.go": true, "adapter_seams.go": true, "dialect.go": true,
		"engine.go": true, "runtime_store_connection_strategy.go": true,
		"runtime_operational_metrics.go": true, "runtime_schema.go": true,
		"runtime_store_open_dependencies.go": true, "store_migration_backup.go": true,
		"runtime_store.go": true, "store_migration_status.go": true, "store_migrations.go": true,
		"module_migration_baseline.go": true, "module_migrations.go": true, "project_database.go": true,
		"store_sql.go":     true,
		"workspace_rls.go": true, "workspace_scope_migration.go": true,
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if !allowed[name] {
			t.Errorf("database root production file is not technical substrate: %s", name)
		}
	}
}

func TestRuntimeTargetLayerImportBoundaries(t *testing.T) {
	root := runtimeRoot(t)
	module := "github.com/domainry/domainry-runtime/runtime/"
	allowed := map[string]bool{}
	rules := []struct {
		layer     string
		forbidden []string
	}{
		{layer: "domain", forbidden: []string{"transport", "infrastructure", "application"}},
		{layer: "application", forbidden: []string{"transport", "infrastructure"}},
		{layer: "infrastructure", forbidden: []string{"transport"}},
		{layer: "platform", forbidden: []string{"domain", "transport", "infrastructure"}},
	}
	for _, rule := range rules {
		layerRoot := filepath.Join(root, rule.layer)
		if _, err := os.Stat(layerRoot); os.IsNotExist(err) {
			continue
		} else if err != nil {
			t.Fatal(err)
		}
		walkProductionGo(t, layerRoot, func(path string, file *ast.File) {
			for _, spec := range file.Imports {
				importPath, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					t.Fatalf("unquote import in %s: %v", path, err)
				}
				for _, forbidden := range rule.forbidden {
					prefix := module + forbidden
					if importPath == prefix || strings.HasPrefix(importPath, prefix+"/") {
						rel, _ := filepath.Rel(root, path)
						key := filepath.ToSlash(rel) + " -> " + importPath
						if _, ok := allowed[key]; ok {
							allowed[key] = true
							continue
						}
						t.Errorf("runtime %s layer boundary violation: %s -> %s", rule.layer, filepath.ToSlash(rel), importPath)
					}
				}
			}
		})
	}
	for dependency, found := range allowed {
		if !found {
			t.Errorf("runtime layer boundary allowlist is stale: %s", dependency)
		}
	}
}

func TestRuntimeHasNoCatchAllTopLevelPackages(t *testing.T) {
	root := runtimeRoot(t)
	for _, name := range []string{"library", "common", "utils", "shared"} {
		path := filepath.Join(root, name)
		if _, err := os.Stat(path); err == nil {
			t.Errorf("runtime catch-all package is forbidden: runtime/%s", name)
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
}

func TestStorageSQLCallsAreContextAware(t *testing.T) {
	root := filepath.Join(runtimeRoot(t), "infrastructure", "persistence")
	forbidden := []string{".Query(", ".QueryRow(", ".Exec(", ".Prepare(", ".Begin(", ".Ping(", "context.Background(", "context.TODO("}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, pattern := range forbidden {
			if strings.Contains(string(content), pattern) {
				t.Errorf("storage production code %s contains non-context call %q", path, pattern)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestProductionCompositionRootUsesExplicitRuntimeServicesDependencies(t *testing.T) {
	root := runtimeRoot(t)
	content, err := os.ReadFile(filepath.Join(root, "bootstrap", "runtime", "service_assembly.go"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if !strings.Contains(text, "NewRuntimeServices(ctx, composition.RuntimeServicesConfig{") || !strings.Contains(text, "Dependencies: composition.RuntimeServicesDependencies{") {
		t.Fatal("bootstrap composition root must inject a typed Runtime manifest and explicit dependencies")
	}
	for _, forbidden := range []string{
		"ConfigureRecordRepository(records",
		"ConfigureAuditRepository(records",
		"ConfigureMetadataRepository(records",
		"ConfigureIntegrationRepositories(records",
		"ConfigureWorkflowWorkerRepository(records",
		"ConfigureAutomationWorkerRepository(records",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("bootstrap composition root must not discover then overwrite dependencies via %s", forbidden)
		}
	}
}

func TestRuntimeHasNoAutomaticRecordToPipelineProjection(t *testing.T) {
	root := runtimeRoot(t)
	for _, relative := range []string{"application", "domain", filepath.Join("bootstrap", "composition")} {
		err := filepath.WalkDir(filepath.Join(root, relative), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			for _, forbidden := range []string{
				"SyncCRM" + "RecordPipeline",
				"ProjectRecord" + "ToPipeline",
				"PipelineCRM" + "Sync",
				"PipelineProjection" + "Schema",
				"SyncRecord" + "Pipeline",
			} {
				if strings.Contains(string(content), forbidden) {
					t.Errorf("%s contains removed automatic record-to-pipeline projection symbol %q", path, forbidden)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestHTTPHandlersUseRecordApplicationBoundaries(t *testing.T) {
	root := runtimeRoot(t)
	directCall := regexp.MustCompile(`\.records\.([A-Z][A-Za-z0-9_]*)\(`)
	err := filepath.WalkDir(filepath.Join(root, "transport", "http"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, match := range directCall.FindAllStringSubmatch(string(content), -1) {
			if match[1] != "Applications" {
				t.Errorf("HTTP handler %s calls RuntimeServices.%s directly; use an application boundary", filepath.Base(path), match[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeDoesNotAddBackgroundContextExceptions(t *testing.T) {
	root := runtimeRoot(t)
	allowed := map[string]int{
		"../pkg/runtimehost/host.go": 1,
	}
	actual := map[string]int{}
	for _, productionRoot := range []string{root, filepath.Join(root, "..", "pkg", "runtimehost")} {
		walkProductionGo(t, productionRoot, func(path string, file *ast.File) {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatal(err)
			}
			rel = filepath.ToSlash(rel)
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				identifier, isIdentifier := selector.X.(*ast.Ident)
				if isIdentifier && identifier.Name == "context" && (selector.Sel.Name == "Background" || selector.Sel.Name == "TODO") {
					actual[rel]++
				}
				return true
			})
		})
	}
	for path, count := range actual {
		if allowed[path] != count {
			t.Fatalf("background context exception changed in %s: allowed=%d actual=%d; pass an entrypoint context instead", path, allowed[path], count)
		}
	}
	for path, count := range allowed {
		if actual[path] != count {
			t.Fatalf("background context allowlist is stale for %s: allowed=%d actual=%d", path, count, actual[path])
		}
	}
}

func TestRuntimeArchitectureInventoryIsCurrent(t *testing.T) {
	root := runtimeRoot(t)
	serviceRoot := filepath.Join(root, "bootstrap", "composition")
	content, err := os.ReadFile(filepath.Join("testdata", "runtime-service-boundary-inventory.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, expected := range []string{
		fmt.Sprintf("## RuntimeServices methods (%d)", countReceiverMethods(t, serviceRoot, "RuntimeServices")),
		"## Cross-aggregate write flows",
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("architecture inventory is stale or incomplete: missing %q; run go run scripts/contracts/runtime_architecture_inventory.go", expected)
		}
	}
}

func TestRuntimeImportBoundary(t *testing.T) {
	root := runtimeRoot(t)
	module := "github.com/domainry/domainry-runtime/"
	var violations []string
	walkAllGo(t, root, func(path string, file *ast.File) {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		rel = filepath.ToSlash(rel)
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("unquote import in %s: %v", rel, err)
			}
			runtimePrefix := module + "runtime"
			testSupportPrefix := module + "testsupport"
			if strings.HasSuffix(rel, "_test.go") && (importPath == testSupportPrefix || strings.HasPrefix(importPath, testSupportPrefix+"/")) {
				continue
			}
			if strings.HasPrefix(importPath, module) &&
				importPath != runtimePrefix && !strings.HasPrefix(importPath, runtimePrefix+"/") &&
				!strings.HasPrefix(importPath, module+"pkg/") {
				violations = append(violations, rel+" -> "+importPath)
			}
		}
	})
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("runtime import boundary violations:\n%s", strings.Join(violations, "\n"))
	}
}

func runtimeRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	return filepath.Dir(filepath.Dir(currentFile))
}

func countReceiverMethods(t *testing.T, root, receiverName string) int {
	t.Helper()
	count := 0
	walkProductionGo(t, root, func(_ string, file *ast.File) {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || len(function.Recv.List) == 0 {
				continue
			}
			receiver := function.Recv.List[0].Type
			if pointer, ok := receiver.(*ast.StarExpr); ok {
				receiver = pointer.X
			}
			if identifier, ok := receiver.(*ast.Ident); ok && identifier.Name == receiverName {
				count++
			}
		}
	})
	return count
}

func countStorageMethodsWithoutContext(t *testing.T, root string) int {
	t.Helper()
	count := 0
	walkProductionGo(t, root, func(path string, file *ast.File) {
		// Explicit cross-package adapter seams are not legacy domain Store API.
		// Their surface exists only so owner packages can leave the database root.
		rel := filepath.ToSlash(path)
		if filepath.Base(path) == "adapter_seams.go" || strings.HasSuffix(rel, "/database/identity/exports.go") {
			return
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv == nil || !function.Name.IsExported() || len(function.Recv.List) == 0 {
				continue
			}
			receiverExpression := function.Recv.List[0].Type
			if pointer, ok := receiverExpression.(*ast.StarExpr); ok {
				receiverExpression = pointer.X
			}
			receiver, ok := receiverExpression.(*ast.Ident)
			if !ok || !strings.HasSuffix(receiver.Name, "Store") || receiver.Name == "RuntimeStore" {
				continue
			}
			if function.Type.Params != nil && len(function.Type.Params.List) > 0 && isContextType(function.Type.Params.List[0].Type) {
				continue
			}
			count++
		}
	})
	return count
}

func isContextType(expression ast.Expr) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Context" {
		return false
	}
	identifier, ok := selector.X.(*ast.Ident)
	return ok && identifier.Name == "context"
}

func countInterfaceMethods(t *testing.T, path, interfaceName string) int {
	t.Helper()
	file := parseFile(t, path)
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, spec := range general.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != interfaceName {
				continue
			}
			iface, ok := typeSpec.Type.(*ast.InterfaceType)
			if !ok {
				t.Fatalf("%s is not an interface", interfaceName)
			}
			count := 0
			for _, field := range iface.Methods.List {
				count += len(field.Names)
			}
			return count
		}
	}
	t.Fatalf("interface %s not found in %s", interfaceName, path)
	return 0
}

func countOptionalInterfaceMethods(t *testing.T, path, interfaceName string) int {
	t.Helper()
	file := parseFile(t, path)
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, spec := range general.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != interfaceName {
				continue
			}
			iface, ok := typeSpec.Type.(*ast.InterfaceType)
			if !ok {
				return 0
			}
			count := 0
			for _, field := range iface.Methods.List {
				count += len(field.Names)
			}
			return count
		}
	}
	return 0
}

func countRouteRegistrations(t *testing.T, path string) int {
	t.Helper()
	file := parseFile(t, path)
	count := 0
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "Routes" || function.Body == nil {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok && (selector.Sel.Name == "Handle" || selector.Sel.Name == "HandleFunc") {
				count++
			}
			return true
		})
		return count
	}
	t.Fatalf("Routes method not found in %s", path)
	return 0
}

func walkProductionGo(t *testing.T, root string, visit func(path string, file *ast.File)) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		visit(path, parseFile(t, path))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func countProductionGoFiles(t *testing.T, root string) int {
	t.Helper()
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return 0
	} else if err != nil {
		t.Fatal(err)
	}
	count := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && filepath.Ext(path) == ".go" && !strings.HasSuffix(path, "_test.go") {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func countTestGoFiles(t *testing.T, root string) int {
	t.Helper()
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return 0
	} else if err != nil {
		t.Fatal(err)
	}
	count := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasSuffix(path, "_test.go") {
			count++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return count
}

func sourceFingerprint(t *testing.T, root string) [32]byte {
	t.Helper()
	repositoryRoot := filepath.Clean(filepath.Join(runtimeRoot(t), ".."))
	paths := []string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && filepath.Ext(path) == ".go" && !strings.HasSuffix(path, "_test.go") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		rel, err := filepath.Rel(repositoryRoot, path)
		if err != nil {
			t.Fatal(err)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		hash.Write([]byte(filepath.ToSlash(rel)))
		hash.Write([]byte{0})
		hash.Write(content)
		hash.Write([]byte{0})
	}
	var result [32]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func walkAllGo(t *testing.T, root string, visit func(path string, file *ast.File)) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		visit(path, parseFile(t, path))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func parseFile(t *testing.T, path string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return file
}

func assertAtMost(t *testing.T, label string, actual, baseline int) {
	t.Helper()
	if actual > baseline {
		t.Fatalf("%s increased: baseline=%d actual=%d; extract or reuse a bounded component instead", label, baseline, actual)
	}
}
