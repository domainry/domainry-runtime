package runtimehost

import (
	"net/http"
	"net/http/httptest"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityhttpapi "github.com/domainry/domainry-identity-sdk/httpapi"
	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
)

type integrationHTTPSurface struct {
	name    string
	routes  []identityhttpapi.Route
	handler http.Handler
}

func (integrationHTTPSurface) ContractVersion() string { return identityhttpapi.ContractVersion }
func (surface integrationHTTPSurface) Name() string    { return surface.name }
func (surface integrationHTTPSurface) Routes() []identityhttpapi.Route {
	return append([]identityhttpapi.Route(nil), surface.routes...)
}
func (surface integrationHTTPSurface) Handler() http.Handler { return surface.handler }

type moduleBindingStub struct {
	runtimetestkit.IdentityBindingStub
	surfaces []identityhttpapi.Surface
}

func (moduleBindingStub) Descriptor() identitysdk.Descriptor {
	return identitysdk.Descriptor{Mode: identitysdk.DeploymentModeModule}
}
func (binding moduleBindingStub) HTTPSurfaces() []identityhttpapi.Surface {
	return append([]identityhttpapi.Surface(nil), binding.surfaces...)
}

func TestProjectHostMountsModuleIdentityHTTPOutsideRuntime(t *testing.T) {
	identityHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Owner", "identity")
		w.WriteHeader(http.StatusNoContent)
	})
	surface := integrationHTTPSurface{name: "identity", handler: identityHandler, routes: []identityhttpapi.Route{
		{Pattern: "POST /auth/login", Exposures: []identityhttpapi.Exposure{identityhttpapi.ExposurePublic, identityhttpapi.ExposureTenantAdmin}},
		{Pattern: "GET /identity/users", Exposures: []identityhttpapi.Exposure{identityhttpapi.ExposureTenantAdmin}},
	}}
	runtimeHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Owner", "runtime")
		w.WriteHeader(http.StatusAccepted)
	})

	public, err := mountIdentityHTTPSurfaces(runtimehttp.SurfaceRouteGroupPublic, []identityhttpapi.Surface{surface}, runtimeHandler)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := mountIdentityHTTPSurfaces(runtimehttp.SurfaceRouteGroupTenantAdmin, []identityhttpapi.Surface{surface}, runtimeHandler)
	if err != nil {
		t.Fatal(err)
	}
	assertOwner := func(handler http.Handler, method, path, owner string, status int) {
		t.Helper()
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(method, path, nil))
		if response.Code != status || response.Header().Get("X-Owner") != owner {
			t.Fatalf("%s %s status=%d owner=%q", method, path, response.Code, response.Header().Get("X-Owner"))
		}
	}
	assertOwner(public, http.MethodPost, "/auth/login", "identity", http.StatusNoContent)
	assertOwner(public, http.MethodGet, "/identity/users", "runtime", http.StatusAccepted)
	assertOwner(admin, http.MethodGet, "/identity/users", "identity", http.StatusNoContent)
	assertOwner(admin, http.MethodGet, "/records", "runtime", http.StatusAccepted)
}

func TestProjectIdentityTopologyRejectsMissingModuleHTTPAndSaaSSurfaces(t *testing.T) {
	cfg := config.Config{IdentityWorkspaceID: "default", IdentityAudience: "orders"}
	if _, _, err := openProjectIdentity(t.Context(), cfg, identityFactoryStub{binding: moduleBindingStub{}}); err == nil {
		t.Fatal("module binding without HTTP surfaces was accepted")
	}
	surface := integrationHTTPSurface{name: "identity", handler: http.NotFoundHandler(), routes: []identityhttpapi.Route{{Pattern: "GET /identity/users", Exposures: []identityhttpapi.Exposure{identityhttpapi.ExposureTenantAdmin}}}}
	saasWithHTTP := saasHTTPBindingStub{surfaces: []identityhttpapi.Surface{surface}}
	if _, _, err := openProjectIdentity(t.Context(), cfg, identityFactoryStub{binding: saasWithHTTP}); err == nil {
		t.Fatal("SaaS binding with in-process HTTP surfaces was accepted")
	}
	if binding, surfaces, err := openProjectIdentity(t.Context(), cfg, identityFactoryStub{binding: identityBindingStub{}}); err != nil || binding == nil || len(surfaces) != 0 {
		t.Fatalf("SaaS binding=%#v surfaces=%d err=%v", binding, len(surfaces), err)
	}
}

type saasHTTPBindingStub struct {
	identityBindingStub
	surfaces []identityhttpapi.Surface
}

func (binding saasHTTPBindingStub) HTTPSurfaces() []identityhttpapi.Surface {
	return append([]identityhttpapi.Surface(nil), binding.surfaces...)
}

var _ identitysdk.Binding = moduleBindingStub{}
var _ identityhttpapi.Provider = moduleBindingStub{}
var _ identityhttpapi.Surface = integrationHTTPSurface{}
var _ identitysdk.Factory = identityFactoryStub{}
