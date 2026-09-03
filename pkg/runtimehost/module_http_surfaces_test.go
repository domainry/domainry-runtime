package runtimehost

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulehttp"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
)

type moduleSurfaceStub struct {
	owner, name string
	routes      []modulehttp.Route
	handler     http.Handler
}

func (moduleSurfaceStub) ContractVersion() string            { return modulehttp.ContractVersion }
func (surface moduleSurfaceStub) Owner() string              { return surface.owner }
func (surface moduleSurfaceStub) Name() string               { return surface.name }
func (surface moduleSurfaceStub) Routes() []modulehttp.Route { return surface.routes }
func (surface moduleSurfaceStub) Handler() http.Handler      { return surface.handler }

func TestModuleHTTPSurfacesMountByExposureAndPreserveFallback(t *testing.T) {
	sample := moduleSurfaceStub{owner: "sample", name: "management", handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/sample" {
			t.Fatalf("module-local path=%q", request.URL.Path)
		}
		writer.WriteHeader(http.StatusNoContent)
	}), routes: []modulehttp.Route{{Action: runtimeHostTestAction("sample.read", "GET /sample", []actioncontract.Exposure{actioncontract.ExposureTenantAdmin}, actioncontract.AuthorizationAuthenticated)}}}
	fallback := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusTeapot) })
	passthrough := func(_ modulehttp.Route, handler http.Handler) (http.Handler, error) { return handler, nil }
	admin, err := mountModuleHTTPSurfaces(runtimehttp.ListenerRouteGroupTenantAdmin, []modulehttp.Surface{sample}, fallback, passthrough)
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
	admin.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/runtime", nil))
	if response.Code != http.StatusTeapot {
		t.Fatalf("fallback status=%d", response.Code)
	}
	public, err := mountModuleHTTPSurfaces(runtimehttp.ListenerRouteGroupPublic, []modulehttp.Surface{sample}, fallback, passthrough)
	if err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	public.ServeHTTP(response, request)
	if response.Code != http.StatusTeapot {
		t.Fatalf("public status=%d", response.Code)
	}
}

func TestModuleHTTPSurfacesRejectCrossModuleRouteCollision(t *testing.T) {
	route := modulehttp.Route{Action: runtimeHostTestAction("test.shared.get", "GET /shared", []actioncontract.Exposure{actioncontract.ExposureTenantAdmin}, actioncontract.AuthorizationAuthenticated)}
	first := moduleSurfaceStub{owner: "alpha", name: "management", handler: http.NotFoundHandler(), routes: []modulehttp.Route{route}}
	second := moduleSurfaceStub{owner: "notification", name: "management", handler: http.NotFoundHandler(), routes: []modulehttp.Route{route}}
	_, err := mountModuleHTTPSurfaces(runtimehttp.ListenerRouteGroupTenantAdmin, []modulehttp.Surface{first, second}, nil, func(_ modulehttp.Route, handler http.Handler) (http.Handler, error) { return handler, nil })
	if err == nil || !strings.Contains(err.Error(), "owned by both") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestModuleHTTPSurfacesRejectAuthorizedRouteWithoutHostGuard(t *testing.T) {
	sample := moduleSurfaceStub{owner: "sample", name: "management", handler: http.NotFoundHandler(), routes: []modulehttp.Route{{Action: runtimeHostTestAction("sample.read", "GET /sample", []actioncontract.Exposure{actioncontract.ExposureTenantAdmin}, actioncontract.AuthorizationAuthenticated)}}}
	_, err := mountModuleHTTPSurfaces(runtimehttp.ListenerRouteGroupTenantAdmin, []modulehttp.Surface{sample}, nil)
	if err == nil || !strings.Contains(err.Error(), "requires a host authorization guard") {
		t.Fatalf("unexpected error: %v", err)
	}
}

var _ modulehttp.Surface = moduleSurfaceStub{}

func runtimeHostTestAction(key, pattern string, exposures []actioncontract.Exposure, strategy actioncontract.AuthorizationStrategy) actioncontract.ActionDefinition {
	method, path, _ := strings.Cut(pattern, " ")
	separator := strings.LastIndex(key, ".")
	action := actioncontract.ActionDefinition{
		Key: key, Owner: "module:test", SourceKind: "module_surface", CapabilityKey: "test.product", CapabilityLabel: "Test",
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
