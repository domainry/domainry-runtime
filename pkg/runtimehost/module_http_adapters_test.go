package runtimehost

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulehttp"
	"github.com/domainry/domainry-foundation/requestcontext"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
)

type moduleAdapterStub struct {
	owner, name string
	routes      []modulehttp.Route
	handler     http.Handler
}

func TestModuleAdapterRouterPublishesRequestContextAndResponseHeaders(t *testing.T) {
	var observedRequestID, observedCorrelationID string
	sample := moduleAdapterStub{owner: "report", name: "business", handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		observedRequestID = requestcontext.RequestID(request.Context())
		observedCorrelationID = requestcontext.CorrelationID(request.Context())
		writer.WriteHeader(http.StatusNoContent)
	}), routes: []modulehttp.Route{{Action: runtimeHostTestAction("report.query", "POST /report/{reportKey}/query", []actioncontract.Exposure{actioncontract.ExposurePublic}, actioncontract.AuthorizationAnonymous)}}}
	router := newModuleAdapterRouter(runtimehttp.ListenerRouteGroupPublic, http.NotFoundHandler())
	if err := router.Bind([]modulehttp.Adapter{sample}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/report/weekly/query", nil)
	request.Header.Set("X-Request-ID", "request-module-1")
	request.Header.Set("X-Correlation-ID", "correlation-module-1")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if observedRequestID != "request-module-1" || observedCorrelationID != "correlation-module-1" {
		t.Fatalf("module context request=%q correlation=%q", observedRequestID, observedCorrelationID)
	}
	if response.Header().Get("X-Request-ID") != observedRequestID || response.Header().Get("X-Correlation-ID") != observedCorrelationID {
		t.Fatalf("module response headers=%v", response.Header())
	}

	request = httptest.NewRequest(http.MethodPost, "/report/weekly/query", nil)
	request.Header.Set("X-Request-ID", "invalid request id")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if observedRequestID == "" || observedRequestID == "invalid request id" || observedCorrelationID != observedRequestID {
		t.Fatalf("generated module context request=%q correlation=%q", observedRequestID, observedCorrelationID)
	}
	if response.Header().Get("X-Request-ID") != observedRequestID || response.Header().Get("X-Correlation-ID") != observedCorrelationID {
		t.Fatalf("generated module response headers=%v", response.Header())
	}

	protected := moduleAdapterStub{owner: "sample", name: "management", handler: sample.handler, routes: []modulehttp.Route{{Action: runtimeHostTestAction("sample.read", "GET /sample", []actioncontract.Exposure{actioncontract.ExposureManagement}, actioncontract.AuthorizationAuthenticated)}}}
	protectedRouter := newModuleAdapterRouter(runtimehttp.ListenerRouteGroupManagement, http.NotFoundHandler())
	if err := protectedRouter.Bind([]modulehttp.Adapter{protected}, func(_ modulehttp.Route, handler http.Handler, _ modulehttp.AuditRecorder) (http.Handler, error) {
		return handler, nil
	}); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest(http.MethodGet, "/sample", nil)
	request.Header.Set("X-Request-ID", "request-protected-1")
	response = httptest.NewRecorder()
	protectedRouter.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || observedRequestID != "request-protected-1" || response.Header().Get("X-Request-ID") != "request-protected-1" {
		t.Fatalf("protected module status=%d context=%q headers=%v", response.Code, observedRequestID, response.Header())
	}
}

func (moduleAdapterStub) ContractVersion() string            { return modulehttp.ContractVersion }
func (adapter moduleAdapterStub) Owner() string              { return adapter.owner }
func (adapter moduleAdapterStub) Name() string               { return adapter.name }
func (adapter moduleAdapterStub) Routes() []modulehttp.Route { return adapter.routes }
func (adapter moduleAdapterStub) Handler() http.Handler      { return adapter.handler }

func TestModuleHTTPAdaptersMountByExposureAndPreserveFallback(t *testing.T) {
	sample := moduleAdapterStub{owner: "sample", name: "management", handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/sample" {
			t.Fatalf("module-local path=%q", request.URL.Path)
		}
		writer.WriteHeader(http.StatusNoContent)
	}), routes: []modulehttp.Route{{Action: runtimeHostTestAction("sample.read", "GET /sample", []actioncontract.Exposure{actioncontract.ExposureManagement}, actioncontract.AuthorizationAuthenticated)}}}
	fallback := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusTeapot) })
	passthrough := func(_ modulehttp.Route, handler http.Handler, _ modulehttp.AuditRecorder) (http.Handler, error) {
		return handler, nil
	}
	admin, err := mountModuleHTTPAdapters(runtimehttp.ListenerRouteGroupManagement, []modulehttp.Adapter{sample}, fallback, passthrough)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/sample", nil)
	response := httptest.NewRecorder()
	admin.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("sample status=%d", response.Code)
	}
	response = httptest.NewRecorder()
	admin.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/orders", nil))
	if response.Code != http.StatusTeapot {
		t.Fatalf("fallback status=%d", response.Code)
	}
	public, err := mountModuleHTTPAdapters(runtimehttp.ListenerRouteGroupPublic, []modulehttp.Adapter{sample}, fallback, passthrough)
	if err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	public.ServeHTTP(response, request)
	if response.Code != http.StatusTeapot {
		t.Fatalf("public status=%d", response.Code)
	}
}

func TestModuleHTTPAdaptersRejectCrossModuleNamespaceLeak(t *testing.T) {
	route := modulehttp.Route{Action: runtimeHostTestAction("test.shared.get", "GET /shared", []actioncontract.Exposure{actioncontract.ExposureManagement}, actioncontract.AuthorizationAuthenticated)}
	first := moduleAdapterStub{owner: "alpha", name: "management", handler: http.NotFoundHandler(), routes: []modulehttp.Route{route}}
	second := moduleAdapterStub{owner: "notification", name: "management", handler: http.NotFoundHandler(), routes: []modulehttp.Route{route}}
	_, err := mountModuleHTTPAdapters(runtimehttp.ListenerRouteGroupManagement, []modulehttp.Adapter{first, second}, nil, func(_ modulehttp.Route, handler http.Handler, _ modulehttp.AuditRecorder) (http.Handler, error) {
		return handler, nil
	})
	if err == nil || !strings.Contains(err.Error(), `must be rooted at "/alpha"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestModuleHTTPAdaptersRejectAuthorizedRouteWithoutHostGuard(t *testing.T) {
	sample := moduleAdapterStub{owner: "sample", name: "management", handler: http.NotFoundHandler(), routes: []modulehttp.Route{{Action: runtimeHostTestAction("sample.read", "GET /sample", []actioncontract.Exposure{actioncontract.ExposureManagement}, actioncontract.AuthorizationAuthenticated)}}}
	_, err := mountModuleHTTPAdapters(runtimehttp.ListenerRouteGroupManagement, []modulehttp.Adapter{sample}, nil)
	if err == nil || !strings.Contains(err.Error(), "requires a host authorization guard") {
		t.Fatalf("unexpected error: %v", err)
	}
}

var _ modulehttp.Adapter = moduleAdapterStub{}

func runtimeHostTestAction(key, pattern string, exposures []actioncontract.Exposure, strategy actioncontract.AuthorizationStrategy) actioncontract.ActionDefinition {
	method, path, _ := strings.Cut(pattern, " ")
	separator := strings.LastIndex(key, ".")
	action := actioncontract.ActionDefinition{
		Key: key, Owner: "module:test", SourceKind: "module_http", CapabilityKey: "test.product", CapabilityLabel: "Test",
		OperationKey: key[separator+1:], OperationLabel: key, Label: key, Exposures: exposures,
		Authorization: actioncontract.Authorization{Strategy: strategy}, HTTP: &actioncontract.HTTPBinding{Method: method, RouteTemplate: path},
		EffectClass: actioncontract.EffectRead, RiskLevel: actioncontract.RiskLow, IdempotencyDecision: "not_applicable", AuditClass: "test_audit", LifecycleStatus: actioncontract.LifecycleActive,
	}
	if strategy == actioncontract.AuthorizationAuthenticated {
		action.Permission = &actioncontract.PermissionDefinition{Key: key, Owner: action.Owner, ResourceKey: key[:separator], OperationKey: key[separator+1:], Label: key, Category: "Test", LifecycleStatus: actioncontract.LifecycleActive}
	} else if strategy == actioncontract.AuthorizationSigned {
		action.Authorization.PolicyKey = "test.policy"
	}
	return action
}

func TestModuleAdapterRouterAppliesRuntimeCORSToModuleRoutes(t *testing.T) {
	// Module routes (Identity /auth, Report /report) are served by the host
	// router ahead of the Runtime router, so the Runtime's CORS middleware never
	// sees them; the host applies the same policy or a browser cannot read them.
	sample := moduleAdapterStub{owner: "report", name: "business", handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}), routes: []modulehttp.Route{{Action: runtimeHostTestAction("report.query", "POST /report/{reportKey}/query", []actioncontract.Exposure{actioncontract.ExposurePublic}, actioncontract.AuthorizationAnonymous)}}}
	fallback := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusTeapot) })
	router := newModuleAdapterRouter(runtimehttp.ListenerRouteGroupPublic, fallback)
	router.corsOrigins = []string{"*"}
	if err := router.Bind([]modulehttp.Adapter{sample}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/report/weekly/query", strings.NewReader("{}"))
	request.Header.Set("Origin", "http://127.0.0.1:4473")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Access-Control-Allow-Origin") != "http://127.0.0.1:4473" || response.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("module route must carry the Runtime CORS policy: status=%d headers=%v", response.Code, response.Header())
	}
	preflight := httptest.NewRequest(http.MethodOptions, "/report/weekly/query", nil)
	preflight.Header.Set("Origin", "http://127.0.0.1:4473")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, preflight)
	if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Origin") != "http://127.0.0.1:4473" || response.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("preflight status=%d headers=%v", response.Code, response.Header())
	}
	bare := newModuleAdapterRouter(runtimehttp.ListenerRouteGroupPublic, fallback)
	if err := bare.Bind([]modulehttp.Adapter{sample}); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	bare.ServeHTTP(response, request)
	if response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("without a configured policy no CORS header is invented: %v", response.Header())
	}
}
