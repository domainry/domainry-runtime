package runtimehost

import (
	"net/http"
	"net/http/httptest"
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityhttpapi "github.com/domainry/domainry-identity-sdk/httpapi"
	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
)

type integrationHTTPAdapter struct {
	name    string
	routes  []identityhttpapi.Route
	handler http.Handler
}

func (integrationHTTPAdapter) ContractVersion() string { return identityhttpapi.ContractVersion }
func (integrationHTTPAdapter) Owner() string           { return "identity" }
func (adapter integrationHTTPAdapter) Name() string    { return adapter.name }
func (adapter integrationHTTPAdapter) Routes() []identityhttpapi.Route {
	return append([]identityhttpapi.Route(nil), adapter.routes...)
}
func (adapter integrationHTTPAdapter) Handler() http.Handler { return adapter.handler }

type moduleBindingStub struct {
	runtimetestkit.IdentityBindingStub
	adapters []identityhttpapi.Adapter
}

func (moduleBindingStub) Descriptor() identitysdk.Descriptor {
	return identitysdk.Descriptor{Mode: identitysdk.DeploymentModeModule}
}
func (binding moduleBindingStub) HTTPAdapters() []identityhttpapi.Adapter {
	return append([]identityhttpapi.Adapter(nil), binding.adapters...)
}

func TestProjectHostMountsModuleIdentityHTTPOutsideRuntime(t *testing.T) {
	identityHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Owner", "identity")
		w.WriteHeader(http.StatusNoContent)
	})
	adapter := integrationHTTPAdapter{name: "identity", handler: identityHandler, routes: []identityhttpapi.Route{
		{Action: runtimeHostTestAction("auth.login", "POST /auth/login", []actioncontract.Exposure{actioncontract.ExposurePublic, actioncontract.ExposureManagement}, actioncontract.AuthorizationAnonymous)},
		{Action: runtimeHostTestAction("identity.users.list", "GET /identity/users", []actioncontract.Exposure{actioncontract.ExposureManagement}, actioncontract.AuthorizationAuthenticated)},
	}}
	runtimeHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Owner", "runtime")
		w.WriteHeader(http.StatusAccepted)
	})

	public, err := mountIdentityHTTPAdapters(runtimehttp.ListenerRouteGroupPublic, []identityhttpapi.Adapter{adapter}, runtimeHandler)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := mountIdentityHTTPAdapters(runtimehttp.ListenerRouteGroupManagement, []identityhttpapi.Adapter{adapter}, runtimeHandler)
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
	cfg := config.Config{IdentityWorkspaceID: "workspace-primary", IdentityAudience: "orders"}
	if _, _, err := openProjectIdentity(t.Context(), cfg, identityFactoryStub{binding: moduleBindingStub{}}); err == nil {
		t.Fatal("module binding without HTTP adapters was accepted")
	}
	adapter := integrationHTTPAdapter{name: "identity", handler: http.NotFoundHandler(), routes: []identityhttpapi.Route{{Action: runtimeHostTestAction("identity.users.list", "GET /identity/users", []actioncontract.Exposure{actioncontract.ExposureManagement}, actioncontract.AuthorizationAuthenticated)}}}
	saasWithHTTP := saasHTTPBindingStub{adapters: []identityhttpapi.Adapter{adapter}}
	if _, _, err := openProjectIdentity(t.Context(), cfg, identityFactoryStub{binding: saasWithHTTP}); err == nil {
		t.Fatal("SaaS binding with in-process HTTP adapters was accepted")
	}
	if binding, adapters, err := openProjectIdentity(t.Context(), cfg, identityFactoryStub{binding: identityBindingStub{}}); err != nil || binding == nil || len(adapters) != 0 {
		t.Fatalf("SaaS binding=%#v adapters=%d err=%v", binding, len(adapters), err)
	}
}

type saasHTTPBindingStub struct {
	identityBindingStub
	adapters []identityhttpapi.Adapter
}

func (binding saasHTTPBindingStub) HTTPAdapters() []identityhttpapi.Adapter {
	return append([]identityhttpapi.Adapter(nil), binding.adapters...)
}

var _ identitysdk.Binding = moduleBindingStub{}
var _ identityhttpapi.Provider = moduleBindingStub{}
var _ identityhttpapi.Adapter = integrationHTTPAdapter{}
var _ identitysdk.Factory = identityFactoryStub{}
