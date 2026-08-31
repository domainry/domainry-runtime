package runtimehost

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	party := moduleSurfaceStub{owner: "party", name: "management", handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/party" {
			t.Fatalf("module-local path=%q", request.URL.Path)
		}
		writer.WriteHeader(http.StatusNoContent)
	}), routes: []modulehttp.Route{{Pattern: "GET /party", Exposures: []modulehttp.Exposure{modulehttp.ExposureTenantAdmin}, Authentication: modulehttp.AuthenticationAuthenticated, Permission: "party.read"}}}
	fallback := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusTeapot) })
	passthrough := func(_ modulehttp.Route, handler http.Handler) (http.Handler, error) { return handler, nil }
	admin, err := mountModuleHTTPSurfaces(runtimehttp.SurfaceRouteGroupTenantAdmin, []modulehttp.Surface{party}, fallback, passthrough)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/party", nil)
	response := httptest.NewRecorder()
	admin.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("party status=%d", response.Code)
	}
	response = httptest.NewRecorder()
	admin.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/runtime", nil))
	if response.Code != http.StatusTeapot {
		t.Fatalf("fallback status=%d", response.Code)
	}
	public, err := mountModuleHTTPSurfaces(runtimehttp.SurfaceRouteGroupPublic, []modulehttp.Surface{party}, fallback, passthrough)
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
	route := modulehttp.Route{Pattern: "GET /shared", Exposures: []modulehttp.Exposure{modulehttp.ExposureTenantAdmin}, Authentication: modulehttp.AuthenticationAuthenticated, PrincipalOnly: true}
	first := moduleSurfaceStub{owner: "party", name: "management", handler: http.NotFoundHandler(), routes: []modulehttp.Route{route}}
	second := moduleSurfaceStub{owner: "notification", name: "management", handler: http.NotFoundHandler(), routes: []modulehttp.Route{route}}
	_, err := mountModuleHTTPSurfaces(runtimehttp.SurfaceRouteGroupTenantAdmin, []modulehttp.Surface{first, second}, nil, func(_ modulehttp.Route, handler http.Handler) (http.Handler, error) { return handler, nil })
	if err == nil || !strings.Contains(err.Error(), "owned by both") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestModuleHTTPSurfacesRejectAuthorizedRouteWithoutHostGuard(t *testing.T) {
	party := moduleSurfaceStub{owner: "party", name: "management", handler: http.NotFoundHandler(), routes: []modulehttp.Route{{Pattern: "GET /party", Exposures: []modulehttp.Exposure{modulehttp.ExposureTenantAdmin}, Authentication: modulehttp.AuthenticationAuthenticated, Permission: "party.read"}}}
	_, err := mountModuleHTTPSurfaces(runtimehttp.SurfaceRouteGroupTenantAdmin, []modulehttp.Surface{party}, nil)
	if err == nil || !strings.Contains(err.Error(), "requires a host authorization guard") {
		t.Fatalf("unexpected error: %v", err)
	}
}

var _ modulehttp.Surface = moduleSurfaceStub{}
