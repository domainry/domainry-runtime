package runtime

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulehttp"
	partysdk "github.com/domainry/domainry-party-sdk"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type runtimeModuleSurface struct{ owner string }

func (runtimeModuleSurface) ContractVersion() string { return modulehttp.ContractVersion }
func (surface runtimeModuleSurface) Owner() string   { return surface.owner }
func (runtimeModuleSurface) Name() string            { return "management" }
func (surface runtimeModuleSurface) Routes() []modulehttp.Route {
	return []modulehttp.Route{{Action: actioncontract.ActionDefinition{
		Key: "test." + surface.owner + ".get", Owner: "module:" + surface.owner, SourceKind: "module_surface", CapabilityKey: "test." + surface.owner, CapabilityLabel: "Test module",
		OperationKey: "get", OperationLabel: "Get test module", Label: "Get test module", Exposures: []actioncontract.Exposure{actioncontract.ExposureTenantAdmin},
		Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationAuthenticatedPrincipal}, HTTP: &actioncontract.HTTPBinding{Method: "GET", RouteTemplate: "/" + surface.owner},
		EffectClass: actioncontract.EffectRead, RiskLevel: actioncontract.RiskLow, IdempotencyDecision: "not_applicable", AuditClass: "test_module_read", LifecycleStatus: actioncontract.LifecycleActive,
	}}}
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

type runtimeCompleteAuthorizationBinding struct {
	actions  []actioncontract.ActionDefinition
	surfaces []modulehttp.Surface
}

func (binding runtimeCompleteAuthorizationBinding) AuthorizationActions() ([]actioncontract.ActionDefinition, error) {
	result := make([]actioncontract.ActionDefinition, len(binding.actions))
	for index := range binding.actions {
		result[index] = actioncontract.CloneDefinition(binding.actions[index])
	}
	return result, nil
}

func (binding runtimeCompleteAuthorizationBinding) HTTPSurfaces() []modulehttp.Surface {
	return append([]modulehttp.Surface(nil), binding.surfaces...)
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
		cfg: config.Config{IdentityAudience: "domainry-runtime"},
		moduleBindings: newRuntimeModuleBindingInventory(
			runtimePartySurfaceBinding{surfaces: []modulehttp.Surface{party}},
			runtimeDataExchangeSurfaceBinding{surfaces: []modulehttp.Surface{dataExchange}},
		),
	}
	surfaces := runtime.ModuleHTTPSurfaces()
	if len(surfaces) != 3 || surfaces[0].Owner() != "party" || surfaces[1].Owner() != "data_exchange" || surfaces[2].Name() != "module_inventory" {
		t.Fatalf("surfaces=%#v", surfaces)
	}
	if err := modulehttp.ValidateSurface(surfaces[1]); err != nil {
		t.Fatal(err)
	}
	if routes := surfaces[2].Routes(); len(routes) != 2 || routes[0].Action.Key != "runtime.modules.list" || routes[1].Action.Key != "runtime.authorization.action_usages.query" {
		t.Fatalf("Runtime inventory routes=%#v", routes)
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

func TestRuntimeModuleSurfaceQueriesLivePermissionUsagesAsOneBatch(t *testing.T) {
	registry := actioncontract.NewRegistry()
	if err := registry.Register(runtimeModuleInventoryActions("domainry-runtime")...); err != nil {
		t.Fatal(err)
	}
	if err := registry.Freeze(); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{cfg: config.Config{IdentityAudience: "domainry-runtime"}, authorizationActions: func() *actioncontract.Registry { return registry }}
	query, err := actioncontract.NewPermissionUsageRequest([]actioncontract.PermissionUsageQuery{{
		SourceOwner: "runtime:builtin", PermissionKeys: []string{"runtime.modules.list"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(query)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	runtimeModuleInventorySurface{runtime: runtime}.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/operations/authorization/action-usages/query", bytes.NewReader(body)))
	var snapshot actioncontract.PermissionUsageSnapshot
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &snapshot) != nil {
		t.Fatalf("query status=%d body=%s", response.Code, response.Body.String())
	}
	if err := snapshot.ValidateFor(query); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Owners) != 1 || !snapshot.Owners[0].Available || len(snapshot.Owners[0].Usages) != 1 || snapshot.Owners[0].Usages[0].Action.Key != "runtime.modules.list" {
		t.Fatalf("query snapshot=%#v", snapshot)
	}

	invalid := httptest.NewRecorder()
	runtimeModuleInventorySurface{runtime: runtime}.Handler().ServeHTTP(invalid, httptest.NewRequest(http.MethodPost, "/operations/authorization/action-usages/query", bytes.NewBufferString(`{"contract_version":"bad"}`)))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid query status=%d body=%s", invalid.Code, invalid.Body.String())
	}
}

func TestRuntimeCollectsHTTPOnlyAndCompleteModuleActionManifests(t *testing.T) {
	httpOnly := runtimePartySurfaceBinding{surfaces: []modulehttp.Surface{runtimeModuleSurface{owner: "party"}}}
	completeSurface := runtimeModuleSurface{owner: "agent"}
	httpAction := completeSurface.Routes()[0].Action
	nonHTTPAction := actioncontract.CloneDefinition(httpAction)
	nonHTTPAction.Key = "agent.tasks.run"
	nonHTTPAction.CapabilityKey = "agent.tasks"
	nonHTTPAction.OperationKey = "run"
	nonHTTPAction.OperationLabel = "Run agent task"
	nonHTTPAction.Label = "Run agent task"
	nonHTTPAction.HTTP = nil
	nonHTTPAction.NonHTTP = []actioncontract.NonHTTPBinding{{Kind: "agent", InvocationKey: "agent.tasks.run"}}
	complete := runtimeCompleteAuthorizationBinding{
		actions: []actioncontract.ActionDefinition{httpAction, nonHTTPAction}, surfaces: []modulehttp.Surface{completeSurface},
	}

	inventory := newRuntimeModuleBindingInventory(httpOnly, complete)
	actions, err := inventory.AuthorizationActions()
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 3 {
		t.Fatalf("actions=%#v", actions)
	}
	foundNonHTTP := false
	for _, action := range actions {
		if action.Key == nonHTTPAction.Key && action.HTTP == nil && len(action.NonHTTP) == 1 {
			foundNonHTTP = true
		}
	}
	if !foundNonHTTP {
		t.Fatalf("pure non-HTTP Action was not contributed: %#v", actions)
	}

	complete.actions[0].Label = "Drifted source manifest"
	if _, err := newRuntimeModuleBindingInventory(complete).AuthorizationActions(); err == nil {
		t.Fatal("Runtime accepted an HTTP Surface that drifted from the complete module manifest")
	}
}

var _ modulehttp.Surface = runtimeModuleSurface{}
var _ modulehttp.Provider = runtimePartySurfaceBinding{}
var _ modulehttp.Provider = runtimeDataExchangeSurfaceBinding{}
var _ modulehttp.Provider = runtimeCompleteAuthorizationBinding{}
var _ actioncontract.Provider = runtimeCompleteAuthorizationBinding{}
