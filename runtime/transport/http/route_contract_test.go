package http

import appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

// These tests guard composition and route/OpenAPI drift across HTTP owner packages.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	runtimeopenapi "github.com/domainry/domainry-runtime/runtime/transport/http/openapi"
)

func TestFallbackRouteCanReturnJSONBeforeAuthentication(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("GET /schema", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("/{path...}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	if fallbackRoute(mux, httptest.NewRequest(http.MethodGet, "/schema", nil)) {
		t.Fatal("known domain route must still pass through authentication policy")
	}
	if !fallbackRoute(mux, httptest.NewRequest(http.MethodGet, "/unknown.json", nil)) {
		t.Fatal("unknown route must reach the structured JSON 404 without an authentication challenge")
	}
}

func TestRoutePolicyUsesRegisteredPatternInsteadOfUserPathSegments(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /objects/{objectKey}/records", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("POST /integrations/webhooks/{workspaceID}/{connectionKey}", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("/{path...}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })

	recordRequest := httptest.NewRequest(http.MethodPost, "/objects/reports/records", nil)
	recordPolicy := routePolicyFor(mux, recordRequest)
	if recordPolicy.path != "/objects/{objectKey}/records" || recordPolicy.anonymous() || recordPolicy.fallback {
		t.Fatalf("unexpected record route policy: %#v", recordPolicy)
	}

	webhookRequest := httptest.NewRequest(http.MethodPost, "/integrations/webhooks/workspace-a/slack", nil)
	webhookPolicy := routePolicyFor(mux, webhookRequest)
	if !webhookPolicy.anonymous() || webhookPolicy.fallback {
		t.Fatalf("unexpected webhook route policy: %#v", webhookPolicy)
	}

	unknownRequest := httptest.NewRequest(http.MethodGet, "/auth/not-a-real-route", nil)
	unknownPolicy := routePolicyFor(mux, unknownRequest)
	if unknownPolicy.anonymous() || !unknownPolicy.fallback {
		t.Fatalf("unknown route inherited a path-prefix policy: %#v", unknownPolicy)
	}
	response := httptest.NewRecorder()
	(&HTTPRouter{}).withAuth(mux, mux).ServeHTTP(response, unknownRequest)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown route was intercepted by authentication: status=%d", response.Code)
	}
}

func TestRuntimeRoutesAndOpenAPIDoNotDrift(t *testing.T) {
	routes := declaredRuntimeRoutes(t)
	spec := runtimeopenapi.Build(appschemamodel.ApplicationSchemaSnapshot{})
	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatal("OpenAPI paths missing")
	}

	missingFromSpec := []string{}
	for contract := range routes {
		method, path, _ := strings.Cut(contract, " ")
		if reason := runtimeRouteOpenAPIExclusion(method, path); reason != "" {
			continue
		}
		pathSpec, exists := paths[path].(map[string]any)
		if !exists || pathSpec[strings.ToLower(method)] == nil {
			missingFromSpec = append(missingFromSpec, contract)
		}
	}

	missingFromRoutes := []string{}
	for path, raw := range paths {
		pathSpec, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		for method := range pathSpec {
			method = strings.ToUpper(method)
			if !isHTTPMethod(method) || isSchemaDerivedOpenAPIPath(path) {
				continue
			}
			contract := method + " " + path
			if !routes[contract] && runtimeRouteOpenAPIExclusion(method, path) == "" {
				missingFromRoutes = append(missingFromRoutes, contract)
			}
		}
	}

	sort.Strings(missingFromSpec)
	sort.Strings(missingFromRoutes)
	if len(missingFromSpec) > 0 || len(missingFromRoutes) > 0 {
		t.Fatalf("route/OpenAPI drift\nmissing from spec: %v\nmissing from routes: %v", missingFromSpec, missingFromRoutes)
	}
}

func TestEveryRuntimeRouteHasACompleteCompiledEndpointSurfaceContract(t *testing.T) {
	routes := declaredRuntimeRoutes(t)
	if err := validateCompiledEndpointSurfaceContracts(); err != nil {
		t.Fatal(err)
	}
	for route := range routes {
		contract, exists := runtimeEndpointSurfaceContracts[route]
		if !exists {
			t.Errorf("%s has no compiled endpoint Surface contract", route)
			continue
		}
		if strings.Contains(contract.PermissionPolicyRef, "owner_handler_policy") &&
			!strings.Contains(contract.PermissionPolicyRef, ".") {
			t.Errorf("%s has an unbound owner permission policy %q", route, contract.PermissionPolicyRef)
		}
		if contract.EffectClass == "write" &&
			(contract.IdempotencyDecision == "" || contract.AuditClass == "" || contract.HighRiskPolicy == "") {
			t.Errorf("%s has an incomplete write contract: %+v", route, contract)
		}
	}
}

func TestRuntimePublishesOneInboundWebhookRoute(t *testing.T) {
	routes := declaredRuntimeRoutes(t)
	want := "POST /integrations/webhooks/{workspaceID}/{connectionKey}"
	webhookRoutes := make([]string, 0)
	for route := range routes {
		if strings.Contains(strings.ToLower(route), "webhook") && !strings.Contains(route, "/webhook-subscriptions") {
			webhookRoutes = append(webhookRoutes, route)
		}
	}
	sort.Strings(webhookRoutes)
	if len(webhookRoutes) != 1 || webhookRoutes[0] != want {
		t.Fatalf("inbound webhook routes=%v want=[%s]", webhookRoutes, want)
	}

	spec := runtimeopenapi.Build(appschemamodel.ApplicationSchemaSnapshot{})
	paths := spec["paths"].(map[string]any)
	webhookPaths := make([]string, 0)
	for path := range paths {
		if strings.Contains(strings.ToLower(path), "webhook") && !strings.Contains(path, "/webhook-subscriptions") {
			webhookPaths = append(webhookPaths, path)
		}
	}
	sort.Strings(webhookPaths)
	if len(webhookPaths) != 1 || webhookPaths[0] != "/integrations/webhooks/{workspaceID}/{connectionKey}" {
		t.Fatalf("OpenAPI inbound webhook paths=%v", webhookPaths)
	}
}

func TestRoutesOnlyComposesDomainRegistrarsAndGlobalMiddleware(t *testing.T) {
	_, current, _, _ := runtime.Caller(0)
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(filepath.Dir(current), "http_router.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]bool{
		"discoveryHTTP": false, "openAPIHTTP": false,
		"reportHTTP": false, "agentDialogHTTP": false, "applicationSchemaHTTP": false,
		"capabilityHTTP": false, "frontendCapabilityHTTP": false, "businessSystemHTTP": false,
		"businessReferenceHTTP": false, "lifecycleHTTP": false,
		"uploadHTTP": false, "surfaceContextHTTP": false, "recordHTTP": false, "workflowHTTP": false,
		"automationHTTP": false, "schedulerHTTP": false,
		"operationsHTTP": false, "businessEventHTTP": false,
	}
	allowedDirectRoutes := map[string]bool{
		"GET /health": true, "GET /metrics": true, "GET /": true,
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "Routes" {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if selector.Sel.Name == "HandleFunc" && len(call.Args) > 0 {
				literal, ok := call.Args[0].(*ast.BasicLit)
				if !ok || !allowedDirectRoutes[strings.Trim(literal.Value, "\"")] {
					t.Errorf("Routes directly registers non-composition endpoint %v", call.Args[0])
				}
			}
			if selector.Sel.Name == "RegisterRoutes" {
				owner, ok := selector.X.(*ast.SelectorExpr)
				if ok {
					if _, exists := expected[owner.Sel.Name]; exists {
						expected[owner.Sel.Name] = true
					}
				}
			}
			return true
		})
	}
	missing := []string{}
	for name, found := range expected {
		if !found {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("Routes is missing domain registrars: %v", missing)
	}
}

func declaredRuntimeRoutes(t *testing.T) map[string]bool {
	t.Helper()
	_, current, _, _ := runtime.Caller(0)
	files := []string{filepath.Join(filepath.Dir(current), "http_router.go"), filepath.Join(filepath.Dir(current), "routes.go")}
	files = append(files, filepath.Join(filepath.Dir(current), "records", "records_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "party", "party_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "agentdialog", "agentdialog_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "surfacecontext", "surfacecontext_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "uploads", "uploads_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "discovery", "discovery_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "openapi", "openapi_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "workflows", "workflows_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "lifecycle", "lifecycle_handler.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "automation", "automation_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "scheduler", "scheduler_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "reports", "reports_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "frontendcapability", "frontendcapability_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "appschema", "appschema_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "businessreferences", "businessreferences_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "businesssystem", "businesssystem_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "capabilities", "capabilities_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "operations", "operations_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "businessevents", "businessevents_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "notifications", "notifications_routes.go"))
	routes := map[string]bool{}
	for _, filename := range files {
		file, parseErr := parser.ParseFile(token.NewFileSet(), filename, nil, 0)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "HandleFunc" {
				return true
			}
			literal, ok := call.Args[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			contract := strings.Trim(literal.Value, "\"")
			method, path, found := strings.Cut(contract, " ")
			if found && isHTTPMethod(method) && strings.HasPrefix(path, "/") {
				routes[method+" "+path] = true
			}
			return true
		})
	}
	return routes
}

func runtimeRouteOpenAPIExclusion(_ string, path string) string {
	for _, prefix := range []string{"/agent-dialog/", "/auth/", "/identity/", "/i18n/", "/uploads/"} {
		if strings.HasPrefix(path, prefix) {
			return "internal or separately governed protocol surface"
		}
	}
	switch path {
	case "/", "/health", "/metrics", "/files":
		return "operational or binary transport endpoint"
	case "/integrations/events/process-due", "/integrations/events/{eventID}/status",
		"/integrations/invocations/{invocationID}/status", "/integrations/outbox/process-due",
		"/integrations/outbox/{messageID}/status", "/workflow-executions/process":
		return "internal worker control endpoint"
	default:
		return ""
	}
}

func isSchemaDerivedOpenAPIPath(path string) bool {
	return strings.HasPrefix(path, "/objects/{objectKey}") || strings.Contains(path, "/actions/{actionKey}")
}

func isHTTPMethod(value string) bool {
	switch strings.ToUpper(value) {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return true
	default:
		return false
	}
}
