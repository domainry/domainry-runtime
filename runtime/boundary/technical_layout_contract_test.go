package boundary_test

import (
	"bufio"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

var businessOwnerRootProductionBaselines = map[string]int{
	"action": 0, "agent": 0, "automation": 0,
	"businessevent": 0, "businessseed": 0, "capability": 0, "changeplan": 0, "definition": 0,
	"deployment": 0, "integration": 0,
	"party":      0,
	"expression": 0,
	"lifecycle":  0, "localization": 0, "manifest": 0, "appschema": 0,
	"notification": 0, "operations": 0, "pipeline": 0, "principal": 0, "profilebinding": 0, "record": 0, "report": 0,
	"scheduler": 0, "surfacecontext": 0, "transaction": 0, "workflow": 0, "workspaceprovision": 0,
	"surface": 0,
}

var businessTechnicalDirectories = technicalLayoutStringSet(
	"model", "validation", "service", "query", "snapshot", "repository", "runtime", "projection",
	"policy", "contract", "testdata",
)

var applicationTopLevelDirectories = technicalLayoutStringSet(
	"action", "agent", "auditbinding", "automation", "businessevent", "businesssystem", "capability", "changeplan", "contractcheck",
	"deployment", "integration", "lifecycle", "appschema", "notificationfacade", "operations", "pipeline", "principal", "record", "report", "scheduler",
	"party",
	"recordmutation", "recordtimer", "seed", "surfacecontext", "upload", "workflow", "workspaceprovision",
	"publicationhandoff",
)

var applicationProductionBaselines = map[string]int{
	".": 0, "action": 43, "agent": 6, "auditbinding": 1, "automation": 8, "businesssystem": 4, "capability": 17,
	"businessevent": 1, "changeplan": 14, "deployment": 5, "integration": 70, "lifecycle": 5, "appschema": 14, "notificationfacade": 4, "operations": 9,
	"pipeline": 4, "record": 20, "recordmutation": 4, "recordtimer": 1, "report": 5, "scheduler": 12, "surfacecontext": 2, "workflow": 26,
	"party": 3, "principal": 1, "workspaceprovision": 1,
	"publicationhandoff": 1,
	"upload":             4,
	"seed":               0, "seed/automation": 2, "seed/business": 2,
	"seed/globalcapability": 2,
}

var bootstrapProductionFiles = technicalLayoutStringSet(
	"bootstrap.go",
)

var bootstrapTopLevelDirectories = technicalLayoutStringSet("composition", "integrationtest", "runtime", "testkit", "transport")

var bootstrapRuntimeProductionFiles = technicalLayoutStringSet(
	"config.go", "construction.go", "http_server.go", "identity_catalog.go",
	"manifest_loader.go", "manifest_preparation.go",
	"manifest_validation_catalog.go", "appschema_restoration.go", "repository_bindings.go",
	"notification_event_types.go", "notification_startup_bindings.go", "runtime.go", "seed_synchronization.go", "service_assembly.go", "startup.go",
	"identity_project_roles.go", "notification_sdk_module_host.go", "party_sdk_module_host.go", "notification_system_retention.go", "notification_system_subjects.go",
	"monitoring_module_host.go",
	"integration_module_host.go", "metadata_module_host.go", "rate_limiter.go", "report_module_host.go",
	"data_exchange_module_host.go",
	"agent_sdk_binding.go",
	"module_http_surfaces.go", "module_inventory.go",
	"startup_errors.go", "store_preparation.go", "worker_dependencies.go", "worker_lifecycle.go", "worker_lifecycle_cleanup.go", "worker_registry.go", "operations_control_worker.go",
)

var bootstrapTransportProductionFiles = technicalLayoutStringSet(
	"agent_application_assembly.go", "agent_task_tool_adapters.go", "entrypoint_mux.go", "http_identity_handler_wiring.go",
	"http_integration_agent_handler_wiring.go", "http_appschema_business_handler_wiring.go",
	"http_record_process_handler_wiring.go", "http_server_assembly.go", "http_server_entrypoint.go",
	"http_technical_metrics.go",
	"operations_break_glass_alert.go", "operations_dead_letter_adapters.go",
)

var httpOwnerDirectories = technicalLayoutStringSet(
	"agentdialog", "automation", "businessevents", "businessreferences", "businesssystem",
	"capabilities", "discovery", "lifecycle",
	"party",
	"integrations", "appschema", "notifications", "openapi", "records", "reports",
	"scheduler", "surfacecontext", "uploads", "workflows", "workspaceprovision",
	"operations",
)

// Technical middleware packages do not own endpoint routes and therefore do
// not participate in the Handler/Routes owner naming contract below.
var httpTechnicalDirectories = technicalLayoutStringSet("accesscontrol")

var httpOwnerExportNames = map[string]string{
	"agentdialog":        "AgentDialog",
	"automation":         "Automation",
	"businessevents":     "BusinessEvents",
	"businessreferences": "BusinessReferences",
	"businesssystem":     "BusinessSystem",
	"capabilities":       "Capabilities",
	"discovery":          "Discovery",
	"party":              "Party",
	"integrations":       "Integrations",
	"lifecycle":          "Lifecycle",
	"appschema":          "ApplicationSchema",
	"notifications":      "Notifications",
	"openapi":            "OpenAPI",
	"operations":         "Operations",
	"records":            "Records",
	"reports":            "Reports",
	"scheduler":          "Scheduler",
	"surfacecontext":     "SurfaceContext",
	"uploads":            "Uploads",
	"workflows":          "Workflows",
	"workspaceprovision": "WorkspaceProvision",
}

var httpRootProductionFiles = technicalLayoutStringSet(
	// Current reviewed seams.
	"http_business_events.go", "http_capacity.go", "http_metrics_collector.go", "http_operational_controls.go", "http_router.go",
	"http_router_dependencies.go", "http_router_handler_wiring.go",
	"http_router_middleware.go", "http_router_response.go", "high_risk_operation_policy.go",
	// Target technical names. Adding any other production file to the HTTP root
	// requires an explicit architecture review.
	"routes.go", "middleware.go", "request.go", "response.go", "metrics.go",
)

var reviewedHTTPInfrastructureImports = technicalLayoutStringSet()

var reviewedDomainFileLineBaselines = map[string]int{
	"domain/surface/model/surface_route_policy.go": 3831,
}

var reviewedApplicationFileLineBaselines = map[string]int{
	"application/agent/runtime/agent_authorization_application_service.go": 406,
	"application/integration/integration_google_oauth.go":                  406,
	"application/scheduler/scheduler_operations.go":                        425,
	"application/scheduler/scheduler_surface_use_cases.go":                 405,
	"application/workflow/workflow_process_runtime_application_service.go": 417,
	"application/workflow/workflow_record_worker_application_service.go":   401,
	"application/integration/integration_application_service.go":           436,
	"bootstrap/runtime/startup.go":                                         569,
}

var reviewedHTTPFileLineBaselines = map[string]int{
	"transport/http/http_router_middleware.go": 426,
}

var reviewedBootstrapFileLineBaselines = map[string]int{
	"bootstrap/runtime/startup.go": 570,
}

var reviewedVagueProductionFiles = technicalLayoutStringSet()

var runtimeTopLevelDirectories = technicalLayoutStringSet("application", "domain", "cmd", "bootstrap", "boundary", "infrastructure", "platform", "transport")
var infrastructureTopLevelDirectories = technicalLayoutStringSet("agentrunner", "broadcast", "connectors", "lifecycleartifact", "persistence", "ratelimitredis")
var platformTopLevelDirectories = technicalLayoutStringSet("apperror", "capacity", "collection", "config", "filelock", "health", "idempotency", "localization", "logging", "mutation", "notificationbinding", "productbrand", "ratelimit", "requestcontext", "resilience", "safehttp", "secrets", "telemetry", "webhooksignature", "worker")
var transportTopLevelDirectories = technicalLayoutStringSet("http", "provision")

var reviewedPackageNameExceptions = map[string]string{
	"domain/integration/contract":                              "integrationcontract",
	"infrastructure/connectors/app_review/google_play_console": "googleplay",
}

var lowerSnakeName = regexp.MustCompile(`^[a-z][a-z0-9]*(?:_[a-z0-9]+)*$`)
var lowerKebabName = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
var goFileName = regexp.MustCompile(`^[a-z][a-z0-9]*(?:_[a-z0-9]+)*(?:_test)?\.go$`)

var genericConnectorAdapterType = regexp.MustCompile(`(?m)^type Adapter\b`)

var genericConnectorAdapterConstructor = regexp.MustCompile(`(?m)^func New\([^)]*\) \*?[A-Za-z0-9]+Adapter\b`)
var goPackageName = regexp.MustCompile(`^[a-z][a-z0-9_]*(?:_test)?$`)

func TestRuntimeTechnicalLayoutContract(t *testing.T) {
	root := runtimeRoot(t)
	t.Run("catch-all directories are forbidden", func(t *testing.T) {
		for _, relative := range []string{"domain", "application", "transport/http"} {
			assertNoCatchAllDirectories(t, filepath.Join(root, filepath.FromSlash(relative)))
		}
	})
	t.Run("domain owners use reviewed technical children", func(t *testing.T) {
		assertBusinessTechnicalLayout(t, root)
	})
	t.Run("application contains use case orchestration only", func(t *testing.T) {
		assertApplicationTechnicalLayout(t, root)
	})
	t.Run("bootstrap files name their composition responsibility", func(t *testing.T) {
		assertBootstrapTechnicalLayout(t, root)
	})
	t.Run("http remains a transport boundary", func(t *testing.T) {
		assertHTTPTechnicalLayout(t, root)
	})
}

func TestRuntimeDomainDependencyMatrixIsClosed(t *testing.T) {
	domainRoot := filepath.Join(runtimeRoot(t), "domain")
	modulePrefix := "github.com/domainry/domainry-runtime/runtime/"
	higherLayers := []string{"application/", "bootstrap/", "transport/", "infrastructure/"}
	allowedSameOwner := map[string]map[string]bool{
		"model":      technicalLayoutStringSet("model", "contract"),
		"contract":   technicalLayoutStringSet("model", "contract"),
		"policy":     technicalLayoutStringSet("model", "contract", "policy"),
		"validation": technicalLayoutStringSet("model", "contract", "policy", "validation"),
		"repository": technicalLayoutStringSet("model", "contract", "repository"),
		"projection": technicalLayoutStringSet("model", "contract", "policy", "projection"),
		"runtime":    technicalLayoutStringSet("model", "contract", "policy", "runtime"),
		"service":    technicalLayoutStringSet("model", "contract", "policy", "validation", "repository", "projection", "runtime", "service"),
		"query":      technicalLayoutStringSet("model", "contract", "policy", "projection", "query", "snapshot"),
		"snapshot":   technicalLayoutStringSet("model", "contract", "snapshot"),
	}

	err := filepath.WalkDir(domainRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		relative, relErr := filepath.Rel(domainRoot, path)
		if relErr != nil {
			return relErr
		}
		parts := strings.Split(filepath.ToSlash(relative), "/")
		sourceOwner, sourceTechnical := parts[0], ""
		if len(parts) > 2 {
			sourceTechnical = parts[1]
		}
		file := parseFile(t, path)
		ast.Inspect(file, func(node ast.Node) bool {
			identifier, ok := node.(*ast.Ident)
			if ok && strings.Contains(identifier.Name, "Application") &&
				!strings.Contains(identifier.Name, "ApplicationSchema") &&
				!strings.Contains(identifier.Name, "ApplicationDefinition") {
				t.Errorf("Domain identifiers must not retain Application-layer ownership: %s declares or uses %s", path, identifier.Name)
			}
			return true
		})
		for _, spec := range file.Imports {
			importPath, unquoteErr := strconv.Unquote(spec.Path.Value)
			if unquoteErr != nil || !strings.HasPrefix(importPath, modulePrefix) {
				continue
			}
			runtimeImport := strings.TrimPrefix(importPath, modulePrefix)
			for _, layer := range higherLayers {
				if strings.HasPrefix(runtimeImport, layer) {
					t.Errorf("Domain code and tests must not import higher-layer implementation: %s imports %s", path, importPath)
				}
			}
			if !strings.HasPrefix(runtimeImport, "domain/") {
				continue
			}
			targetParts := strings.Split(strings.TrimPrefix(runtimeImport, "domain/"), "/")
			targetOwner, targetTechnical := targetParts[0], ""
			if len(targetParts) > 1 {
				targetTechnical = targetParts[1]
			}
			if sourceOwner != targetOwner {
				if sourceOwner == "manifest" && sourceTechnical == "validation" && targetTechnical == "validation" && (targetOwner == "notification" || targetOwner == "workflow") {
					continue
				}
				if targetTechnical != "model" && targetTechnical != "contract" {
					t.Errorf("cross-owner Domain imports must target model/contract leaves: %s imports %s", path, importPath)
				}
				continue
			}
			if sourceTechnical == "" || targetTechnical == "" {
				continue
			}
			allowed, reviewed := allowedSameOwner[sourceTechnical]
			if reviewed && !allowed[targetTechnical] {
				t.Errorf("owner technical-package dependency violates R3.3: %s imports %s", path, importPath)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeDomainContainsNoApplicationOrProcessLifecycleImplementations(t *testing.T) {
	domainRoot := filepath.Join(runtimeRoot(t), "domain")
	forbiddenImports := []string{
		"github.com/domainry/domainry-foundation/worker",
		"github.com/domainry/domainry-foundation/logging",
	}
	forbiddenMutationPorts := technicalLayoutStringSet("Create", "Update", "Delete", "Restore", "Publish", "InvokeAction", "RunWorkflow", "EmitEvent")
	forbiddenApplicationContracts := technicalLayoutStringSet(
		"WorkflowDependencies", "WorkflowSchedulerWorkerConfig", "WorkflowScheduler",
		"WorkflowBusinessActionInvocation", "WorkflowBusinessActionInvocationResult",
	)

	err := filepath.WalkDir(domainRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, relErr := filepath.Rel(domainRoot, path)
		if relErr != nil {
			return relErr
		}
		parts := strings.Split(filepath.ToSlash(relative), "/")
		technical := ""
		if len(parts) > 2 {
			technical = parts[1]
		}
		file := parseFile(t, path)
		for _, spec := range file.Imports {
			importPath, unquoteErr := strconv.Unquote(spec.Path.Value)
			if unquoteErr != nil {
				continue
			}
			for _, forbidden := range forbiddenImports {
				if importPath == forbidden {
					t.Errorf("Domain must not own process lifecycle or process logging: %s imports %s", path, importPath)
				}
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.TypeSpec:
				if forbiddenApplicationContracts[value.Name.Name] {
					t.Errorf("Application orchestration contract must not be declared in Domain: %s declares %s", path, value.Name.Name)
				}
				if structType, ok := value.Type.(*ast.StructType); ok && (strings.HasSuffix(value.Name.Name, "Store") || strings.HasSuffix(value.Name.Name, "Repository")) {
					_ = structType
					t.Errorf("Domain may define Store/Repository ports but not concrete implementations: %s declares struct %s", path, value.Name.Name)
				}
			case *ast.FuncDecl:
				if strings.HasPrefix(value.Name.Name, "Start") && strings.HasSuffix(value.Name.Name, "Worker") {
					t.Errorf("worker start/stop lifecycle belongs to Bootstrap, not Domain: %s declares %s", path, value.Name.Name)
				}
			case *ast.Field:
				if technical != "projection" && technical != "validation" && technical != "runtime" {
					break
				}
				if _, ok := value.Type.(*ast.FuncType); !ok {
					break
				}
				for _, name := range value.Names {
					if forbiddenMutationPorts[name.Name] {
						t.Errorf("Domain %s must consume snapshots or pure ports, not hide Application mutation orchestration: %s declares %s", technical, path, name.Name)
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestBackendDevelopmentGuideIsArchitectureGovernanceEntry(t *testing.T) {
	repositoryRoot := filepath.Clean(filepath.Join(runtimeRoot(t), ".."))
	brandManifestPath := filepath.Join(repositoryRoot, "config", "product-brand.json")
	brandManifestBytes, err := os.ReadFile(brandManifestPath)
	if err != nil {
		t.Fatalf("read product brand manifest: %v", err)
	}
	var brandManifest struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(brandManifestBytes, &brandManifest); err != nil || strings.TrimSpace(brandManifest.Name) == "" {
		t.Fatalf("decode product brand manifest %s: name=%q error=%v", brandManifestPath, brandManifest.Name, err)
	}
	guidePath := filepath.Join(repositoryRoot, "docs", "architecture", "backend-development-guide.md")
	if content, err := os.ReadFile(guidePath); err != nil {
		t.Fatalf("backend architecture development guide must be retained: %v", err)
	} else if !strings.Contains(string(content), "# "+brandManifest.Name+" 后端开发指导") {
		t.Fatalf("backend architecture development guide has unexpected identity: %s", guidePath)
	}
	for _, relative := range []string{"README.md"} {
		path := filepath.Join(repositoryRoot, filepath.FromSlash(relative))
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read backend governance entry %s: %v", relative, err)
		}
		if !strings.Contains(string(content), "docs/architecture/backend-development-guide.md") && !strings.Contains(string(content), "architecture/backend-development-guide.md") {
			t.Errorf("backend governance entry must reference docs/architecture/backend-development-guide.md: %s", relative)
		}
	}
	for _, legacyPath := range []string{
		filepath.Join(repositoryRoot, "RUNTIME_DEVELOPMENT.md"),
		filepath.Join(repositoryRoot, "docs", "runtime-development-guide.md"),
		filepath.Join(repositoryRoot, "docs", "architecture", "runtime-development-guide.md"),
		filepath.Join(repositoryRoot, "docs", "backend-package-layout-conventions.md"),
	} {
		if _, err := os.Stat(legacyPath); err == nil {
			t.Errorf("backend development guide must not have a shadow copy: %s", legacyPath)
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
}

func TestRuntimeLayerAndNamingContract(t *testing.T) {
	root := runtimeRoot(t)
	assertReviewedLayerDirectories(t, root)
	assertRuntimePathNames(t, root)
	assertRuntimePackageNames(t, root)
	assertRuntimeExportedSymbolNames(t, root)
}

func TestRuntimeDomainAndApplicationDoNotReExportTypeAliases(t *testing.T) {
	root := runtimeRoot(t)
	for _, layer := range []string{"domain", "application"} {
		walkProductionGo(t, filepath.Join(root, layer), func(path string, file *ast.File) {
			if filepath.ToSlash(path) == filepath.ToSlash(filepath.Join(root, "application", "auditbinding", "audit_application_service.go")) {
				return
			}
			for _, declaration := range file.Decls {
				general, ok := declaration.(*ast.GenDecl)
				if !ok || general.Tok != token.TYPE {
					continue
				}
				for _, specification := range general.Specs {
					typeSpec, ok := specification.(*ast.TypeSpec)
					if ok && typeSpec.Assign.IsValid() {
						t.Errorf("%s must depend on the owning package directly instead of re-exporting type alias %s", path, typeSpec.Name.Name)
					}
				}
			}
		})
	}
}

func TestRuntimeServiceAndStoreNamingContract(t *testing.T) {
	root := runtimeRoot(t)
	t.Run("domain services use domain names and matching files", func(t *testing.T) {
		assertLayerServiceNames(t, filepath.Join(root, "domain"), "DomainService")
	})
	t.Run("application services use application names and matching files", func(t *testing.T) {
		assertLayerServiceNames(t, filepath.Join(root, "application"), "ApplicationService")
	})
	t.Run("stores use owned names and matching files", func(t *testing.T) {
		assertStoreNames(t, filepath.Join(root, "infrastructure", "persistence"))
		assertStoreNames(t, filepath.Join(root, "platform"))
	})
	t.Run("bootstrap owns no business services or stores", func(t *testing.T) {
		assertBootstrapOwnsNoBusinessServicesOrStores(t, filepath.Join(root, "bootstrap"))
	})
}

func assertBootstrapOwnsNoBusinessServicesOrStores(t *testing.T, root string) {
	t.Helper()
	walkProductionGo(t, root, func(path string, file *ast.File) {
		for _, declaration := range file.Decls {
			switch declaration := declaration.(type) {
			case *ast.GenDecl:
				for _, specification := range declaration.Specs {
					typeSpec, ok := specification.(*ast.TypeSpec)
					if !ok {
						continue
					}
					name := typeSpec.Name.Name
					if strings.HasSuffix(name, "DomainService") || strings.HasSuffix(name, "ApplicationService") || strings.HasSuffix(name, "Store") {
						t.Errorf("bootstrap must assemble, not own, business services or stores: %s declares %s", path, name)
					}
				}
			case *ast.FuncDecl:
				if declaration.Recv != nil {
					continue
				}
				name := declaration.Name.Name
				if strings.HasPrefix(name, "New") && (strings.HasSuffix(name, "DomainService") || strings.HasSuffix(name, "ApplicationService") || strings.HasSuffix(name, "Store")) {
					t.Errorf("bootstrap must not construct an owned business type: %s declares %s", path, name)
				}
			}
		}
	})
}

func assertLayerServiceNames(t *testing.T, root string, requiredSuffix string) {
	t.Helper()
	walkProductionGo(t, root, func(path string, file *ast.File) {
		serviceCount := 0
		for _, declaration := range file.Decls {
			switch declaration := declaration.(type) {
			case *ast.GenDecl:
				for _, specification := range declaration.Specs {
					typeSpec, ok := specification.(*ast.TypeSpec)
					if !ok || !strings.HasSuffix(typeSpec.Name.Name, "Service") {
						continue
					}
					serviceCount++
					assertOwnedTypeFileName(t, path, typeSpec.Name.Name, requiredSuffix)
				}
			case *ast.FuncDecl:
				if declaration.Recv != nil || !strings.HasPrefix(declaration.Name.Name, "New") || !strings.HasSuffix(declaration.Name.Name, "Service") {
					continue
				}
				if !strings.HasSuffix(declaration.Name.Name, requiredSuffix) {
					t.Errorf("service constructor must end in NewXxx%s: %s declares %s", requiredSuffix, path, declaration.Name.Name)
				}
			}
		}
		if serviceCount > 1 {
			t.Errorf("service production file must own one service type: %s declares %d", path, serviceCount)
		}
	})
}

func assertStoreNames(t *testing.T, root string) {
	t.Helper()
	walkProductionGo(t, root, func(path string, file *ast.File) {
		storeCount := 0
		for _, declaration := range file.Decls {
			switch declaration := declaration.(type) {
			case *ast.GenDecl:
				for _, specification := range declaration.Specs {
					typeSpec, ok := specification.(*ast.TypeSpec)
					if !ok || !ast.IsExported(typeSpec.Name.Name) || !strings.HasSuffix(typeSpec.Name.Name, "Store") {
						continue
					}
					// Repository ports and small unexported implementation details do not
					// own persistence adapter files. This contract is intentionally about
					// exported, concrete Store implementations.
					if _, ok := typeSpec.Type.(*ast.StructType); !ok {
						continue
					}
					storeCount++
					name := typeSpec.Name.Name
					if name == "Store" || name == "ContextStore" || strings.HasPrefix(name, "Context") {
						t.Errorf("store type must name its owner and must not encode context plumbing: %s declares %s", path, name)
					}
					assertOwnedTypeFileName(t, path, name, "Store")
				}
			case *ast.FuncDecl:
				if declaration.Recv != nil || !strings.HasPrefix(declaration.Name.Name, "New") || !strings.HasSuffix(declaration.Name.Name, "Store") {
					continue
				}
				name := strings.TrimPrefix(declaration.Name.Name, "New")
				if name == "Store" || name == "ContextStore" || strings.HasPrefix(name, "Context") {
					t.Errorf("store constructor must name its owner and must not encode context plumbing: %s declares %s", path, declaration.Name.Name)
				}
			}
		}
		if storeCount > 1 {
			t.Errorf("store production file must own one store type: %s declares %d", path, storeCount)
		}
	})
}

func assertOwnedTypeFileName(t *testing.T, path string, typeName string, requiredSuffix string) {
	t.Helper()
	if !strings.HasSuffix(typeName, requiredSuffix) {
		t.Errorf("type must end in %s: %s declares %s", requiredSuffix, path, typeName)
	}
	expected := mixedCapsFileName(typeName)
	actual := filepath.Base(path)
	if actual != expected && actual != compactOwnerTypeFileName(path, expected) {
		t.Errorf("type and file name must match: %s declares %s, want %s", path, typeName, expected)
	}
}

func compactOwnerTypeFileName(path, expected string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	owner := ""
	for index := range parts {
		if parts[index] == "domain" && index+1 < len(parts) {
			owner = parts[index+1]
			break
		}
	}
	if owner == "" {
		return ""
	}
	stem := strings.TrimSuffix(expected, ".go")
	segments := strings.Split(stem, "_")
	for count := 1; count <= len(segments); count++ {
		prefix := strings.Join(segments[:count], "_")
		if strings.ReplaceAll(prefix, "_", "") != owner {
			continue
		}
		remainder := strings.Join(segments[count:], "_")
		if remainder == "" {
			return owner + ".go"
		}
		return owner + "_" + remainder + ".go"
	}
	return ""
}

func mixedCapsFileName(name string) string {
	runes := []rune(name)
	var builder strings.Builder
	for index, current := range runes {
		if unicode.IsUpper(current) && index > 0 {
			previous := runes[index-1]
			nextIsLower := index+1 < len(runes) && unicode.IsLower(runes[index+1])
			if unicode.IsLower(previous) || unicode.IsDigit(previous) || unicode.IsUpper(previous) && nextIsLower {
				builder.WriteByte('_')
			}
		}
		builder.WriteRune(unicode.ToLower(current))
	}
	return builder.String() + ".go"
}

func TestMixedCapsFileName(t *testing.T) {
	t.Parallel()
	for name, expected := range map[string]string{
		"AuthDomainService": "auth_domain_service.go",
		"HTTPAuthStore":     "http_auth_store.go",
		"SQLIdentityStore":  "sql_identity_store.go",
	} {
		if actual := mixedCapsFileName(name); actual != expected {
			t.Errorf("mixedCapsFileName(%q) = %q, want %q", name, actual, expected)
		}
	}
}

func TestRuntimeTechnicalLayoutFileBudgets(t *testing.T) {
	root := runtimeRoot(t)
	assertProductionFileLineBudget(t, filepath.Join(root, "domain"), 500, reviewedDomainFileLineBaselines)
	assertProductionFileLineBudget(t, filepath.Join(root, "application"), 400, reviewedApplicationFileLineBaselines)
	assertProductionFileLineBudget(t, filepath.Join(root, "bootstrap"), 500, reviewedBootstrapFileLineBaselines)
	assertProductionFileLineBudget(t, filepath.Join(root, "transport", "http"), 400, reviewedHTTPFileLineBaselines)
}

func assertBootstrapTechnicalLayout(t *testing.T, runtimeRoot string) {
	t.Helper()
	bootstrapRoot := filepath.Join(runtimeRoot, "bootstrap")
	entries, err := os.ReadDir(bootstrapRoot)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			if !bootstrapTopLevelDirectories[name] {
				t.Errorf("bootstrap subdirectory must be composition, integrationtest, or testkit: %s", name)
			}
			continue
		}
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		seen[name] = true
		if !bootstrapProductionFiles[name] {
			t.Errorf("bootstrap production file must name its exact composition responsibility: %s", name)
		}
	}
	for name := range bootstrapProductionFiles {
		if !seen[name] {
			t.Errorf("remove stale bootstrap production-file contract entry: %s", name)
		}
	}
	assertBootstrapSubpackageFiles(t, filepath.Join(bootstrapRoot, "runtime"), "Runtime process", bootstrapRuntimeProductionFiles)
	assertBootstrapSubpackageFiles(t, filepath.Join(bootstrapRoot, "transport"), "Transport wiring", bootstrapTransportProductionFiles)
}

func assertBootstrapSubpackageFiles(t *testing.T, root, responsibility string, reviewed map[string]bool) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			t.Errorf("%s Bootstrap package must remain flat: %s", responsibility, filepath.Join(root, name))
			continue
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		seen[name] = true
		if !reviewed[name] {
			t.Errorf("unreviewed %s Bootstrap production file: %s", responsibility, filepath.Join(root, name))
		}
	}
	for name := range reviewed {
		if !seen[name] {
			t.Errorf("remove stale %s Bootstrap production-file entry: %s", responsibility, name)
		}
	}
}

func TestHTTPOwnerUsesPrefixedHandlerAndRoutesFiles(t *testing.T) {
	root := filepath.Join(runtimeRoot(t), "transport", "http")
	for owner := range httpOwnerDirectories {
		ownerRoot := filepath.Join(root, owner)
		for _, name := range []string{owner + "_handler.go", owner + "_routes.go"} {
			if _, err := os.Stat(filepath.Join(ownerRoot, name)); err != nil {
				t.Errorf("HTTP owner %s must declare its primary file as %s: %v", owner, name, err)
			}
		}
		for _, legacy := range []string{"handler.go", "routes.go", "handler_test.go", "routes_test.go"} {
			if _, err := os.Stat(filepath.Join(ownerRoot, legacy)); err == nil {
				t.Errorf("HTTP owner %s must use owner-prefixed file names; rename %s", owner, legacy)
			}
		}
	}
}

func TestHTTPOwnerUsesBusinessSpecificHandlerSymbols(t *testing.T) {
	root := filepath.Join(runtimeRoot(t), "transport", "http")
	for owner := range httpOwnerDirectories {
		exportName, ok := httpOwnerExportNames[owner]
		if !ok {
			t.Errorf("HTTP owner %s must declare its exported business name", owner)
			continue
		}

		expectedTypes := map[string]bool{
			exportName + "Handler":      false,
			exportName + "Dependencies": false,
		}
		expectedConstructor := "New" + exportName + "Handler"
		constructorFound := false
		ownerRoot := filepath.Join(root, owner)
		walkProductionGo(t, ownerRoot, func(path string, file *ast.File) {
			for _, declaration := range file.Decls {
				switch typed := declaration.(type) {
				case *ast.GenDecl:
					if typed.Tok != token.TYPE {
						continue
					}
					for _, specification := range typed.Specs {
						typeSpec, typeOK := specification.(*ast.TypeSpec)
						if !typeOK {
							continue
						}
						if typeSpec.Name.Name == "Handler" || typeSpec.Name.Name == "Dependencies" {
							t.Errorf("HTTP owner %s must use business-specific symbols; found %s in %s", owner, typeSpec.Name.Name, path)
						}
						if _, expected := expectedTypes[typeSpec.Name.Name]; expected {
							expectedTypes[typeSpec.Name.Name] = true
						}
					}
				case *ast.FuncDecl:
					if typed.Name.Name == "NewHandler" {
						t.Errorf("HTTP owner %s must use a business-specific constructor in %s", owner, path)
					}
					if typed.Name.Name == expectedConstructor {
						constructorFound = true
					}
				}
			}
		})
		for expectedType, found := range expectedTypes {
			if !found {
				t.Errorf("HTTP owner %s must declare %s", owner, expectedType)
			}
		}
		if !constructorFound {
			t.Errorf("HTTP owner %s must declare %s", owner, expectedConstructor)
		}
	}
	for owner := range httpOwnerExportNames {
		if !httpOwnerDirectories[owner] {
			t.Errorf("remove stale HTTP exported business name mapping: %s", owner)
		}
	}
}

func TestHTTPRoutesFilesOnlyRegisterRoutes(t *testing.T) {
	root := filepath.Join(runtimeRoot(t), "transport", "http")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_routes.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Errorf("parse HTTP routes file %s: %v", path, err)
			return nil
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if function.Name.Name != "RegisterRoutes" {
				t.Errorf("HTTP routes file may only declare RegisterRoutes: %s declares %s", path, function.Name.Name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestHTTPInfrastructureImportsStayOnReviewedSeams(t *testing.T) {
	root := runtimeRoot(t)
	httpRoot := filepath.Join(root, "transport", "http")
	seen := map[string]bool{}
	walkProductionGo(t, httpRoot, func(path string, file *ast.File) {
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil || !strings.Contains(importPath, "/runtime/infrastructure/") {
				continue
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatal(err)
			}
			key := filepath.ToSlash(relative) + " -> " + importPath
			seen[key] = true
			if !reviewedHTTPInfrastructureImports[key] {
				t.Errorf("HTTP must receive infrastructure implementations from bootstrap composition; unreviewed edge: %s", key)
			}
		}
	})
	for edge := range reviewedHTTPInfrastructureImports {
		if !seen[edge] {
			t.Errorf("remove stale reviewed HTTP infrastructure edge: %s", edge)
		}
	}
}

func TestHTTPDependsDirectlyOnApplicationServices(t *testing.T) {
	httpRoot := filepath.Join(runtimeRoot(t), "transport", "http")
	walkProductionGo(t, httpRoot, func(path string, file *ast.File) {
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.TYPE {
				continue
			}
			for _, specification := range general.Specs {
				typeSpec, ok := specification.(*ast.TypeSpec)
				if !ok {
					continue
				}
				if _, isInterface := typeSpec.Type.(*ast.InterfaceType); isInterface && strings.HasSuffix(typeSpec.Name.Name, "ApplicationPort") {
					t.Errorf("HTTP must depend directly on concrete Application Services, not declare local port %s: %s", typeSpec.Name.Name, path)
				}
			}
		}
	})
}

func TestAuditOwnerBoundaryIsClosed(t *testing.T) {
	root := runtimeRoot(t)
	domainRoot := filepath.Join(root, "domain")
	auditRoot := filepath.Join(root, "application", "auditbinding")
	auditEventFactoryMethods := 0
	if err := filepath.WalkDir(auditRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	walkProductionGo(t, auditRoot, func(path string, file *ast.File) {
		contextAliases := technicalLayoutStringSet("context")
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil || importPath != "context" {
				continue
			}
			if spec.Name != nil && spec.Name.Name != "_" && spec.Name.Name != "." {
				contextAliases[spec.Name.Name] = true
			}
		}
		assertAuditEventFactorySignature := func(functionType *ast.FuncType) {
			if functionType.Params == nil || len(functionType.Params.List) == 0 ||
				!isRuntimeContextType(functionType.Params.List[0].Type, contextAliases) {
				t.Errorf("Audit NewAuditEvent must declare context.Context as its first explicit parameter: %s", path)
				return
			}
			auditEventFactoryMethods++
		}
		ast.Inspect(file, func(node ast.Node) bool {
			field, ok := node.(*ast.Field)
			if !ok || len(field.Names) != 1 || field.Names[0].Name != "NewAuditEvent" {
				return true
			}
			functionType, ok := field.Type.(*ast.FuncType)
			if ok {
				assertAuditEventFactorySignature(functionType)
			}
			return true
		})
		for _, declaration := range file.Decls {
			if function, ok := declaration.(*ast.FuncDecl); ok && function.Name.Name == "AuditBuildEvent" {
				assertAuditEventFactorySignature(function.Type)
			}
		}
	})
	if auditEventFactoryMethods == 0 {
		t.Error("Audit must expose NewAuditEvent through a context-first contract")
	}
	forbiddenImports := technicalLayoutStringSet(
		"github.com/domainry/domainry-runtime/runtime/domain/audit/repository",
		"github.com/domainry/domainry-runtime/runtime/domain/audit/service",
	)
	walkProductionGo(t, domainRoot, func(path string, file *ast.File) {
		if path == auditRoot || strings.HasPrefix(path, auditRoot+string(filepath.Separator)) {
			return
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err == nil && forbiddenImports[importPath] {
				t.Errorf("domain owners may consume Audit model/contract only: %s imports %s", path, importPath)
			}
		}
	})

}

func TestReportOwnerBoundaryIsClosed(t *testing.T) {
	root := runtimeRoot(t)
	reportRoot := filepath.Join(root, "domain", "report")
	if err := filepath.WalkDir(reportRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") && !strings.HasPrefix(entry.Name(), "report_") {
			t.Errorf("Report file must retain the report_ owner prefix for global searchability: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	forbiddenImports := technicalLayoutStringSet(
		"github.com/domainry/domainry-runtime/runtime/domain/record",
		"github.com/domainry/domainry-runtime/runtime/domain/record/repository",
		"github.com/domainry/domainry-runtime/runtime/domain/record/service",
		"github.com/domainry/domainry-runtime/runtime/domain/record/runtime",
	)
	walkProductionGo(t, reportRoot, func(path string, file *ast.File) {
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err == nil && forbiddenImports[importPath] {
				t.Errorf("Report Domain must consume Record model through a Report-owned contract, not Record behavior: %s imports %s", path, importPath)
			}
			if err == nil && strings.HasPrefix(importPath, "github.com/domainry/domainry-runtime/runtime/domain/audit/") {
				t.Errorf("Report export audit sequencing belongs to Application: %s imports %s", path, importPath)
			}
		}
		for _, declaration := range file.Decls {
			switch value := declaration.(type) {
			case *ast.GenDecl:
				for _, spec := range value.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if ok && typeSpec.Name.IsExported() && !strings.HasPrefix(typeSpec.Name.Name, "Report") {
						t.Errorf("exported Report type must retain the Report owner prefix: %s declares %s", path, typeSpec.Name.Name)
					}
				}
			case *ast.FuncDecl:
				if value.Name.Name == "SetRepository" && value.Recv != nil {
					t.Errorf("Report dependencies must be fixed at construction: %s", path)
				}
			}
		}
	})

	applicationRoot := filepath.Join(root, "application", "report")
	walkProductionGo(t, applicationRoot, func(path string, file *ast.File) {
		name := filepath.Base(path)
		if name != "report_query_application_service.go" && name != "report_snapshot_application_service.go" {
			return
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			if strings.Contains(importPath, "domainry-data-exchange") || strings.Contains(importPath, "/auditbinding") || strings.Contains(importPath, "/domain/record/repository") || strings.Contains(importPath, "/application/record") {
				t.Errorf("Report Query/Snapshot application capability must not absorb Export, Audit, or Record persistence dependencies: %s imports %s", path, importPath)
			}
		}
	})
}

func TestWorkflowOwnerNamingIsGloballySearchable(t *testing.T) {
	workflowRoot := filepath.Join(runtimeRoot(t), "domain", "workflow")
	forbiddenImports := technicalLayoutStringSet(
		"github.com/domainry/domainry-runtime/runtime/domain/record/repository",
		"github.com/domainry/domainry-runtime/runtime/domain/record/runtime",
		"github.com/domainry/domainry-runtime/runtime/domain/record/service",
		"github.com/domainry/domainry-runtime/runtime/domain/record/validation",
	)
	if err := filepath.WalkDir(workflowRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") && !strings.HasPrefix(entry.Name(), "workflow_") {
			t.Errorf("Workflow file must retain the workflow_ owner prefix for global searchability: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	walkProductionGo(t, workflowRoot, func(path string, file *ast.File) {
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err == nil && forbiddenImports[importPath] {
				t.Errorf("Workflow Domain must consume Record model or Workflow-owned contract only: %s imports %s", path, importPath)
			}
		}
		for _, declaration := range file.Decls {
			switch value := declaration.(type) {
			case *ast.FuncDecl:
				if value.Recv == nil && value.Name.IsExported() &&
					!strings.HasPrefix(value.Name.Name, "Workflow") && !strings.HasPrefix(value.Name.Name, "NewWorkflow") {
					t.Errorf("exported Workflow function must retain the Workflow owner prefix: %s declares %s", path, value.Name.Name)
				}
			case *ast.GenDecl:
				for _, spec := range value.Specs {
					switch typed := spec.(type) {
					case *ast.TypeSpec:
						if typed.Name.IsExported() && !strings.HasPrefix(typed.Name.Name, "Workflow") {
							t.Errorf("exported Workflow type must retain the Workflow owner prefix: %s declares %s", path, typed.Name.Name)
						}
					case *ast.ValueSpec:
						for _, name := range typed.Names {
							if name.IsExported() && !strings.HasPrefix(name.Name, "Workflow") {
								t.Errorf("exported Workflow value must retain the Workflow owner prefix: %s declares %s", path, name.Name)
							}
						}
					}
				}
			}
		}
	})
}

func TestActionExecutionRepositoryBoundaryIsClosed(t *testing.T) {
	root := runtimeRoot(t)
	actionRoot := filepath.Join(root, "domain", "action")
	for _, ownerRoot := range []string{
		actionRoot,
		filepath.Join(root, "application", "action"),
		filepath.Join(root, "infrastructure", "persistence", "database", "action"),
	} {
		if err := filepath.WalkDir(ownerRoot, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") && !strings.HasPrefix(entry.Name(), "action_") {
				t.Errorf("Action file must retain the action_ owner prefix for global searchability: %s", path)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	repositoryRoot := filepath.Join(actionRoot, "contract")
	walkProductionGo(t, repositoryRoot, func(path string, file *ast.File) {
		if !strings.HasPrefix(filepath.Base(path), "action_") {
			t.Errorf("Action contract file must retain the action_ owner prefix: %s", path)
		}
		for _, declaration := range file.Decls {
			switch value := declaration.(type) {
			case *ast.GenDecl:
				if value.Tok == token.IMPORT {
					continue
				}
				for _, spec := range value.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if !ok {
						t.Errorf("Action contract package may declare ports only: %s", path)
						continue
					}
					if _, ok := typeSpec.Type.(*ast.InterfaceType); !ok {
						t.Errorf("Action contract package may not own adapters: %s declares %s", path, typeSpec.Name.Name)
					}
				}
			default:
				t.Errorf("Action contract package may declare interfaces only: %s", path)
			}
		}
	})
	walkProductionGo(t, actionRoot, func(path string, file *ast.File) {
		contextAliases := technicalLayoutStringSet("context")
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil || importPath != "context" {
				continue
			}
			if spec.Name != nil && spec.Name.Name != "_" && spec.Name.Name != "." {
				contextAliases[spec.Name.Name] = true
			}
		}
		for _, declaration := range file.Decls {
			switch value := declaration.(type) {
			case *ast.FuncDecl:
				if value.Recv == nil && value.Name.IsExported() &&
					!strings.HasPrefix(value.Name.Name, "Action") &&
					!strings.HasPrefix(value.Name.Name, "NewAction") {
					t.Errorf("exported Action function must retain the Action owner prefix: %s declares %s", path, value.Name.Name)
				}
				if value.Name.Name == "NewActionInvocationID" &&
					(value.Type.Params == nil || len(value.Type.Params.List) == 0 ||
						!isRuntimeContextType(value.Type.Params.List[0].Type, contextAliases)) {
					t.Errorf("Action NewActionInvocationID must declare context.Context as its first explicit parameter: %s", path)
				}
			case *ast.GenDecl:
				for _, spec := range value.Specs {
					switch typed := spec.(type) {
					case *ast.TypeSpec:
						if typed.Name.IsExported() && !strings.HasPrefix(typed.Name.Name, "Action") {
							t.Errorf("exported Action type must retain the Action owner prefix: %s declares %s", path, typed.Name.Name)
						}
					case *ast.ValueSpec:
						for _, name := range typed.Names {
							if name.IsExported() && !strings.HasPrefix(name.Name, "Action") {
								t.Errorf("exported Action value must retain the Action owner prefix: %s declares %s", path, name.Name)
							}
						}
					}
				}
			}
			function, ok := declaration.(*ast.FuncDecl)
			if ok && function.Name.Name == "SetRepository" && function.Recv != nil {
				t.Errorf("Action repository dependencies must be fixed at construction: %s", path)
			}
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			const retiredIdentityPrefix = "github.com/domainry/domainry-runtime/runtime/domain/identity/"
			if strings.HasPrefix(importPath, retiredIdentityPrefix) {
				t.Errorf("Action Domain must consume SDK-backed Principal context, not a Plane Identity domain: %s imports %s", path, importPath)
			}
			switch importPath {
			case "github.com/domainry/domainry-runtime/runtime/domain/record/repository",
				"github.com/domainry/domainry-runtime/runtime/domain/record/service",
				"github.com/domainry/domainry-runtime/runtime/domain/record/runtime":
				t.Errorf("Action Domain must consume Record aggregate behavior through Action-owned contracts: %s imports %s", path, importPath)
			}
		}
	})
	validationRoot := filepath.Join(actionRoot, "validation")
	walkProductionGo(t, validationRoot, func(path string, file *ast.File) {
		if !strings.HasPrefix(filepath.Base(path), "action_") {
			t.Errorf("Action validation file must retain the action_ owner prefix: %s", path)
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err == nil && importPath == "github.com/domainry/domainry-runtime/runtime/domain/appschema" {
				t.Errorf("Action validation may consume Metadata model only, not Metadata behavior: %s", path)
			}
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if ok && function.Recv == nil && function.Name.IsExported() && !strings.HasPrefix(function.Name.Name, "Action") {
				t.Errorf("exported Action validation function must retain the Action owner prefix: %s declares %s", path, function.Name.Name)
			}
		}
	})
	policyRoot := filepath.Join(actionRoot, "policy")
	walkProductionGo(t, policyRoot, func(path string, file *ast.File) {
		if !strings.HasPrefix(filepath.Base(path), "action_") {
			t.Errorf("Action policy file must retain the action_ owner prefix: %s", path)
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			const recordPrefix = "github.com/domainry/domainry-runtime/runtime/domain/record/"
			if strings.HasPrefix(importPath, recordPrefix) && importPath != recordPrefix+"model" {
				t.Errorf("Action policy may consume Record model only, not Record behavior packages: %s imports %s", path, importPath)
			}
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if ok && function.Recv == nil && function.Name.IsExported() && !strings.HasPrefix(function.Name.Name, "Action") {
				t.Errorf("exported Action policy function must retain the Action owner prefix: %s declares %s", path, function.Name.Name)
			}
		}
	})
	projectionRoot := filepath.Join(actionRoot, "projection")
	walkProductionGo(t, projectionRoot, func(path string, file *ast.File) {
		if !strings.HasPrefix(filepath.Base(path), "action_") {
			t.Errorf("Action projection file must retain the action_ owner prefix: %s", path)
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err == nil && (importPath == "github.com/domainry/domainry-runtime/runtime/domain/action" || importPath == "github.com/domainry/domainry-runtime/runtime/domain/record/policy" || importPath == "github.com/domainry/domainry-runtime/runtime/domain/record/validation") {
				t.Errorf("Action projection may consume stable models only, not owner behavior packages: %s imports %s", path, importPath)
			}
		}
		for _, declaration := range file.Decls {
			switch value := declaration.(type) {
			case *ast.FuncDecl:
				if value.Recv == nil && value.Name.IsExported() && !strings.HasPrefix(value.Name.Name, "Action") {
					t.Errorf("exported Action projection function must retain the Action owner prefix: %s declares %s", path, value.Name.Name)
				}
			case *ast.GenDecl:
				for _, spec := range value.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if ok && typeSpec.Name.IsExported() && !strings.HasPrefix(typeSpec.Name.Name, "Action") {
						t.Errorf("exported Action projection type must retain the Action owner prefix: %s declares %s", path, typeSpec.Name.Name)
					}
				}
			}
		}
	})
}

func TestCapabilityOwnerBoundaryIsClosed(t *testing.T) {
	root := runtimeRoot(t)
	domainRoot := filepath.Join(root, "domain")
	capabilityRoot := filepath.Join(domainRoot, "capability")

	entries, err := os.ReadDir(capabilityRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
			t.Errorf("Capability root package must stay empty; move production code into an explicit role package: %s", entry.Name())
		}
	}

	if err := filepath.WalkDir(capabilityRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") && !strings.HasPrefix(entry.Name(), "capability_") {
			t.Errorf("Capability file must retain the capability_ owner prefix for global searchability: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	walkProductionGo(t, capabilityRoot, func(path string, file *ast.File) {
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.TYPE {
				continue
			}
			for _, spec := range general.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if ok && typeSpec.Name.IsExported() && !strings.HasPrefix(typeSpec.Name.Name, "Capability") {
					t.Errorf("exported Capability type must retain the Capability owner prefix: %s declares %s", path, typeSpec.Name.Name)
				}
			}
		}
	})

	forbiddenImports := technicalLayoutStringSet(
		"github.com/domainry/domainry-runtime/runtime/domain/capability",
		"github.com/domainry/domainry-runtime/runtime/domain/capability/policy",
		"github.com/domainry/domainry-runtime/runtime/domain/capability/projection",
		"github.com/domainry/domainry-runtime/runtime/domain/capability/service",
	)
	walkProductionGo(t, domainRoot, func(path string, file *ast.File) {
		if path == capabilityRoot || strings.HasPrefix(path, capabilityRoot+string(filepath.Separator)) {
			return
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err == nil && forbiddenImports[importPath] {
				t.Errorf("domain owners may consume Capability contract only: %s imports %s", path, importPath)
			}
		}
	})
}

func TestRecordOwnerLayoutIsEnforced(t *testing.T) {
	root := runtimeRoot(t)
	domainRoot := filepath.Join(root, "domain")
	recordRoot := filepath.Join(root, "domain", "record")

	entries, err := os.ReadDir(recordRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
			t.Errorf("Record root package must stay empty; move production code into an explicit role package: %s", entry.Name())
		}
	}

	if err := filepath.WalkDir(recordRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") && !strings.HasPrefix(entry.Name(), "record_") {
			t.Errorf("Record file must retain the record_ owner prefix for global searchability: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	walkProductionGo(t, recordRoot, func(path string, file *ast.File) {
		for _, declaration := range file.Decls {
			switch value := declaration.(type) {
			case *ast.GenDecl:
				if value.Tok != token.TYPE {
					continue
				}
				for _, spec := range value.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if ok && typeSpec.Name.IsExported() && !strings.HasPrefix(typeSpec.Name.Name, "Record") {
						t.Errorf("exported Record type must retain the Record owner prefix: %s declares %s", path, typeSpec.Name.Name)
					}
				}
			case *ast.FuncDecl:
				if value.Recv == nil && value.Name.IsExported() && !strings.HasPrefix(value.Name.Name, "Record") && !strings.HasPrefix(value.Name.Name, "NewRecord") {
					t.Errorf("exported Record function must retain the Record owner prefix: %s declares %s", path, value.Name.Name)
				}
			}
		}
	})

	allowedRoleImports := map[string]map[string]bool{
		"model":      {},
		"contract":   {"model": true},
		"policy":     {"contract": true, "model": true},
		"validation": {"contract": true, "model": true, "policy": true},
		"repository": {"contract": true, "model": true},
		"runtime":    {"contract": true, "model": true, "policy": true},
		"projection": {"contract": true, "model": true, "policy": true},
		"service":    {"contract": true, "model": true, "policy": true, "projection": true, "repository": true, "runtime": true, "validation": true},
	}
	walkProductionGo(t, recordRoot, func(path string, file *ast.File) {
		relative, err := filepath.Rel(recordRoot, path)
		if err != nil {
			t.Fatal(err)
		}
		sourceRole := strings.Split(filepath.ToSlash(relative), "/")[0]
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			const prefix = "github.com/domainry/domainry-runtime/runtime/domain/record/"
			if !strings.HasPrefix(importPath, prefix) {
				continue
			}
			targetRole := strings.Split(strings.TrimPrefix(importPath, prefix), "/")[0]
			if !allowedRoleImports[sourceRole][targetRole] {
				t.Errorf("Record technical-package dependency violates R3.3: %s imports %s", path, importPath)
			}
		}
	})

	repositoryRoot := filepath.Join(recordRoot, "repository")
	walkProductionGo(t, repositoryRoot, func(path string, file *ast.File) {
		for _, declaration := range file.Decls {
			switch value := declaration.(type) {
			case *ast.GenDecl:
				if value.Tok == token.IMPORT {
					continue
				}
				for _, spec := range value.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if !ok {
						t.Errorf("Record repository package may declare ports only: %s", path)
						continue
					}
					if _, ok := typeSpec.Type.(*ast.InterfaceType); !ok {
						t.Errorf("Record repository package may not own adapters: %s declares %s", path, typeSpec.Name.Name)
					}
				}
			default:
				t.Errorf("Record repository package may declare interfaces only: %s", path)
			}
		}
	})

	serviceRoot := filepath.Join(recordRoot, "service")
	walkProductionGo(t, serviceRoot, func(path string, file *ast.File) {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok {
				continue
			}
			switch function.Name.Name {
			case "SetRepository", "SetIdentity", "SetIdentityDirectory", "SetRoleWriter":
				t.Errorf("Record service dependencies must be fixed at construction: %s declares %s", path, function.Name.Name)
			}
		}
	})

	walkProductionGo(t, domainRoot, func(path string, file *ast.File) {
		if path == recordRoot || strings.HasPrefix(path, recordRoot+string(filepath.Separator)) {
			return
		}
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			const prefix = "github.com/domainry/domainry-runtime/runtime/domain/record/"
			if importPath == prefix+"service" || importPath == prefix+"runtime" {
				t.Errorf("domain owners may not consume Record behavior packages: %s imports %s", path, importPath)
			}
		}
	})
}

func TestRetiredHorizontalDomainRootsStayRetired(t *testing.T) {
	root := runtimeRoot(t)
	domainRoot := filepath.Join(root, "domain")
	retired := technicalLayoutStringSet("apperror", "mutation", "rules")
	for packageName := range retired {
		path := filepath.Join(domainRoot, packageName)
		if _, err := os.Stat(path); err == nil {
			t.Errorf("retired horizontal Domain root must not be recreated: %s", path)
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	walkProductionGo(t, root, func(path string, file *ast.File) {
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			for packageName := range retired {
				if importPath == "github.com/domainry/domainry-runtime/runtime/domain/"+packageName {
					t.Errorf("retired horizontal Domain import must use its Platform successor: %s imports %s", path, importPath)
				}
			}
		}
	})
}

func TestRuntimeLeafOwnerPackagesStayDependencyFree(t *testing.T) {
	root := runtimeRoot(t)
	allowed := map[string]map[string]bool{
		"definition": technicalLayoutStringSet(
			"github.com/domainry/domainry-runtime/runtime/domain/localization/model",
		),
		"localization": {},
		"transaction": technicalLayoutStringSet(
			"github.com/domainry/domainry-audit-sdk/contract",
			"github.com/domainry/domainry-runtime/runtime/domain/definition/model",
			"github.com/domainry/domainry-runtime/runtime/domain/integration/model",
			"github.com/domainry/domainry-runtime/runtime/domain/record/model",
			"github.com/domainry/domainry-runtime/runtime/domain/workflow/model",
		),
	}
	for owner, ownerAllowed := range allowed {
		ownerRoot := filepath.Join(root, "domain", owner)
		walkProductionGo(t, ownerRoot, func(path string, file *ast.File) {
			for _, spec := range file.Imports {
				importPath, err := strconv.Unquote(spec.Path.Value)
				if err != nil || !strings.HasPrefix(importPath, "github.com/domainry/domainry-runtime/runtime/") {
					continue
				}
				if !ownerAllowed[importPath] {
					t.Errorf("leaf Domain owner may depend only on reviewed model contracts: %s imports %s", path, importPath)
				}
			}
		})
	}
}

func TestRuntimeContextParametersComeFirst(t *testing.T) {
	root := runtimeRoot(t)
	workflowRoot := filepath.Join(root, "domain", "workflow")
	auditRoot := filepath.Join(root, "application", "auditbinding")
	walkProductionGo(t, root, func(path string, file *ast.File) {
		contextAliases := technicalLayoutStringSet("context")
		timeAliases := technicalLayoutStringSet("time")
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			alias := ""
			if spec.Name != nil && spec.Name.Name != "_" && spec.Name.Name != "." {
				alias = spec.Name.Name
			}
			switch importPath {
			case "context":
				if alias != "" {
					contextAliases[alias] = true
				}
			case "time":
				if alias != "" {
					timeAliases[alias] = true
				}
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			functionType, ok := node.(*ast.FuncType)
			if !ok || functionType.Params == nil || len(functionType.Params.List) == 0 {
				return true
			}
			hasContext := false
			for _, parameter := range functionType.Params.List {
				if isRuntimeContextType(parameter.Type, contextAliases) {
					hasContext = true
					break
				}
			}
			if hasContext && !isRuntimeContextType(functionType.Params.List[0].Type, contextAliases) {
				t.Errorf("context.Context must be the first explicit parameter: %s", path)
			}
			return true
		})
		if path == auditRoot || strings.HasPrefix(path, auditRoot+string(filepath.Separator)) {
			ast.Inspect(file, func(node ast.Node) bool {
				field, ok := node.(*ast.Field)
				if !ok || len(field.Names) == 0 {
					return true
				}
				for _, name := range field.Names {
					if name.Name != "NewAuditEvent" && name.Name != "newAuditEvent" {
						continue
					}
					functionType, ok := field.Type.(*ast.FuncType)
					if !ok || functionType.Params == nil || len(functionType.Params.List) == 0 ||
						!isRuntimeContextType(functionType.Params.List[0].Type, contextAliases) {
						t.Errorf("Audit event constructors must receive context.Context first: %s declares %s", path, name.Name)
					}
				}
				return true
			})
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok || (function.Name.Name != "NewAuditEvent" && function.Name.Name != "newAuditEvent") {
					continue
				}
				if function.Type.Params == nil || len(function.Type.Params.List) == 0 ||
					!isRuntimeContextType(function.Type.Params.List[0].Type, contextAliases) {
					t.Errorf("Audit event constructors must receive context.Context first: %s declares %s", path, function.Name.Name)
				}
			}
		}
		if path == workflowRoot || strings.HasPrefix(path, workflowRoot+string(filepath.Separator)) {
			contextFirstWorkflowPorts := technicalLayoutStringSet(
				"ObjectForAction", "ObjectMap", "CanAccessRecord", "ActionExists",
				"WorkflowSchemaSnapshot", "ConnectorAdapterExists",
			)
			ast.Inspect(file, func(node ast.Node) bool {
				field, ok := node.(*ast.Field)
				if !ok || len(field.Names) == 0 {
					return true
				}
				for _, name := range field.Names {
					if !contextFirstWorkflowPorts[name.Name] {
						continue
					}
					functionType, ok := field.Type.(*ast.FuncType)
					if !ok || functionType.Params == nil || len(functionType.Params.List) == 0 ||
						!isRuntimeContextType(functionType.Params.List[0].Type, contextAliases) {
						t.Errorf("Workflow cross-owner ports must receive context.Context first: %s declares %s", path, name.Name)
					}
				}
				return true
			})
		}
		inClockEnforcedOwner := path == workflowRoot || strings.HasPrefix(path, workflowRoot+string(filepath.Separator)) ||
			path == auditRoot || strings.HasPrefix(path, auditRoot+string(filepath.Separator))
		if !inClockEnforcedOwner {
			return
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil || !runtimeCallsTimeNow(function.Body, timeAliases) {
				continue
			}
			if function.Type.Params == nil || len(function.Type.Params.List) == 0 ||
				!isRuntimeContextType(function.Type.Params.List[0].Type, contextAliases) {
				t.Errorf("functions that read the clock must receive context.Context first: %s declares %s", path, function.Name.Name)
			}
		}
	})
}

func runtimeCallsTimeNow(node ast.Node, aliases map[string]bool) bool {
	found := false
	ast.Inspect(node, func(current ast.Node) bool {
		call, ok := current.(*ast.CallExpr)
		if !ok {
			return !found
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return !found
		}
		identifier, identifierOK := selector.X.(*ast.Ident)
		if identifierOK && selector.Sel.Name == "Now" && aliases[identifier.Name] {
			found = true
			return false
		}
		return !found
	})
	return found
}

func isRuntimeContextType(expression ast.Expr, aliases map[string]bool) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Context" {
		return false
	}
	identifier, ok := selector.X.(*ast.Ident)
	return ok && aliases[identifier.Name]
}

func assertBusinessTechnicalLayout(t *testing.T, runtimeRoot string) {
	t.Helper()
	businessRoot := filepath.Join(runtimeRoot, "domain")
	entries, err := os.ReadDir(businessRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, ownerEntry := range entries {
		if !ownerEntry.IsDir() {
			continue
		}
		owner := ownerEntry.Name()
		if businessTechnicalDirectories[owner] && owner != "runtime" {
			t.Errorf("technical role cannot be a top-level domain owner: domain/%s", owner)
		}
		baseline, known := businessOwnerRootProductionBaselines[owner]
		if !known {
			t.Errorf("new domain owner %q requires an explicit technical-layout review and baseline", owner)
			continue
		}
		ownerRoot := filepath.Join(businessRoot, owner)
		assertAtMost(t, "domain/"+owner+" root production files", technicalLayoutCountImmediateProductionGoFiles(t, ownerRoot), baseline)
		ownerPrefix := owner + "_"
		alternateOwnerPrefix := ""
		if owner == "surfacecontext" {
			ownerPrefix = "surface_context_"
		} else if owner == "appschema" {
			alternateOwnerPrefix = "application_schema_"
		} else if owner == "workspaceprovision" {
			ownerPrefix = "workspace_provision_"
		}
		if walkErr := filepath.WalkDir(ownerRoot, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") &&
				!strings.HasPrefix(entry.Name(), ownerPrefix) &&
				(alternateOwnerPrefix == "" || !strings.HasPrefix(entry.Name(), alternateOwnerPrefix)) {
				t.Errorf("Domain owner file must retain the owner prefix for global searchability: %s", path)
			}
			return nil
		}); walkErr != nil {
			t.Fatal(walkErr)
		}
		children, readErr := os.ReadDir(ownerRoot)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, child := range children {
			if !child.IsDir() {
				continue
			}
			if !businessTechnicalDirectories[child.Name()] {
				t.Errorf("domain/%s has unreviewed technical directory %q", owner, child.Name())
				continue
			}
			assertTechnicalPackageName(t, filepath.Join(ownerRoot, child.Name()), owner, child.Name())
		}
	}
}

func assertReviewedLayerDirectories(t *testing.T, root string) {
	t.Helper()
	assertOnlyReviewedDirectories(t, root, runtimeTopLevelDirectories, "Runtime top-level layer")
	assertOnlyReviewedDirectories(t, filepath.Join(root, "infrastructure"), infrastructureTopLevelDirectories, "infrastructure adapter family")
	assertOnlyReviewedDirectories(t, filepath.Join(root, "platform"), platformTopLevelDirectories, "platform capability")
	assertOnlyReviewedDirectories(t, filepath.Join(root, "transport"), transportTopLevelDirectories, "transport protocol")

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		if !strings.HasSuffix(entry.Name(), "_test.go") {
			t.Errorf("Runtime root may contain architecture contract tests only: %s", entry.Name())
		}
	}
}

func assertOnlyReviewedDirectories(t *testing.T, root string, reviewed map[string]bool, label string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() && !reviewed[entry.Name()] {
			t.Errorf("%s %q requires explicit architecture review", label, entry.Name())
		}
	}
}

func assertRuntimePathNames(t *testing.T, root string) {
	t.Helper()
	vagueNames := technicalLayoutStringSet(
		"common.go", "utils.go", "util.go", "helpers.go", "helper.go", "misc.go",
		"types.go", "base.go", "manager.go", "impl.go", "interface.go", "interfaces.go",
		"models.go", "services.go", "repositories.go",
	)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			name := entry.Name()
			isCommand := strings.HasPrefix(relative, "cmd/") && !strings.Contains(strings.TrimPrefix(relative, "cmd/"), "/")
			if isCommand {
				if !lowerKebabName.MatchString(name) {
					t.Errorf("command directory must use lower-kebab-case: %s", relative)
				}
			} else if !lowerSnakeName.MatchString(name) {
				t.Errorf("Runtime directory must use lower_snake_case: %s", relative)
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		if !goFileName.MatchString(entry.Name()) {
			t.Errorf("Go file must use lower_snake_case and *_test.go for tests: %s", relative)
		}
		if strings.HasPrefix(relative, "infrastructure/connectors/") && relative != "infrastructure/connectors/sdk/adapter.go" {
			ownerPrefix, prefixErr := connectorOwnerFilePrefix(filepath.Dir(path))
			if prefixErr != nil {
				return prefixErr
			}
			if ownerPrefix != "" && !strings.HasPrefix(entry.Name(), ownerPrefix+"_") {
				t.Errorf("connector provider file must retain its provider-capability prefix %q: %s", ownerPrefix, relative)
			}
			if entry.Name() == "adapter.go" {
				t.Errorf("connector adapter file must identify provider and capability: %s", relative)
			}
			source, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			if genericConnectorAdapterType.Match(source) {
				t.Errorf("connector adapter type must identify provider and capability: %s", relative)
			}
			if genericConnectorAdapterConstructor.Match(source) {
				t.Errorf("connector adapter constructor must identify provider and capability: %s", relative)
			}
		}
		if filepath.Base(filepath.Dir(path)) == "contract" && !strings.HasSuffix(entry.Name(), "_test.go") {
			owner := filepath.Base(filepath.Dir(filepath.Dir(path)))
			if entry.Name() == owner+".go" || entry.Name() == "contract.go" || entry.Name() == "ports.go" {
				t.Errorf("contract file must name a concrete caller capability, not its owner or technical package: %s", relative)
			}
		}
		if vagueNames[entry.Name()] && !reviewedVagueProductionFiles[relative] {
			t.Errorf("vague production filename is forbidden; name the owned capability or technical role: %s", relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func connectorOwnerFilePrefix(directory string) (string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", err
	}
	mainAdapter := ""
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasSuffix(name, "_test.go") || !strings.HasSuffix(name, "_adapter.go") {
			continue
		}
		if mainAdapter == "" || len(name) < len(mainAdapter) {
			mainAdapter = name
		}
	}
	return strings.TrimSuffix(mainAdapter, "_adapter.go"), nil
}

func assertRuntimePackageNames(t *testing.T, root string) {
	t.Helper()
	packagesByDirectory := map[string]map[string]bool{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.PackageClauseOnly)
		if parseErr != nil {
			t.Errorf("parse package declaration %s: %v", path, parseErr)
			return nil
		}
		name := file.Name.Name
		if !goPackageName.MatchString(name) {
			t.Errorf("package name must be lowercase and semantic: %s declares %s", path, name)
		}
		if strings.HasSuffix(entry.Name(), "_test.go") && strings.HasSuffix(name, "_test") {
			return nil
		}
		directory := filepath.Dir(path)
		if packagesByDirectory[directory] == nil {
			packagesByDirectory[directory] = map[string]bool{}
		}
		packagesByDirectory[directory][name] = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for directory, packages := range packagesByDirectory {
		relative, relErr := filepath.Rel(root, directory)
		if relErr != nil {
			t.Fatal(relErr)
		}
		relative = filepath.ToSlash(relative)
		if relative == "." {
			continue
		}
		want := strings.ReplaceAll(filepath.Base(directory), "_", "")
		if want == "model" {
			want = strings.ReplaceAll(filepath.Base(filepath.Dir(directory)), "_", "") + "model"
		}
		if strings.HasPrefix(relative, "cmd/") {
			want = "main"
		}
		if strings.HasPrefix(relative, "application/seed/") {
			want += "seed"
		}
		if exception, ok := reviewedPackageNameExceptions[relative]; ok {
			want = exception
		}
		if len(packages) != 1 || !packages[want] {
			t.Errorf("package must match directory owner: %s declares %v, want %s", relative, sortedPackageNames(packages), want)
		}
	}
}

func assertRuntimeExportedSymbolNames(t *testing.T, root string) {
	t.Helper()
	walkProductionGo(t, root, func(path string, file *ast.File) {
		ast.Inspect(file, func(node ast.Node) bool {
			switch declaration := node.(type) {
			case *ast.TypeSpec:
				assertExportedIdentifierName(t, path, declaration.Name.Name)
				if interfaceType, ok := declaration.Type.(*ast.InterfaceType); ok && interfaceType != nil && regexp.MustCompile(`^I[A-Z]`).MatchString(declaration.Name.Name) {
					t.Errorf("Go interface names must describe capability and must not use an I prefix: %s declares %s", path, declaration.Name.Name)
				}
			case *ast.FuncDecl:
				assertExportedIdentifierName(t, path, declaration.Name.Name)
			case *ast.ValueSpec:
				for _, name := range declaration.Names {
					assertExportedIdentifierName(t, path, name.Name)
				}
			}
			return true
		})
	})
}

func assertExportedIdentifierName(t *testing.T, path, name string) {
	t.Helper()
	if ast.IsExported(name) && strings.Contains(name, "_") {
		t.Errorf("exported Go identifier must use MixedCaps without underscores: %s declares %s", path, name)
	}
}

func sortedPackageNames(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func assertApplicationTechnicalLayout(t *testing.T, runtimeRoot string) {
	t.Helper()
	appRoot := filepath.Join(runtimeRoot, "application")
	entries, err := os.ReadDir(appRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if !applicationTopLevelDirectories[entry.Name()] {
			t.Errorf("application has unreviewed technical directory %q", entry.Name())
		}
	}
	for relative, baseline := range applicationProductionBaselines {
		path := appRoot
		if relative != "." {
			path = filepath.Join(appRoot, relative)
		}
		assertAtMost(t, "application/"+relative+" production files", technicalLayoutCountImmediateProductionGoFiles(t, path), baseline)
	}
	forbidden := []string{"model", "validation", "repository", "policy"}
	for _, name := range forbidden {
		if stat, statErr := os.Stat(filepath.Join(appRoot, name)); statErr == nil && stat.IsDir() {
			t.Errorf("application must not own domain technical role directory %q", name)
		}
	}
	if err := filepath.WalkDir(appRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		if !strings.HasSuffix(entry.Name(), "_test.go") && strings.Contains(strings.ToLower(entry.Name()), "legacy") {
			relative, relErr := filepath.Rel(runtimeRoot, path)
			if relErr != nil {
				return relErr
			}
			t.Errorf("Application production files must not contain legacy compatibility adapters: %s", filepath.ToSlash(relative))
		}
		if strings.Contains(entry.Name(), "_domain_service") {
			relative, relErr := filepath.Rel(runtimeRoot, path)
			if relErr != nil {
				return relErr
			}
			t.Errorf("Application file names must describe an application use case, not a Domain Service: %s", filepath.ToSlash(relative))
		}
		if strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		file := parseFile(t, path)
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.TYPE {
				continue
			}
			for _, specification := range general.Specs {
				typeSpec, ok := specification.(*ast.TypeSpec)
				if !ok || !strings.HasSuffix(typeSpec.Name.Name, "Repository") {
					continue
				}
				if _, isInterface := typeSpec.Type.(*ast.InterfaceType); isInterface {
					relative, relErr := filepath.Rel(runtimeRoot, path)
					if relErr != nil {
						return relErr
					}
					t.Errorf("Application must consume Domain Repository contracts instead of declaring %s: %s", typeSpec.Name.Name, filepath.ToSlash(relative))
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func assertHTTPTechnicalLayout(t *testing.T, runtimeRoot string) {
	t.Helper()
	httpRoot := filepath.Join(runtimeRoot, "transport", "http")
	entries, err := os.ReadDir(httpRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			if !httpOwnerDirectories[entry.Name()] && !httpTechnicalDirectories[entry.Name()] {
				t.Errorf("HTTP owner %q requires explicit route ownership review", entry.Name())
			}
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if !httpRootProductionFiles[name] {
			t.Errorf("unclassified HTTP root production file %q; use server/routes/middleware/request/response/metrics or an owner package", name)
		}
	}
	for _, owner := range sortedKeys(httpOwnerDirectories) {
		ownerRoot := filepath.Join(httpRoot, owner)
		children, readErr := os.ReadDir(ownerRoot)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, child := range children {
			if child.IsDir() {
				t.Errorf("HTTP owner %s must classify transport roles by file, not nested catch-all package %s", owner, child.Name())
			}
		}
	}
}

func assertNoCatchAllDirectories(t *testing.T, root string) {
	t.Helper()
	forbidden := technicalLayoutStringSet("common", "utils", "shared", "library")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && forbidden[entry.Name()] {
			t.Errorf("catch-all directory is forbidden: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertTechnicalPackageName(t *testing.T, root, owner, technicalRole string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	want := technicalRole
	if technicalRole == "model" {
		want = owner + "model"
	}
	if exception, ok := reviewedPackageNameExceptions[filepath.ToSlash(filepath.Join("domain", owner, technicalRole))]; ok {
		want = exception
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.PackageClauseOnly)
		if parseErr != nil {
			t.Errorf("parse package declaration %s: %v", path, parseErr)
			continue
		}
		if file.Name.Name != want {
			t.Errorf("%s declares package %s, want %s", path, file.Name.Name, want)
		}
	}
}

func assertProductionFileLineBudget(t *testing.T, root string, defaultLimit int, reviewed map[string]int) {
	t.Helper()
	runtimeRoot := runtimeRoot(t)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		lines, countErr := countLines(path)
		if countErr != nil {
			t.Errorf("count lines in %s: %v", path, countErr)
			return nil
		}
		relative, relErr := filepath.Rel(runtimeRoot, path)
		if relErr != nil {
			t.Fatal(relErr)
		}
		key := filepath.ToSlash(relative)
		limit := defaultLimit
		if baseline, ok := reviewed[key]; ok {
			limit = baseline
		}
		if lines > limit {
			t.Errorf("production Go file exceeds technical-layout line budget: %s has %d lines, limit %d", key, lines, limit)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func countLines(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	lines := 0
	for scanner.Scan() {
		lines++
	}
	return lines, scanner.Err()
}

func technicalLayoutCountImmediateProductionGoFiles(t *testing.T, root string) int {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
			count++
		}
	}
	return count
}

func technicalLayoutStringSet(values ...string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func sortedKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
