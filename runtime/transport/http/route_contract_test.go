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
	mux.HandleFunc("POST /records/{objectKey}", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("POST /integration/webhooks/{workspaceID}/{connectionKey}", func(http.ResponseWriter, *http.Request) {})
	mux.HandleFunc("/{path...}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })

	recordRequest := httptest.NewRequest(http.MethodPost, "/records/report", nil)
	recordPolicy := routePolicyFor(mux, recordRequest)
	if recordPolicy.path != "/records/{objectKey}" || recordPolicy.anonymous() || recordPolicy.fallback {
		t.Fatalf("unexpected record route policy: %#v", recordPolicy)
	}

	webhookRequest := httptest.NewRequest(http.MethodPost, "/integration/webhooks/workspace-a/slack", nil)
	webhookPolicy := routePolicyFor(mux, webhookRequest)
	if webhookPolicy.anonymous() || webhookPolicy.fallback {
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

func TestEveryRuntimeRouteIsDocumentedByAggregatedOpenAPI(t *testing.T) {
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

	sort.Strings(missingFromSpec)
	if len(missingFromSpec) > 0 {
		t.Fatalf("Runtime routes missing from aggregated OpenAPI: %v", missingFromSpec)
	}
}

func TestEveryRuntimeRouteHasACompleteCompiledEndpointContract(t *testing.T) {
	routes := declaredRuntimeRoutes(t)
	if err := validateCompiledEndpointContracts(); err != nil {
		t.Fatal(err)
	}
	for route := range routes {
		contract, exists := runtimeEndpointContracts[route]
		if !exists {
			t.Errorf("%s has no compiled endpoint contract", route)
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

func TestEveryRuntimeRouteUsesItsOwnerNamespace(t *testing.T) {
	namespaces := map[string]string{
		"metadata":           "/metadata",
		"automation":         "/automation",
		"businessreferences": "/references",
		"businesssystem":     "/authoring",
		"businessevents":     "/realtime",
		"appschema":          "/application-schema",
		"dispatch":           "/dispatch",
		"discovery":          "/discovery",
		"lifecycle":          "/lifecycle",
		"notifications":      "/notification",
		"operations":         "/operations",
		"publicationhandoff": "/publication-handoffs",
		"records":            "/records",
		"provision":          "/provision",
		"uploads":            "/uploads",
		"workflows":          "/workflow",
		"workspaceprovision": "/workspaces",
	}
	for identity, contract := range runtimeEndpointContracts {
		if contract.SourceOwner == "root" || contract.SourceOwner == "openapi" {
			continue
		}
		root, found := namespaces[contract.SourceOwner]
		if !found {
			t.Errorf("%s has no URL namespace for owner %q", identity, contract.SourceOwner)
			continue
		}
		_, path, _ := strings.Cut(identity, " ")
		if path != root && !strings.HasPrefix(path, root+"/") {
			t.Errorf("%s owned by %q must be rooted at %q", identity, contract.SourceOwner, root)
		}
	}
}

func TestLegacyFrontendSurfaceRoutesAreNotPublished(t *testing.T) {
	routes := declaredRuntimeRoutes(t)
	spec := runtimeopenapi.Build(appschemamodel.ApplicationSchemaSnapshot{})
	paths := spec["paths"].(map[string]any)
	for _, identity := range []string{
		"GET /business/surface-context",
		"GET /portal/surface-context",
		"POST /surfaces/{surfaceKey}/context",
	} {
		method, path, _ := strings.Cut(identity, " ")
		if _, exists := routes[identity]; exists {
			t.Errorf("legacy frontend Surface route is still registered: %s", identity)
		}
		if _, exists := runtimeEndpointContracts[identity]; exists {
			t.Errorf("legacy frontend Surface route still has an endpoint contract: %s", identity)
		}
		if item, exists := paths[path].(map[string]any); exists && item[strings.ToLower(method)] != nil {
			t.Errorf("legacy frontend Surface route is still published by OpenAPI: %s", identity)
		}
	}
}

func TestRuntimeRoutesDoNotReintroduceTransportContainerSegments(t *testing.T) {
	routes := declaredRuntimeRoutes(t)
	paths := runtimeopenapi.Build(appschemamodel.ApplicationSchemaSnapshot{})["paths"].(map[string]any)
	forbidden := []string{
		"/runtime/",
		"/records/objects/",
		"/workflow/operations/",
		"/workspace/provision",
		"/uploads/files",
		"/operations/bulk/dead-letters",
		"/automation/rules/capabilities",
		"/automation/rules/executions",
		"/automation/rules/validate",
		"/automation/rules/simulate",
		"/automation/rules/authoring-fragments",
	}
	assertCanonical := func(kind, value string) {
		t.Helper()
		for _, fragment := range forbidden {
			if strings.Contains(value, fragment) {
				t.Errorf("%s still contains transport container %q: %s", kind, fragment, value)
			}
		}
	}
	for identity := range routes {
		assertCanonical("registered route", identity)
	}
	for identity := range runtimeEndpointContracts {
		assertCanonical("endpoint contract", identity)
	}
	for path := range paths {
		assertCanonical("OpenAPI path", path)
	}
}

func TestRuntimeDoesNotPublishIntegrationOwnerWebhookRoute(t *testing.T) {
	routes := declaredRuntimeRoutes(t)
	webhookRoutes := make([]string, 0)
	for route := range routes {
		if strings.Contains(strings.ToLower(route), "webhook") && !strings.Contains(route, "/webhook-subscriptions") {
			webhookRoutes = append(webhookRoutes, route)
		}
	}
	sort.Strings(webhookRoutes)
	if len(webhookRoutes) != 0 {
		t.Fatalf("Runtime published Integration-owner webhook routes=%v", webhookRoutes)
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
	if len(webhookPaths) != 0 {
		t.Fatalf("Runtime OpenAPI published Integration-owner webhook paths=%v", webhookPaths)
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
		"applicationSchemaHTTP": false,
		"businessSystemHTTP":    false,
		"businessReferenceHTTP": false, "lifecycleHTTP": false,
		"workspaceProvisionHTTP": false,
		"uploadHTTP":             false, "recordHTTP": false, "workflowHTTP": false,
		"automationHTTP": false, "dispatchHTTP": false,
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
	files = append(files, filepath.Join(filepath.Dir(current), "uploads", "uploads_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "discovery", "discovery_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "openapi", "openapi_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "workflows", "workflows_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "lifecycle", "lifecycle_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "automation", "automation_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "dispatch", "dispatch_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "appschema", "appschema_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "businessreferences", "businessreferences_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "publicationhandoff", "publication_handoff_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "businesssystem", "businesssystem_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "operations", "operations_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "businessevents", "businessevents_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "notifications", "notifications_routes.go"))
	files = append(files, filepath.Join(filepath.Dir(current), "workspaceprovision", "workspaceprovision_routes.go"))
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
	for _, prefix := range []string{"/agent/", "/auth/", "/identity/", "/discovery/i18n/", "/uploads/"} {
		if strings.HasPrefix(path, prefix) {
			return "internal or separately governed protocol surface"
		}
	}
	switch path {
	case "/", "/health", "/metrics", "/uploads":
		return "operational or binary transport endpoint"
	case "/integration/events/process-due", "/integration/events/{eventID}/status",
		"/integration/invocations/{invocationID}/status", "/integration/outbox/process-due",
		"/integration/outbox/{messageID}/status":
		return "internal worker control endpoint"
	default:
		return ""
	}
}

func isSchemaDerivedOpenAPIPath(path string) bool {
	return strings.HasPrefix(path, "/records/{objectKey}") || strings.Contains(path, "/actions/{actionKey}")
}

func isHTTPMethod(value string) bool {
	switch strings.ToUpper(value) {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return true
	default:
		return false
	}
}
