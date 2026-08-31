package runtime

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-foundation/modulehttp"
	partysdk "github.com/domainry/domainry-party-sdk"
)

type runtimeModuleSurface struct{ owner string }

func (runtimeModuleSurface) ContractVersion() string { return modulehttp.ContractVersion }
func (surface runtimeModuleSurface) Owner() string   { return surface.owner }
func (runtimeModuleSurface) Name() string            { return "management" }
func (runtimeModuleSurface) Routes() []modulehttp.Route {
	return []modulehttp.Route{{Pattern: "GET /module", Exposures: []modulehttp.Exposure{modulehttp.ExposureTenantAdmin}, Authentication: modulehttp.AuthenticationAuthenticated, PrincipalOnly: true}}
}
func (runtimeModuleSurface) Handler() http.Handler { return http.NotFoundHandler() }

type runtimePartySurfaceBinding struct {
	partysdk.Binding
	surfaces []modulehttp.Surface
}

type runtimeDataExchangeSurfaceBinding struct {
	dataexchange.Binding
	surfaces []modulehttp.Surface
}

func (binding runtimeDataExchangeSurfaceBinding) HTTPSurfaces() []modulehttp.Surface {
	return append([]modulehttp.Surface(nil), binding.surfaces...)
}

func (binding runtimePartySurfaceBinding) HTTPSurfaces() []modulehttp.Surface {
	return append([]modulehttp.Surface(nil), binding.surfaces...)
}

func TestRuntimeCollectsModuleOwnedHTTPSurfaces(t *testing.T) {
	party := runtimeModuleSurface{owner: "party"}
	dataExchange := runtimeModuleSurface{owner: "data_exchange"}
	runtime := &Runtime{
		partyBinding:        runtimePartySurfaceBinding{surfaces: []modulehttp.Surface{party}},
		dataExchangeBinding: runtimeDataExchangeSurfaceBinding{surfaces: []modulehttp.Surface{dataExchange}},
	}
	surfaces := runtime.ModuleHTTPSurfaces()
	if len(surfaces) != 3 || surfaces[0].Owner() != "party" || surfaces[1].Owner() != "data_exchange" || surfaces[2].Name() != "module_inventory" {
		t.Fatalf("surfaces=%#v", surfaces)
	}
	if err := modulehttp.ValidateSurface(surfaces[1]); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	runtimeModuleInventorySurface{runtime: &Runtime{}}.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/operations/modules", nil))
	var inventory map[string]any
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(response.Body.Bytes(), &inventory) != nil || inventory["contract_version"] == "" {
		t.Fatalf("inventory response status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	surfaces[0] = nil
	if runtime.ModuleHTTPSurfaces()[0] == nil {
		t.Fatal("caller mutated Runtime module Surface inventory")
	}
}

var _ modulehttp.Surface = runtimeModuleSurface{}
var _ modulehttp.Provider = runtimePartySurfaceBinding{}
var _ modulehttp.Provider = runtimeDataExchangeSurfaceBinding{}
