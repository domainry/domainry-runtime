package http

import (
	"context"
	"errors"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	capacityplatform "github.com/domainry/domainry-runtime/runtime/platform/capacity"
	healthplatform "github.com/domainry/domainry-runtime/runtime/platform/health"
	agentdialoghttp "github.com/domainry/domainry-runtime/runtime/transport/http/agentdialog"
	notificationhttp "github.com/domainry/domainry-runtime/runtime/transport/http/notifications"
)

type routerIdentityMiddlewareStub struct {
	principal identitysdk.Principal
	invalid   bool
}

func (stub routerIdentityMiddlewareStub) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if stub.invalid {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		parts := strings.Fields(request.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		identity := identitysdk.RequestIdentity{Principal: stub.principal, AccessToken: "token"}
		next.ServeHTTP(writer, request.WithContext(identitysdk.WithRequestIdentity(request.Context(), identity)))
	})
}

func (stub routerIdentityMiddlewareStub) RequirePasswordChanged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := identitysdk.RequestIdentityFromContext(r.Context())
		if !ok || identity.Principal.MustChangePassword {
			status := http.StatusUnauthorized
			if ok {
				status = http.StatusForbidden
			}
			w.WriteHeader(status)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func routerWithIdentitySDK(principal identitysdk.Principal) *HTTPRouter {
	return &HTTPRouter{
		identityAuthentication: routerIdentityMiddlewareStub{principal: principal},
		identityPrincipal: func(source identitysdk.Principal, requestID string) principalmodel.Principal {
			return principalmodel.NewPrincipalFromIdentity(source, requestID)
		},
	}
}

func requestWithSDKIdentity(request *http.Request, principal identitysdk.Principal) *http.Request {
	identity := identitysdk.RequestIdentity{Principal: principal, AccessToken: "token"}
	return request.WithContext(identitysdk.WithRequestIdentity(request.Context(), identity))
}

type routerIntegrationAuthStub struct {
	principal principalmodel.Principal
	error     error
}

func (s routerIntegrationAuthStub) PrincipalFromIntegrationAPIKey(context.Context, string, string, string) (principalmodel.Principal, integrationmodel.IntegrationAPIKey, error) {
	return s.principal, integrationmodel.IntegrationAPIKey{}, s.error
}

type routerIdentityAuthorizationStub struct {
	principal principalmodel.Principal
	error     error
}

func (s routerIdentityAuthorizationStub) Resolve(context.Context, identitysdk.PrincipalResolutionRequest) (identitysdk.PrincipalResolution, error) {
	if s.error != nil {
		return identitysdk.PrincipalResolution{}, s.error
	}
	bundle := identitysdk.AccessBundle{}
	if s.principal.AccessBundle != nil {
		bundle = *s.principal.AccessBundle
	}
	return identitysdk.PrincipalResolution{Principal: s.principal.Principal, AccessBundle: bundle}, nil
}

func routerTestPrincipal(principal identitysdk.Principal, permissions ...string) principalmodel.Principal {
	bundle := identitysdk.AccessBundle{ContractVersion: identitysdk.CurrentPolicyBundleVersion}
	for _, permission := range permissions {
		resource, action, ok := strings.Cut(permission, ".")
		if !ok {
			continue
		}
		bundle.FunctionGrants = append(bundle.FunctionGrants, identitysdk.FunctionGrant{
			Resource: identitysdk.ResourceType(resource), Action: identitysdk.Action(action), Effect: identitysdk.EffectAllow,
		})
	}
	principal.AccessBundle = &bundle
	return principalmodel.NewPrincipalFromIdentity(principal, "")
}

type routerBusinessPrincipalStub struct {
	principal principalmodel.Principal
	error     error
	selection [3]string
}

func (s *routerBusinessPrincipalStub) ResolveBusinessPrincipal(_ context.Context, _ principalmodel.Principal, surfaceKey, bindingKey, recordID string) (principalmodel.Principal, error) {
	s.selection = [3]string{surfaceKey, bindingKey, recordID}
	return s.principal, s.error
}

type routerAuditStub struct {
	events   []string
	metadata []map[string]any
}

type routerCountingRegistrar struct{ calls *int }

func (s routerCountingRegistrar) RegisterRoutes(*http.ServeMux) { *s.calls++ }

func completeRouterForSurfaceGroupTests(config HTTPRouterConfig) *HTTPRouter {
	router := NewHTTPRouter(config, HTTPRouterDependencies{})
	calls := 0
	registrar := routerCountingRegistrar{calls: &calls}
	router.recordHTTP, router.surfaceContextHTTP, router.uploadHTTP, router.discoveryHTTP, router.openAPIHTTP = registrar, registrar, registrar, registrar, registrar
	router.workflowHTTP, router.automationHTTP, router.schedulerHTTP, router.reportHTTP, router.frontendCapabilityHTTP = registrar, registrar, registrar, registrar, registrar
	router.businessReferenceHTTP, router.businessSystemHTTP, router.capabilityHTTP, router.changePlanHTTP, router.integrationHTTP = registrar, registrar, registrar, registrar, registrar
	router.applicationSchemaHTTP, router.operationsHTTP = registrar, registrar
	router.partyHTTP = registrar
	return router
}

type routerRuntimeStatusStub struct {
	storageErr   error
	migrationErr error
}

func (s routerRuntimeStatusStub) Health(context.Context) map[string]any {
	return map[string]any{"status": "ok"}
}
func (s routerRuntimeStatusStub) Metrics(context.Context) map[string]any {
	return map[string]any{"runtime_id": "runtime-1", "objects": 2}
}
func (s routerRuntimeStatusStub) StorageReadiness(context.Context) error   { return s.storageErr }
func (s routerRuntimeStatusStub) MigrationReadiness(context.Context) error { return s.migrationErr }

type routerFailingResponseWriter struct{ header http.Header }

func (w *routerFailingResponseWriter) Header() http.Header     { return w.header }
func (*routerFailingResponseWriter) WriteHeader(int)           {}
func (*routerFailingResponseWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func (s *routerAuditStub) AppendWithMetadata(_ context.Context, event, _, _ string, _ principalmodel.Principal, _ string, _, _, metadata map[string]any) {
	s.events = append(s.events, event)
	s.metadata = append(s.metadata, metadata)
}

func TestOperationalControlRemainingStateMatrix(t *testing.T) {
	called := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called++; w.WriteHeader(http.StatusNoContent) })
	(&HTTPRouter{}).withOperationalControls(nil, next).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/records", nil))
	if called != 1 {
		t.Fatal("missing control provider did not pass the request through")
	}

	for _, test := range []struct {
		name           string
		maintenance    bool
		draining       bool
		maintenanceErr error
		drainErr       error
		wantCode       string
	}{
		{name: "drain lookup error", drainErr: errors.New("drain unavailable"), wantCode: "control_state_unavailable"},
		{name: "maintenance", maintenance: true, wantCode: "maintenance_active"},
		{name: "draining", draining: true, wantCode: "instance_draining"},
		{name: "maintenance and draining prefers drain", maintenance: true, draining: true, wantCode: "instance_draining"},
		{name: "inactive", wantCode: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			router := &HTTPRouter{healthRegistry: healthplatform.NewRegistry(), runtimeInstanceID: "runtime-1"}
			router.operationsControlState = func(_ context.Context, kind, owner string) (bool, bool, error) {
				if kind == "maintenance" {
					return test.maintenance, false, test.maintenanceErr
				}
				if owner != "runtime-1" {
					t.Fatalf("drain owner=%q", owner)
				}
				return test.draining, false, test.drainErr
			}
			response := httptest.NewRecorder()
			router.withOperationalControls(nil, next).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/records", nil))
			if test.wantCode == "" {
				if response.Code != http.StatusNoContent {
					t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
				}
			} else if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), test.wantCode) || response.Header().Get("Retry-After") != "5" {
				t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
			}
		})
	}
}

func TestRefreshAndExemptOperationalControlEdges(t *testing.T) {
	(*HTTPRouter)(nil).refreshOperationalState(t.Context())
	(&HTTPRouter{}).refreshOperationalState(t.Context())
	(&HTTPRouter{operationsControlState: func(context.Context, string, string) (bool, bool, error) { return true, false, nil }}).refreshOperationalState(t.Context())

	router := &HTTPRouter{healthRegistry: healthplatform.NewRegistry(), runtimeInstanceID: "runtime-2"}
	router.operationsControlState = func(_ context.Context, kind, _ string) (bool, bool, error) {
		if kind == "maintenance" {
			return true, false, nil
		}
		return true, false, errors.New("keep previous drain state")
	}
	router.refreshOperationalState(t.Context())
	if !router.healthRegistry.Maintenance() || router.healthRegistry.Draining() {
		t.Fatalf("operational state maintenance=%v draining=%v", router.healthRegistry.Maintenance(), router.healthRegistry.Draining())
	}

	for _, test := range []struct {
		method, path string
		exempt       bool
	}{
		{http.MethodGet, "/records", true}, {http.MethodHead, "/records", true}, {http.MethodOptions, "/records", true},
		{http.MethodPost, "/operations", true}, {http.MethodDelete, "/operations/jobs/1", true}, {http.MethodPost, "/records", false},
	} {
		if got := operationalControlExempt(httptest.NewRequest(test.method, test.path, nil), test.path); got != test.exempt {
			t.Fatalf("%s %s exempt=%v want=%v", test.method, test.path, got, test.exempt)
		}
	}
	if !operationalControlExempt(nil, "") {
		t.Fatal("nil request must be exempt")
	}
}

func TestRoutePolicyAndAnonymousPathCompleteMatrix(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/unregistered", nil)
	if policy := routePolicyFor(nil, request); policy.path != "/unregistered" || policy.fallback {
		t.Fatalf("nil routes policy=%+v", policy)
	}
	emptyMux := http.NewServeMux()
	if policy := routePolicyFor(emptyMux, request); policy.path != "/unregistered" || policy.fallback {
		t.Fatalf("empty routes policy=%+v", policy)
	}
	rootMux := http.NewServeMux()
	rootMux.HandleFunc("/", func(http.ResponseWriter, *http.Request) {})
	if policy := routePolicyFor(rootMux, request); !policy.fallback {
		t.Fatalf("root catch-all policy=%+v", policy)
	}

	paths := []string{"/", "/live", "/ready", "/startup", "/i18n/en", "/integrations/webhooks/{workspaceID}/{connectionKey}", "/integrations/google/oauth/callback", "/agent-dialog/task-tools/invoke"}
	for _, path := range paths {
		if !anonymousAuthPath(path) {
			t.Errorf("anonymous path rejected: %s", path)
		}
	}
	for _, path := range []string{"", "/records", "/metrics", "/openapi.json", "/schema", "/tenant-admin/platform-capabilities", "/domain-system-snapshot", "/metadata/manifests/x", "/api/x", "/auth/me", "/auth/change-password", "/auth/external-accounts", "/integrations/webhooks/provider/events"} {
		if anonymousAuthPath(path) {
			t.Errorf("protected path accepted as anonymous: %s", path)
		}
	}
	if anonymousAuthPath("") {
		t.Fatal("protected path accepted as anonymous")
	}
	if fallbackRoute(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), request) {
		t.Fatal("non-mux reported fallback")
	}
}

func TestJSONDecodeDefaultLimitTrailingAndErrorContracts(t *testing.T) {
	router := &HTTPRouter{}
	for _, test := range []struct {
		body   string
		ok     bool
		status int
	}{
		{body: `{"name":"ok"}`, ok: true, status: http.StatusOK},
		{body: `{"name":"ok"} {}`, ok: false, status: http.StatusBadRequest},
		{body: `{"unknown":true}`, ok: false, status: http.StatusBadRequest},
	} {
		var value struct {
			Name string `json:"name"`
		}
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(test.body))
		if got := router.decodeJSONBody(response, request, &value); got != test.ok || (!test.ok && response.Code != test.status) {
			t.Fatalf("body=%q ok=%v status=%d response=%s", test.body, got, response.Code, response.Body.String())
		}
	}

	response := httptest.NewRecorder()
	writeServiceError(response, httptest.NewRequest(http.MethodGet, "/", nil), context.DeadlineExceeded)
	if response.Code != http.StatusGatewayTimeout || !strings.Contains(response.Body.String(), "backend.deadline_exceeded") {
		t.Fatalf("deadline status=%d body=%s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	writeErrorWithParams(response, httptest.NewRequest(http.MethodGet, "/", nil), http.StatusBadRequest, " ", map[string]string{"field_path": "object.key", "resource_type": "object"})
	if !strings.Contains(response.Body.String(), "backend.request_failed") || !strings.Contains(response.Body.String(), "field_path") {
		t.Fatalf("error contract body=%s", response.Body.String())
	}
}

func TestRouterIdentityLifecycleProbeAndMetricsEdges(t *testing.T) {
	router := NewHTTPRouter(HTTPRouterConfig{}, HTTPRouterDependencies{RuntimeInstanceID: " instance "})
	if router.runtimeInstanceID != "instance" {
		t.Fatalf("instance=%q", router.runtimeInstanceID)
	}
	UseServiceIdentity(router, "  ")
	if router.runtimeVersion != "dev" {
		t.Fatalf("blank version changed identity: %q", router.runtimeVersion)
	}
	UseServiceIdentity(router, " v2 ")
	UseRuntimeReleaseIdentity(router, RuntimeReleaseIdentity{ContractVersion: "runtime-release.v1"})
	UseManifest(router, manifestmodel.ManifestSchema{TemplateID: " template ", ManifestHash: " hash "})
	metadata := router.BusinessSystemRuntimeMetadata()
	if metadata.RuntimeVersion != "v2" || metadata.ManifestHash != "hash" {
		t.Fatalf("metadata=%+v", metadata)
	}

	(*HTTPRouter)(nil).MarkStartupComplete()
	(*HTTPRouter)(nil).SetDraining(true)
	(&HTTPRouter{}).MarkStartupComplete()
	(&HTTPRouter{}).SetDraining(true)
	router.MarkStartupComplete()
	router.SetDraining(false)
	router.SetMaintenance(false)

	nilStatus := &HTTPRouter{healthRegistry: healthplatform.NewRegistry(), healthCheckTimeout: time.Millisecond}
	nilStatus.MarkStartupComplete()
	if snapshot := nilStatus.readinessSnapshot(t.Context()); snapshot.Status != "unavailable" {
		t.Fatalf("nil status readiness=%+v", snapshot)
	}

	controller := capacityplatform.NewController(capacityplatform.Limits{GlobalInFlight: 1, WorkspaceInFlight: 2, UseCaseInFlight: 2}, nil)
	lease, decision := controller.Acquire(t.Context(), capacityplatform.Request{WorkspaceID: "one", UseCase: "one"})
	if !decision.Allowed {
		t.Fatalf("first capacity decision=%+v", decision)
	}
	defer lease.Release()
	_, _ = controller.Acquire(t.Context(), capacityplatform.Request{WorkspaceID: "two", UseCase: "two"})
	overloaded := &HTTPRouter{healthRegistry: healthplatform.NewRegistry(), healthCheckTimeout: time.Millisecond, capacityController: controller}
	overloaded.MarkStartupComplete()
	if snapshot := overloaded.readinessSnapshot(t.Context()); snapshot.Status != "unavailable" {
		t.Fatalf("overloaded readiness=%+v", snapshot)
	}

	for _, candidate := range []*HTTPRouter{{}, {httpMetrics: NewMemoryHTTPMetricsCollector(2)}, {technicalMetrics: func(context.Context) string { return "custom_metric 1\n" }}, {capacityController: capacityplatform.NewController(capacityplatform.Limits{}, nil)}} {
		response := httptest.NewRecorder()
		candidate.metrics(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		if response.Code != http.StatusOK || !strings.HasSuffix(response.Body.String(), "# EOF\n") {
			t.Fatalf("metrics status=%d body=%q", response.Code, response.Body.String())
		}
	}
	eventRouter := NewHTTPRouter(HTTPRouterConfig{CapacityLimits: capacityplatform.Limits{GlobalInFlight: 1, WorkspaceInFlight: 2, UseCaseInFlight: 2}}, HTTPRouterDependencies{})
	eventLease, eventDecision := eventRouter.capacityController.Acquire(t.Context(), capacityplatform.Request{WorkspaceID: "one", UseCase: "one"})
	if !eventDecision.Allowed {
		t.Fatalf("event seed decision=%+v", eventDecision)
	}
	defer eventLease.Release()
	if _, eventDecision = eventRouter.capacityController.Acquire(t.Context(), capacityplatform.Request{WorkspaceID: "two", UseCase: "two"}); eventDecision.Allowed {
		t.Fatalf("capacity event was not emitted: %+v", eventDecision)
	}
}

func TestHTTPRouterSurfaceGroupConstructionAndOriginEdges(t *testing.T) {
	originalContracts := runtimeEndpointSurfaceContracts
	func() {
		defer func() {
			runtimeEndpointSurfaceContracts = originalContracts
			if recover() == nil {
				t.Fatal("invalid compiled endpoint contracts did not panic")
			}
		}()
		runtimeEndpointSurfaceContracts = nil
		NewHTTPRouter(HTTPRouterConfig{}, HTTPRouterDependencies{})
	}()

	router := completeRouterForSurfaceGroupTests(HTTPRouterConfig{
		SurfaceGroupPolicies: map[SurfaceRouteGroup]SurfaceRouteGroupPolicy{
			SurfaceRouteGroupPublic:      {RateLimitPerMinute: 1},
			SurfaceRouteGroupTenantAdmin: {},
		},
	})
	if router.surfaceGroupCapacity[SurfaceRouteGroupPublic] == nil {
		t.Fatal("positive listener rate limit did not create a capacity controller")
	}
	if router.surfaceGroupCapacity[SurfaceRouteGroupTenantAdmin] != nil {
		t.Fatal("zero listener rate limit created a capacity controller")
	}

	if handler := router.RoutesForSurfaceGroup(SurfaceRouteGroupAll); handler == nil {
		t.Fatal("all-surface handler is nil")
	}
	unknownResponse := httptest.NewRecorder()
	router.RoutesForSurfaceGroup(SurfaceRouteGroup("unknown")).ServeHTTP(unknownResponse, httptest.NewRequest(http.MethodGet, "/records", nil))
	if unknownResponse.Code != http.StatusNotFound {
		t.Fatalf("unknown group status=%d body=%s", unknownResponse.Code, unknownResponse.Body.String())
	}
	if handler := router.RoutesForSurfaceGroup(SurfaceRouteGroupPublic); handler == nil {
		t.Fatal("metered public handler is nil")
	}

	router.httpMetrics = nil
	malformedRoutes := []string{"invalid", " GET /invalid-method", "GET "}
	for _, route := range malformedRoutes {
		runtimeSurfaceRoutePolicies[route] = nil
	}
	func() {
		defer func() {
			for _, route := range malformedRoutes {
				delete(runtimeSurfaceRoutePolicies, route)
			}
		}()
		if handler := router.RoutesForSurfaceGroup(SurfaceRouteGroupPublic); handler == nil {
			t.Fatal("public handler is nil")
		}
	}()
	if handler := router.RoutesForSurfaceGroup(SurfaceRouteGroupTenantAdmin); handler == nil {
		t.Fatal("tenant-admin handler is nil")
	}

	if SurfaceRouteGroupEndpointCount(SurfaceRouteGroupAll) != len(runtimeSurfaceRoutePolicies) {
		t.Fatal("all-surface endpoint count mismatch")
	}
	if SurfaceRouteGroupEndpointCount(SurfaceRouteGroup("unknown")) != 0 {
		t.Fatal("unknown surface group has endpoints")
	}
}

func TestAuthenticationMiddlewareCompleteDecisionMatrix(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /public", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })
	mux.HandleFunc("GET /records", func(w http.ResponseWriter, r *http.Request) {
		principal, _ := principalFromContext(r)
		w.Header().Set("X-Test-Principal", principal.UserID)
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("GET /auth/me", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("GET /auth/session", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})
	mux.HandleFunc("/{path...}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })

	tests := []struct {
		name      string
		router    *HTTPRouter
		method    string
		path      string
		headers   map[string]string
		want      int
		principal string
	}{
		{name: "options bypass", router: &HTTPRouter{}, method: http.MethodOptions, path: "/records", want: http.StatusNotFound},
		{name: "fallback bypass", router: &HTTPRouter{}, method: http.MethodGet, path: "/missing", want: http.StatusNotFound},
		{name: "api key valid", router: &HTTPRouter{integrationAuth: routerIntegrationAuthStub{principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "integration", WorkspaceID: "workspace-a"}}}}, method: http.MethodGet, path: "/records", headers: map[string]string{"X-API-Key": "key", "X-Workspace-ID": "workspace-a"}, want: http.StatusAccepted, principal: "integration"},
		{name: "api key invalid", router: &HTTPRouter{integrationAuth: routerIntegrationAuthStub{error: errors.New("invalid")}}, method: http.MethodGet, path: "/records", headers: map[string]string{"X-API-Key": "key"}, want: http.StatusUnauthorized},
		{name: "api key limited", router: &HTTPRouter{integrationAuth: routerIntegrationAuthStub{error: &apperror.AppError{Kind: apperror.KindRateLimited, Code: "backend.integration.api_key.rate_limited"}}}, method: http.MethodGet, path: "/records", headers: map[string]string{"X-API-Key": "key"}, want: http.StatusTooManyRequests},
		{name: "api workspace mismatch", router: &HTTPRouter{integrationAuth: routerIntegrationAuthStub{principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}}}, method: http.MethodGet, path: "/records", headers: map[string]string{"X-API-Key": "key", "X-Workspace-ID": "workspace-b"}, want: http.StatusForbidden},
		{name: "bearer valid", router: routerWithIdentitySDK(identitysdk.Principal{Known: true, UserID: "bearer", WorkspaceID: "workspace-a"}), method: http.MethodGet, path: "/records", headers: map[string]string{"Authorization": "Bearer token", "X-Workspace-ID": "workspace-a"}, want: http.StatusAccepted, principal: "bearer"},
		{name: "temporary password denied before handler", router: routerWithIdentitySDK(identitysdk.Principal{Known: true, UserID: "bearer", WorkspaceID: "workspace-a", MustChangePassword: true}), method: http.MethodGet, path: "/records", headers: map[string]string{"Authorization": "Bearer token", "X-Workspace-ID": "workspace-a"}, want: http.StatusForbidden},
		{name: "bearer invalid", router: &HTTPRouter{identityAuthentication: routerIdentityMiddlewareStub{invalid: true}, identityPrincipal: routerWithIdentitySDK(identitysdk.Principal{}).identityPrincipal}, method: http.MethodGet, path: "/records", headers: map[string]string{"Authorization": "Bearer token"}, want: http.StatusUnauthorized},
		{name: "bearer workspace mismatch", router: routerWithIdentitySDK(identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "bearer"}), method: http.MethodGet, path: "/records", headers: map[string]string{"Authorization": "Bearer token", "X-Workspace-ID": "workspace-b"}, want: http.StatusForbidden},
		{name: "dev header allowed", router: &HTTPRouter{allowDevAuthHeaders: true}, method: http.MethodGet, path: "/records", headers: map[string]string{"X-User-ID": "developer"}, want: http.StatusAccepted},
		{name: "dev header disabled", router: routerWithIdentitySDK(identitysdk.Principal{Known: true, UserID: "unused"}), method: http.MethodGet, path: "/records", headers: map[string]string{"X-User-ID": "developer"}, want: http.StatusUnauthorized},
		{name: "token required", router: routerWithIdentitySDK(identitysdk.Principal{Known: true, UserID: "unused"}), method: http.MethodGet, path: "/records", want: http.StatusUnauthorized},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, test.path, nil)
			for key, value := range test.headers {
				request.Header.Set(key, value)
			}
			test.router.withAuth(mux, mux).ServeHTTP(response, request)
			if response.Code != test.want || response.Header().Get("X-Test-Principal") != test.principal {
				t.Fatalf("status=%d principal=%q body=%s", response.Code, response.Header().Get("X-Test-Principal"), response.Body.String())
			}
		})
	}
}

func TestPrincipalProjectionAPIKeyAndDevelopmentMatrix(t *testing.T) {
	request := func(headers map[string]string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/records", nil)
		for key, value := range headers {
			r.Header.Set(key, value)
		}
		return r
	}

	router := &HTTPRouter{integrationAuth: routerIntegrationAuthStub{principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "api-user", WorkspaceID: "default"}}}}
	if principal := router.principalFromRequest(request(map[string]string{"X-API-Key": "key"})); principal.UserID != "api-user" {
		t.Fatalf("api principal=%+v", principal)
	}
	router.integrationAuth = routerIntegrationAuthStub{error: errors.New("invalid")}
	if principal := router.principalFromRequest(request(map[string]string{"X-API-Key": "key"})); principal.Known {
		t.Fatalf("invalid API principal=%+v", principal)
	}

	router = &HTTPRouter{allowDevAuthHeaders: true, identityAuthorization: routerIdentityAuthorizationStub{principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "role-dev"}}}}
	if principal := router.principalFromRequest(request(map[string]string{"X-User-ID": "dev", "X-Role": "admin"})); principal.UserID != "role-dev" {
		t.Fatalf("role dev principal=%+v", principal)
	}
	router = &HTTPRouter{allowDevAuthHeaders: true, identityAuthorization: routerIdentityAuthorizationStub{principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "resolved-dev"}}}}
	if principal := router.principalFromRequest(request(map[string]string{"X-User-ID": "dev"})); principal.UserID != "resolved-dev" {
		t.Fatalf("resolved dev principal=%+v", principal)
	}
	router.identityAuthorization = routerIdentityAuthorizationStub{principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: false, UserID: "unknown-dev"}}, error: errors.New("unknown")}
	if principal := router.principalFromRequest(request(map[string]string{"X-User-ID": "dev"})); principal.Known {
		t.Fatalf("unknown dev principal=%+v", principal)
	}
}

func TestActorAuditAndRecoveryRemainingEdges(t *testing.T) {
	router := &HTTPRouter{}
	request := requestWithSDKIdentity(httptest.NewRequest(http.MethodGet, "/records", nil), identitysdk.Principal{Known: true, UserID: "subject"})
	if actor := router.actorIDFromRequest(request); actor != "subject" {
		t.Fatalf("actor=%q", actor)
	}
	if actor := router.actorIDFromRequest(httptest.NewRequest(http.MethodGet, "/records", nil)); actor != "" {
		t.Fatalf("invalid-token actor=%q", actor)
	}

	audit := &routerAuditStub{}
	router = &HTTPRouter{securityAudit: audit}
	router.appendSecurityAuditForPrincipal(httptest.NewRequest(http.MethodPost, "/records", nil), principalmodel.Principal{}, "event", "summary", nil)
	if len(audit.events) != 1 || audit.metadata[0]["path"] != "/records" || audit.metadata[0]["method"] != http.MethodPost {
		t.Fatalf("audit events=%v metadata=%v", audit.events, audit.metadata)
	}
	(&HTTPRouter{}).appendSecurityAuditForPrincipal(httptest.NewRequest(http.MethodGet, "/", nil), principalmodel.Principal{}, "ignored", "ignored", nil)

	deferred := func() (recovered any) {
		defer func() { recovered = recover() }()
		(&HTTPRouter{}).withRecovery(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
		return nil
	}()
	if deferred != http.ErrAbortHandler {
		t.Fatalf("abort recovery=%v", deferred)
	}
}

func TestOptionalRouteRegistrationAndHealthAuthorization(t *testing.T) {
	runOptionalRouteRegistrar(false, func() { t.Fatal("disabled registrar called") })
	called := false
	runOptionalRouteRegistrar(true, func() { called = true })
	if !called {
		t.Fatal("optional HTTP registrar was not called")
	}

	router := &HTTPRouter{}
	for _, principal := range []principalmodel.Principal{principalmodel.Principal{Principal: identitysdk.Principal{Known: false}}, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, RoleKey: "viewer"}}} {
		response := httptest.NewRecorder()
		request := requestWithPrincipal(httptest.NewRequest(http.MethodGet, "/health", nil), principal)
		router.health(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("principal=%+v status=%d body=%s", principal, response.Code, response.Body.String())
		}
	}
	operator := routerTestPrincipal(identitysdk.Principal{Known: true, RoleKey: "operator"}, "runtime_ops.capability_status.read")
	if !healthAllowed(operator) {
		t.Fatal("runtime operator health permissions were rejected")
	}
}

func TestReadinessStartupMaintenanceWorkerAndManifestObjectEdges(t *testing.T) {
	router := &HTTPRouter{healthRegistry: healthplatform.NewRegistry(), healthCheckTimeout: time.Millisecond}
	if snapshot := router.readinessSnapshot(t.Context()); snapshot.Status != "unavailable" {
		t.Fatalf("startup-incomplete readiness=%+v", snapshot)
	}
	router.MarkStartupComplete()
	router.SetMaintenance(true)
	if snapshot := router.readinessSnapshot(t.Context()); snapshot.Status != "unavailable" {
		t.Fatalf("maintenance readiness=%+v", snapshot)
	}
	router.SetMaintenance(false)
	worker := workerplatform.NewController()
	worker.Drain()
	router.workerControl = worker
	if snapshot := router.readinessSnapshot(t.Context()); snapshot.Status != "unavailable" {
		t.Fatalf("worker-drain readiness=%+v", snapshot)
	}

	startupRouter := &HTTPRouter{healthRegistry: healthplatform.NewRegistry(), healthCheckTimeout: time.Millisecond, manifest: manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "customer"}}}}
	startupRouter.MarkStartupComplete()
	response := httptest.NewRecorder()
	startupRouter.startup(response, httptest.NewRequest(http.MethodGet, "/startup", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("object manifest startup status=%d body=%s", response.Code, response.Body.String())
	}
	(&HTTPRouter{}).SetMaintenance(true)
}

func TestAdmissionAndCapacityHelperRemainingEdges(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	response := httptest.NewRecorder()
	(&HTTPRouter{}).withAdmission(nil, next).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/records", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("nil capacity status=%d", response.Code)
	}

	router := &HTTPRouter{capacityController: capacityplatform.NewController(capacityplatform.Limits{}, nil)}
	response = httptest.NewRecorder()
	router.withAdmission(nil, next).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("probe capacity status=%d", response.Code)
	}

	router.backpressure = func(context.Context) bool { return false }
	blankPrincipalRequest := requestWithPrincipal(httptest.NewRequest(http.MethodGet, "/", nil), principalmodel.Principal{Principal: identitysdk.Principal{Known: true}})
	response = httptest.NewRecorder()
	router.withAdmission(nil, next).ServeHTTP(response, blankPrincipalRequest)
	if response.Code != http.StatusNoContent {
		t.Fatalf("blank-workspace admission status=%d", response.Code)
	}

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	response = httptest.NewRecorder()
	router.withAdmission(nil, next).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/records", nil).WithContext(cancelled))
	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "1" {
		t.Fatalf("cancelled admission status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}

	workspaceController := capacityplatform.NewController(capacityplatform.Limits{GlobalInFlight: 10, WorkspaceInFlight: 1, UseCaseInFlight: 10}, nil)
	lease, decision := workspaceController.Acquire(t.Context(), capacityplatform.Request{WorkspaceID: "workspace", UseCase: "first"})
	if !decision.Allowed {
		t.Fatalf("seed decision=%+v", decision)
	}
	defer lease.Release()
	workspaceRouter := &HTTPRouter{capacityController: workspaceController}
	workspaceRequest := requestWithPrincipal(httptest.NewRequest(http.MethodGet, "/records", nil), principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace"}})
	response = httptest.NewRecorder()
	workspaceRouter.withAdmission(nil, next).ServeHTTP(response, workspaceRequest)
	if response.Code != http.StatusTooManyRequests || response.Header().Get("X-Capacity-Dimension") != "workspace" {
		t.Fatalf("workspace admission status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}

	if capacityUseCase(httptest.NewRequest(http.MethodGet, "/", nil), "") != "get:root" {
		t.Fatal("empty use case was not normalized")
	}
	if !capacityRetryRequest("/jobs/replay") || !capacityRetryRequest("/jobs/retry") || capacityRetryRequest("/records") {
		t.Fatal("retry classification mismatch")
	}
}

func TestMetricsNormalizationSortingAndBounds(t *testing.T) {
	collector := NewMemoryHTTPMetricsCollector(0)
	collector.Begin()
	collector.Observe("POST", "/z", http.StatusOK, time.Millisecond)
	collector.Begin()
	collector.Observe("GET", "/b", http.StatusInternalServerError, time.Millisecond)
	collector.Begin()
	collector.Observe("GET", "/a", http.StatusCreated, time.Millisecond)
	collector.Begin()
	collector.Observe("GET", "/a", http.StatusBadRequest, time.Millisecond)
	snapshot := collector.Snapshot()
	if len(snapshot) != 4 || snapshot[0].Method != "GET" || snapshot[0].Route != "/a" || snapshot[0].StatusClass != "2xx" {
		t.Fatalf("sorted snapshot=%+v", snapshot)
	}

	if normalizeMetricRoute("") != "unmatched" || normalizeMetricRoute("missing-slash") != "unmatched" || normalizeMetricRoute("/"+strings.Repeat("x", 256)) != "unmatched" {
		t.Fatal("route bounds mismatch")
	}
	for value, want := range map[string]string{"": "fallback", strings.Repeat("a", 65): "fallback", "UPPER": "fallback", "digit1": "fallback", "valid_label": "valid_label"} {
		if got := normalizeMetricLabel(value, "fallback"); got != want {
			t.Fatalf("label %q=%q want=%q", value, got, want)
		}
	}
	if statusClass(99) != "unknown" || statusClass(600) != "unknown" || statusClass(http.StatusNoContent) != "2xx" {
		t.Fatal("status class bounds mismatch")
	}
	if got := normalizedCorrelationValue("ABC"); got != "ABC" {
		t.Fatalf("bounded uppercase=%q", got)
	}
	if got := normalizedCorrelationValue("123"); got != "123" {
		t.Fatalf("bounded digits=%q", got)
	}
	if normalizedCorrelationValue("{") != "" {
		t.Fatal("punctuation correlation value accepted")
	}
	if normalizeMetricLabel("{", "fallback") != "fallback" {
		t.Fatal("punctuation metric label accepted")
	}
}

func TestCompleteRouterCompositionSmokeAndCallbacks(t *testing.T) {
	router := NewHTTPRouter(HTTPRouterConfig{AllowDevAuthHeaders: true}, HTTPRouterDependencies{IdentityAuthorization: routerIdentityAuthorizationStub{principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "developer", WorkspaceID: "default"}}}})
	returned := UseHandlers(router, HTTPRouterHandlers{})
	if returned != router {
		t.Fatal("UseHandlers did not return the configured router")
	}
	calls := 0
	registrar := routerCountingRegistrar{calls: &calls}
	router.recordHTTP, router.surfaceContextHTTP, router.uploadHTTP, router.discoveryHTTP, router.openAPIHTTP = registrar, registrar, registrar, registrar, registrar
	router.workflowHTTP, router.automationHTTP, router.schedulerHTTP, router.reportHTTP, router.frontendCapabilityHTTP = registrar, registrar, registrar, registrar, registrar
	router.businessReferenceHTTP, router.businessSystemHTTP, router.capabilityHTTP, router.changePlanHTTP, router.integrationHTTP = registrar, registrar, registrar, registrar, registrar
	router.applicationSchemaHTTP, router.notificationHTTP, router.agentDialogHTTP, router.operationsHTTP = registrar, registrar, registrar, registrar
	handler := router.Routes()
	if calls != 19 {
		t.Fatalf("registrar calls=%d", calls)
	}
	for _, test := range []struct {
		path   string
		status int
	}{{"/", http.StatusOK}, {"/live", http.StatusOK}, {"/missing", http.StatusNotFound}} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		handler.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Fatalf("path=%s status=%d body=%s", test.path, response.Code, response.Body.String())
		}
	}

	callbacks := router.HandlerCallbacks()
	if callbacks.Principal == nil || callbacks.WriteJSON == nil || callbacks.WriteError == nil || callbacks.WriteServiceError == nil || callbacks.DecodeJSON == nil || callbacks.SecurityAudit == nil || callbacks.SecurityAuditForPrincipal == nil || callbacks.ProvisionRequired == nil || callbacks.Locale == nil {
		t.Fatalf("incomplete callbacks=%+v", callbacks)
	}
}

func TestAuthorizedHealthAndAuditPrincipalSources(t *testing.T) {
	releaseIdentity := RuntimeReleaseIdentity{ContractVersion: "domainry-runtime-release-identity-v1", BuildMode: "packaged", CombinationSHA256: strings.Repeat("a", 64)}
	router := &HTTPRouter{runtimeStatus: routerRuntimeStatusStub{}, healthRegistry: healthplatform.NewRegistry(), healthCheckTimeout: time.Second, httpMetrics: NewMemoryHTTPMetricsCollector(2), capacityController: capacityplatform.NewController(capacityplatform.Limits{}, nil), releaseIdentity: releaseIdentity}
	router.MarkStartupComplete()
	admin := routerTestPrincipal(identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "workspace"}, "workspace.admin")
	response := httptest.NewRecorder()
	router.health(response, requestWithPrincipal(httptest.NewRequest(http.MethodGet, "/health", nil), admin))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "api_contract_hash") || !strings.Contains(response.Body.String(), "\"ready\":true") || !strings.Contains(response.Body.String(), "\"release_identity\"") || !strings.Contains(response.Body.String(), releaseIdentity.CombinationSHA256) {
		t.Fatalf("health status=%d body=%s", response.Code, response.Body.String())
	}
	ready := httptest.NewRecorder()
	router.ready(ready, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if ready.Code != http.StatusOK || ready.Body.String() != "{\"status\":\"ok\"}\n" || strings.Contains(ready.Body.String(), "release_identity") {
		t.Fatalf("ready status=%d body=%s", ready.Code, ready.Body.String())
	}

	router.integrationAuth = routerIntegrationAuthStub{principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "api-auditor", WorkspaceID: "default"}}}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-API-Key", "key")
	if principal := router.auditPrincipalFromRequest(request); principal.UserID != "api-auditor" {
		t.Fatalf("API audit principal=%+v", principal)
	}
	router.integrationAuth = routerIntegrationAuthStub{error: errors.New("invalid")}
	request = requestWithPrincipal(httptest.NewRequest(http.MethodGet, "/", nil), principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "bearer-auditor", WorkspaceID: "workspace"}})
	request.Header.Set("X-Workspace-ID", "workspace")
	if principal := router.auditPrincipalFromRequest(request); principal.UserID != "bearer-auditor" || principal.WorkspaceID != "workspace" {
		t.Fatalf("bearer audit principal=%+v", principal)
	}
	router.allowDevAuthHeaders = true
	router.identityAuthorization = routerIdentityAuthorizationStub{principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "dev-auditor"}}}
	request = httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-User-ID", "dev")
	if principal := router.auditPrincipalFromRequest(request); principal.UserID != "dev-auditor" {
		t.Fatalf("dev audit principal=%+v", principal)
	}
}

func TestLastRouterConditionEdges(t *testing.T) {
	router := &HTTPRouter{healthRegistry: healthplatform.NewRegistry(), runtimeInstanceID: "runtime"}
	router.operationsControlState = func(_ context.Context, kind, _ string) (bool, bool, error) {
		if kind == "maintenance" {
			return false, false, errors.New("maintenance unavailable")
		}
		return true, false, nil
	}
	router.refreshOperationalState(t.Context())
	if !router.healthRegistry.Draining() || router.healthRegistry.Maintenance() {
		t.Fatalf("refresh state maintenance=%v draining=%v", router.healthRegistry.Maintenance(), router.healthRegistry.Draining())
	}

	configured := UseHandlers(&HTTPRouter{}, HTTPRouterHandlers{Notifications: &notificationhttp.NotificationsHandler{}, AgentDialog: &agentdialoghttp.AgentDialogHandler{}})
	if configured.notificationHTTP == nil || configured.agentDialogHTTP == nil {
		t.Fatal("optional handlers were not retained")
	}

	nilMetrics := &HTTPRouter{}
	nilMetrics.observeHTTPRequest(httptest.NewRequest(http.MethodGet, "/", nil), "/", http.StatusOK, 0)
	response := httptest.NewRecorder()
	nilMetrics.withMetrics(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("nil-metrics status=%d", response.Code)
	}
	if normalizedCorrelationValue(strings.Repeat("x", 129)) != "" {
		t.Fatal("overlong correlation value accepted")
	}
	if requestLocale(httptest.NewRequest(http.MethodGet, "/", nil)) != "en-US" {
		t.Fatal("empty locale did not use default")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /records", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	response = httptest.NewRecorder()
	missingDevHeaderRouter := routerWithIdentitySDK(identitysdk.Principal{Known: true, UserID: "unused"})
	missingDevHeaderRouter.allowDevAuthHeaders = true
	missingDevHeaderRouter.withAuth(mux, mux).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/records", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing dev header status=%d", response.Code)
	}

	response = httptest.NewRecorder()
	oversized := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{}"))
	if (&HTTPRouter{maxJSONBodyBytes: 1}).decodeJSONBody(response, oversized, &map[string]any{}) || response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status=%d body=%s", response.Code, response.Body.String())
	}

	devRouter := &HTTPRouter{allowDevAuthHeaders: true, identityAuthorization: routerIdentityAuthorizationStub{principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user-role"}}}}
	devRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	devRequest.Header.Set("X-User-ID", "dev")
	devRequest.Header.Set("X-User-Role", "viewer")
	if principal := devRouter.principalFromRequest(devRequest); principal.UserID != "user-role" {
		t.Fatalf("user-role principal=%+v", principal)
	}
	devRouter = &HTTPRouter{allowDevAuthHeaders: true, identityAuthorization: routerIdentityAuthorizationStub{principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: false}}}}
	unknownDevRequest := httptest.NewRequest(http.MethodGet, "/", nil)
	unknownDevRequest.Header.Set("X-User-ID", "dev")
	if principal := devRouter.principalFromRequest(unknownDevRequest); principal.Known {
		t.Fatalf("empty dev principal=%+v", principal)
	}

	auditRouter := &HTTPRouter{}
	auditRequest := requestWithPrincipal(httptest.NewRequest(http.MethodGet, "/", nil), principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "default-workspace", WorkspaceID: "default"}})
	if principal := auditRouter.auditPrincipalFromRequest(auditRequest); principal.WorkspaceID != "default" {
		t.Fatalf("default audit principal=%+v", principal)
	}

	failing := &routerFailingResponseWriter{header: http.Header{}}
	writeJSON(failing, http.StatusOK, map[string]any{"ok": true})
}

func TestHTTPPrincipalBusinessProfileSelectionIsServerResolvedAndFailClosed(t *testing.T) {
	resolved := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user", WorkspaceID: "default"}, SurfaceKey: "portal", BusinessClaims: map[string]profilebindingmodel.ClaimValue{"member_no": {Type: "text", Value: "M-1"}}}
	business := &routerBusinessPrincipalStub{principal: resolved}
	router := &HTTPRouter{businessPrincipal: business}
	request := requestWithPrincipal(httptest.NewRequest(http.MethodGet, "/", nil), principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user", WorkspaceID: "default"}})
	request.Header.Set("X-Surface-Key", "portal")
	request.Header.Set("X-Business-Profile-Key", "member")
	request.Header.Set("X-Business-Profile-ID", "member-1")
	if principal := router.principalFromRequest(request); !principal.Known || principal.BusinessClaims["member_no"].Value != "M-1" {
		t.Fatalf("resolved business principal=%#v", principal)
	}
	if business.selection != [3]string{"portal", "member", "member-1"} {
		t.Fatalf("selection headers=%#v", business.selection)
	}

	audit := &routerAuditStub{}
	business.error = errors.New("selection denied")
	router.securityAudit = audit
	denied := router.principalFromRequest(request)
	if denied.Known || denied.BusinessClaims != nil || len(audit.events) != 1 || audit.events[0] != "business_profile_selection_denied" {
		t.Fatalf("denied principal=%#v audit=%#v", denied, audit.events)
	}
}
