package runtime

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulehttp"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

type runtimeModuleAdapter struct{ owner string }

func (runtimeModuleAdapter) ContractVersion() string { return modulehttp.ContractVersion }
func (adapter runtimeModuleAdapter) Owner() string   { return adapter.owner }
func (runtimeModuleAdapter) Name() string            { return "management" }
func (adapter runtimeModuleAdapter) Routes() []modulehttp.Route {
	path := "/" + strings.ReplaceAll(adapter.owner, "_", "-")
	return []modulehttp.Route{{Action: actioncontract.ActionDefinition{
		Key: "test." + adapter.owner + ".get", Owner: "module:" + adapter.owner, SourceKind: "module_http", CapabilityKey: "test." + adapter.owner, CapabilityLabel: "Test module",
		OperationKey: "get", OperationLabel: "Get test module", Label: "Get test module", Exposures: []actioncontract.Exposure{actioncontract.ExposureManagement},
		Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationAuthenticated}, HTTP: &actioncontract.HTTPBinding{Method: "GET", RouteTemplate: path},
		EffectClass: actioncontract.EffectRead, RiskLevel: actioncontract.RiskLow, IdempotencyDecision: "not_applicable", AuditClass: "test_module_read", LifecycleStatus: actioncontract.LifecycleActive,
	}}}
}
func (runtimeModuleAdapter) Handler() http.Handler { return http.NotFoundHandler() }

type runtimeHTTPOnlyAdapterBinding struct{ adapters []modulehttp.Adapter }

type runtimeDataExchangeAdapterBinding struct {
	dataexchange.Binding
	adapters []modulehttp.Adapter
}

type runtimeCompleteAuthorizationBinding struct {
	actions  []actioncontract.ActionDefinition
	adapters []modulehttp.Adapter
}

type runtimeActionManifestBinding struct {
	actions []actioncontract.ActionDefinition
}

func (binding runtimeActionManifestBinding) AuthorizationActions() ([]actioncontract.ActionDefinition, error) {
	result := make([]actioncontract.ActionDefinition, len(binding.actions))
	for index := range binding.actions {
		result[index] = actioncontract.CloneDefinition(binding.actions[index])
	}
	return result, nil
}

func (binding runtimeCompleteAuthorizationBinding) AuthorizationActions() ([]actioncontract.ActionDefinition, error) {
	result := make([]actioncontract.ActionDefinition, len(binding.actions))
	for index := range binding.actions {
		result[index] = actioncontract.CloneDefinition(binding.actions[index])
	}
	return result, nil
}

func (binding runtimeCompleteAuthorizationBinding) HTTPAdapters() []modulehttp.Adapter {
	return append([]modulehttp.Adapter(nil), binding.adapters...)
}

func (binding runtimeDataExchangeAdapterBinding) HTTPAdapters() []modulehttp.Adapter {
	return append([]modulehttp.Adapter(nil), binding.adapters...)
}

func (binding runtimeHTTPOnlyAdapterBinding) HTTPAdapters() []modulehttp.Adapter {
	return append([]modulehttp.Adapter(nil), binding.adapters...)
}

func TestRuntimeCollectsModuleOwnedHTTPAdapters(t *testing.T) {
	notification := runtimeModuleAdapter{owner: "notification"}
	dataExchange := runtimeModuleAdapter{owner: "data_exchange"}
	runtime := &Runtime{
		cfg: config.Config{IdentityAudience: "domainry-runtime"},
		moduleBindings: newRuntimeModuleBindingInventory(
			runtimeHTTPOnlyAdapterBinding{adapters: []modulehttp.Adapter{notification}},
			runtimeDataExchangeAdapterBinding{adapters: []modulehttp.Adapter{dataExchange}},
		),
	}
	adapters := runtime.ModuleHTTPAdapters()
	if len(adapters) != 5 || adapters[0].Owner() != "notification" || adapters[1].Owner() != "data_exchange" || adapters[2].Owner() != "discovery" || adapters[3].Owner() != "action" || adapters[4].Owner() != "public-resources" {
		t.Fatalf("adapters=%#v", adapters)
	}
	for _, adapter := range adapters {
		if err := modulehttp.ValidateAdapter(adapter); err != nil {
			t.Fatal(err)
		}
	}
	if routes := adapters[2].Routes(); len(routes) != 1 || routes[0].Action.Key != "runtime.discovery.modules.list" {
		t.Fatalf("Runtime modules routes=%#v", routes)
	}
	if routes := adapters[3].Routes(); len(routes) != 1 || routes[0].Action.Key != "runtime.action.permission_usages.query" {
		t.Fatalf("Runtime authorization routes=%#v", routes)
	}
	if routes := adapters[4].Routes(); len(routes) != 2 || routes[0].Action.Key != "runtime.public_resources.read" || routes[1].Action.Key != "runtime.public_resources.files.read" {
		t.Fatalf("Runtime public resource routes=%#v", routes)
	}
	response := httptest.NewRecorder()
	runtimeDiscoveryHTTPAdapter{runtime: &Runtime{}}.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/discovery/modules", nil))
	var inventory map[string]any
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(response.Body.Bytes(), &inventory) != nil || inventory["contract_version"] == "" {
		t.Fatalf("inventory response status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	adapters[0] = nil
	if runtime.ModuleHTTPAdapters()[0] == nil {
		t.Fatal("caller mutated Runtime module Adapter inventory")
	}
}

func TestRuntimePublicResourceAdapterOmitsFileRouteWhenUploadsAreUnselected(t *testing.T) {
	runtime := &Runtime{schemaCapabilitiesSelected: true, schemaCapabilities: persistence.RuntimeSchemaCapabilities{}}
	adapter := runtimePublicResourceHTTPAdapter{runtime: runtime}
	if routes := adapter.Routes(); len(routes) != 1 || routes[0].Action.Key != "runtime.public_resources.read" {
		t.Fatalf("minimal public-resource routes=%#v", routes)
	}

	response := httptest.NewRecorder()
	adapter.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/public-resources/catalog/access/files/manual", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("unselected public-resource file route status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRuntimeModuleAdapterQueriesLivePermissionUsagesAsOneBatch(t *testing.T) {
	registry := actioncontract.NewRegistry()
	if err := registry.Register(runtimeModuleInventoryActions("domainry-runtime")...); err != nil {
		t.Fatal(err)
	}
	if err := registry.Freeze(); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{cfg: config.Config{IdentityAudience: "domainry-runtime"}, authorizationActions: func() *actioncontract.Registry { return registry }}
	query, err := actioncontract.NewPermissionUsageRequest([]actioncontract.PermissionUsageQuery{{
		SourceOwner: "runtime:builtin", PermissionKeys: []string{"runtime.discovery.modules.list"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(query)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	runtimeActionHTTPAdapter{runtime: runtime}.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/action/permission-usages/query", bytes.NewReader(body)))
	var snapshot actioncontract.PermissionUsageSnapshot
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &snapshot) != nil {
		t.Fatalf("query status=%d body=%s", response.Code, response.Body.String())
	}
	if err := snapshot.ValidateFor(query); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Owners) != 1 || !snapshot.Owners[0].Available || len(snapshot.Owners[0].Usages) != 1 || snapshot.Owners[0].Usages[0].Action.Key != "runtime.discovery.modules.list" {
		t.Fatalf("query snapshot=%#v", snapshot)
	}

	invalid := httptest.NewRecorder()
	runtimeActionHTTPAdapter{runtime: runtime}.Handler().ServeHTTP(invalid, httptest.NewRequest(http.MethodPost, "/action/permission-usages/query", bytes.NewBufferString(`{"contract_version":"bad"}`)))
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid query status=%d body=%s", invalid.Code, invalid.Body.String())
	}
}

func TestRuntimeCollectsHTTPOnlyAndCompleteModuleActionManifests(t *testing.T) {
	httpOnly := runtimeHTTPOnlyAdapterBinding{adapters: []modulehttp.Adapter{runtimeModuleAdapter{owner: "notification"}}}
	completeAdapter := runtimeModuleAdapter{owner: "agent"}
	httpAction := completeAdapter.Routes()[0].Action
	nonHTTPAction := actioncontract.CloneDefinition(httpAction)
	nonHTTPAction.Key = "agent.tasks.run"
	nonHTTPAction.CapabilityKey = "agent.tasks"
	nonHTTPAction.OperationKey = "run"
	nonHTTPAction.OperationLabel = "Run agent task"
	nonHTTPAction.Label = "Run agent task"
	nonHTTPAction.HTTP = nil
	nonHTTPAction.NonHTTP = []actioncontract.NonHTTPBinding{{Kind: "agent", InvocationKey: "agent.tasks.run"}}
	complete := runtimeCompleteAuthorizationBinding{
		actions: []actioncontract.ActionDefinition{httpAction, nonHTTPAction}, adapters: []modulehttp.Adapter{completeAdapter},
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
		t.Fatal("Runtime accepted an HTTP Adapter that drifted from the complete module manifest")
	}
}

func TestRuntimeDoesNotImportUnmountedSchedulerHTTPActions(t *testing.T) {
	actions, err := schedulersdk.SchedulerAuthorizationActions()
	if err != nil {
		t.Fatal(err)
	}
	collected, err := newRuntimeModuleBindingInventory(runtimeActionManifestBinding{actions: actions}).AuthorizationActions()
	if err != nil {
		t.Fatal(err)
	}
	if len(collected) != 0 {
		t.Fatalf("Runtime imported %d unmounted Scheduler HTTP actions", len(collected))
	}
}

var _ modulehttp.Adapter = runtimeModuleAdapter{}
var _ modulehttp.Provider = runtimeHTTPOnlyAdapterBinding{}
var _ modulehttp.Provider = runtimeDataExchangeAdapterBinding{}
var _ modulehttp.Provider = runtimeCompleteAuthorizationBinding{}
var _ actioncontract.Provider = runtimeCompleteAuthorizationBinding{}
var _ actioncontract.Provider = runtimeActionManifestBinding{}
