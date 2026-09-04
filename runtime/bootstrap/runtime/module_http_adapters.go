package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulehttp"
	endpointmodel "github.com/domainry/domainry-runtime/runtime/domain/endpoint/model"
)

// ModuleHTTPAdapters returns routes owned by in-process module Bindings.
// Runtime-owned orchestration and cross-capability routes remain on Runtime's
// HTTP router.
func (runtime *Runtime) ModuleHTTPAdapters() []modulehttp.Adapter {
	if runtime == nil {
		return nil
	}
	adapters := runtime.moduleBindings.HTTPAdapters()
	adapters = append(adapters,
		runtimeDiscoveryHTTPAdapter{runtime: runtime},
		runtimeActionHTTPAdapter{runtime: runtime},
	)
	return append([]modulehttp.Adapter(nil), adapters...)
}

// runtimeModuleBindingInventory is the one process-composition inventory from
// which Runtime derives both module HTTP adapters and module authorization
// Actions. Keeping the heterogeneous SDK Bindings here avoids a second module
// registration list while leaving each source module in charge of its own
// contracts.
type runtimeModuleBindingInventory struct {
	bindings []any
}

func newRuntimeModuleBindingInventory(bindings ...any) runtimeModuleBindingInventory {
	return runtimeModuleBindingInventory{bindings: append([]any(nil), bindings...)}
}

func (inventory runtimeModuleBindingInventory) clone() runtimeModuleBindingInventory {
	return newRuntimeModuleBindingInventory(inventory.bindings...)
}

func (inventory runtimeModuleBindingInventory) HTTPAdapters() []modulehttp.Adapter {
	var adapters []modulehttp.Adapter
	for _, binding := range inventory.bindings {
		if provider, ok := binding.(modulehttp.Provider); ok && provider != nil {
			adapters = append(adapters, provider.HTTPAdapters()...)
		}
	}
	return append([]modulehttp.Adapter(nil), adapters...)
}

// AuthorizationActions collects one complete authorization
// contribution per module. Existing HTTP-only modules use Route.Action as
// their source manifest. Modules with RPC/job/agent Actions implement
// action.Provider; their HTTP Adapters must then be exact projections of that
// complete manifest.
func (inventory runtimeModuleBindingInventory) AuthorizationActions() ([]actioncontract.ActionDefinition, error) {
	definitions := []actioncontract.ActionDefinition{}
	for _, binding := range inventory.bindings {
		actionProvider, hasCompleteManifest := binding.(actioncontract.Provider)
		httpProvider, hasHTTPAdapters := binding.(modulehttp.Provider)
		if !hasCompleteManifest && !hasHTTPAdapters {
			continue
		}
		if hasHTTPAdapters {
			if err := modulehttp.ValidateSourceOwners(httpProvider); err != nil {
				return nil, fmt.Errorf("validate module authorization owner: %w", err)
			}
		}

		var provided []actioncontract.ActionDefinition
		var err error
		if hasCompleteManifest {
			provided, err = actionProvider.AuthorizationActions()
			if err != nil {
				return nil, fmt.Errorf("load module authorization Actions: %w", err)
			}
			if len(provided) == 0 {
				return nil, fmt.Errorf("module Action provider returned an empty manifest")
			}
			if hasHTTPAdapters {
				if err := modulehttp.ValidateAuthorizationProjection(provided, httpProvider); err != nil {
					return nil, fmt.Errorf("validate module authorization projection: %w", err)
				}
			} else if err := validateRuntimeHostedModuleFacade(provided); err != nil {
				return nil, fmt.Errorf("validate Runtime-hosted module authorization projection: %w", err)
			}
		} else {
			provided, err = modulehttp.AuthorizationActions(httpProvider)
			if err != nil {
				return nil, fmt.Errorf("load HTTP-only module authorization Actions: %w", err)
			}
		}
		for index := range provided {
			definition, err := actioncontract.NormalizeDefinition(provided[index])
			if err != nil {
				return nil, fmt.Errorf("normalize module authorization Action %d: %w", index, err)
			}
			definitions = append(definitions, definition)
		}
	}
	return definitions, nil
}

func validateRuntimeHostedModuleFacade(definitions []actioncontract.ActionDefinition) error {
	owners := map[string]bool{}
	patterns := make(map[string]endpointmodel.RuntimeEndpointContractV1)
	httpActions := 0
	for _, definition := range definitions {
		normalized, err := actioncontract.NormalizeDefinition(definition)
		if err != nil {
			return err
		}
		if normalized.HTTP == nil {
			continue
		}
		httpActions++
		if normalized.SourceKind != "host_facade" || !strings.HasPrefix(normalized.Owner, "module:") {
			return fmt.Errorf("module HTTP Action %q has no mounted Adapter and is not an explicit host facade", normalized.Key)
		}
		owners[strings.TrimPrefix(normalized.Owner, "module:")] = true
	}
	if httpActions == 0 {
		return nil
	}
	if len(owners) != 1 {
		return fmt.Errorf("Runtime-hosted module facade must have exactly one source owner")
	}
	owner := ""
	for value := range owners {
		owner = value
	}
	for pattern, contract := range endpointmodel.EndpointContracts {
		if strings.TrimSpace(contract.SourceOwner) == owner && strings.HasPrefix(strings.TrimSpace(contract.PermissionPolicyRef), "static_permission:") {
			patterns[strings.TrimSpace(pattern)] = contract
		}
	}
	for _, definition := range definitions {
		if definition.HTTP == nil {
			continue
		}
		pattern := strings.TrimSpace(definition.HTTP.Method) + " " + strings.TrimSpace(definition.HTTP.RouteTemplate)
		contract, found := patterns[pattern]
		if !found {
			return fmt.Errorf("module host-facade Action %q has no Runtime handler contract for %q", definition.Key, pattern)
		}
		if err := endpointmodel.ValidateHostFacadeAction(contract, definition); err != nil {
			return fmt.Errorf("validate module host-facade Action %q: %w", definition.Key, err)
		}
		delete(patterns, pattern)
	}
	if len(patterns) != 0 {
		return fmt.Errorf("Runtime source %q has protected handler contracts absent from its module Action manifest", owner)
	}
	return nil
}

type runtimeDiscoveryHTTPAdapter struct{ runtime *Runtime }

func (runtimeDiscoveryHTTPAdapter) ContractVersion() string { return modulehttp.ContractVersion }
func (runtimeDiscoveryHTTPAdapter) Owner() string           { return "discovery" }
func (runtimeDiscoveryHTTPAdapter) Name() string            { return "modules" }
func (runtimeDiscoveryHTTPAdapter) Routes() []modulehttp.Route {
	return []modulehttp.Route{{Action: runtimeModuleInventoryAction()}}
}

type runtimeActionHTTPAdapter struct{ runtime *Runtime }

func (runtimeActionHTTPAdapter) ContractVersion() string { return modulehttp.ContractVersion }
func (runtimeActionHTTPAdapter) Owner() string           { return "action" }
func (runtimeActionHTTPAdapter) Name() string            { return "permission_usage" }
func (adapter runtimeActionHTTPAdapter) Routes() []modulehttp.Route {
	audience := ""
	if adapter.runtime != nil {
		audience = adapter.runtime.cfg.IdentityAudience
	}
	return []modulehttp.Route{{Action: runtimePermissionUsageQueryAction(audience)}}
}

func runtimeModuleInventoryActions(identityAudience string) []actioncontract.ActionDefinition {
	return []actioncontract.ActionDefinition{runtimeModuleInventoryAction(), runtimePermissionUsageQueryAction(identityAudience)}
}

func runtimeModuleInventoryAction() actioncontract.ActionDefinition {
	return actioncontract.ActionDefinition{
		Key: "runtime.discovery.modules.list", Owner: "runtime:builtin", SourceKind: "builtin_http", CapabilityKey: "runtime.discovery.modules", CapabilityLabel: "Runtime modules",
		OperationKey: "list", OperationLabel: "List Runtime modules", Label: "List Runtime modules", Exposures: []actioncontract.Exposure{actioncontract.ExposureOps},
		Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationAuthenticated},
		HTTP:          &actioncontract.HTTPBinding{Method: "GET", RouteTemplate: "/discovery/modules"},
		Permission:    &actioncontract.PermissionDefinition{Key: "runtime.discovery.modules.list", Owner: "runtime:builtin", ResourceKey: "runtime.discovery.modules", OperationKey: "list", Label: "List Runtime modules", Category: "Runtime", LifecycleStatus: actioncontract.LifecycleActive},
		EffectClass:   actioncontract.EffectRead, RiskLevel: actioncontract.RiskLow, IdempotencyDecision: "not_applicable", AuditClass: "runtime_module_inventory_read", LifecycleStatus: actioncontract.LifecycleActive,
	}
}

func runtimePermissionUsageQueryAction(identityAudience string) actioncontract.ActionDefinition {
	return actioncontract.ActionDefinition{
		Key: "runtime.action.permission_usages.query", Owner: "runtime:builtin", SourceKind: "builtin_http", CapabilityKey: "runtime.action.permission_usages", CapabilityLabel: "Action permission usages",
		OperationKey: "action_usages.query", OperationLabel: "Query Action usages", Label: "Query live Action usages", Exposures: []actioncontract.Exposure{actioncontract.ExposureManagement, actioncontract.ExposureOps},
		Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationSigned, PolicyKey: "runtime.action.permission_usages.query", Audiences: []string{strings.TrimSpace(identityAudience)}},
		HTTP:          &actioncontract.HTTPBinding{Method: http.MethodPost, RouteTemplate: "/action/permission-usages/query"},
		EffectClass:   actioncontract.EffectRead, RiskLevel: actioncontract.RiskLow, IdempotencyDecision: "not_applicable", AuditClass: "runtime_action_usage_read", LifecycleStatus: actioncontract.LifecycleActive,
	}
}

func (s runtimeDiscoveryHTTPAdapter) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /discovery/modules", func(response http.ResponseWriter, request *http.Request) {
		inventory, err := s.runtime.ModuleInventory()
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("Cache-Control", "no-store")
		if err != nil {
			response.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(response).Encode(map[string]string{"code": "runtime.module_inventory_unavailable"})
			return
		}
		_ = json.NewEncoder(response).Encode(inventory)
	})
	return mux
}

func (s runtimeActionHTTPAdapter) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /action/permission-usages/query", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("Cache-Control", "no-store")
		var query actioncontract.PermissionUsageRequest
		decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&query); err != nil {
			response.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(response).Encode(map[string]string{"code": "runtime.action_usage_query_invalid"})
			return
		}
		if err := ensureRuntimeUsageRequestEOF(decoder); err != nil {
			response.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(response).Encode(map[string]string{"code": "runtime.action_usage_query_invalid"})
			return
		}
		if s.runtime == nil || s.runtime.authorizationActions == nil {
			response.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(response).Encode(map[string]string{"code": "runtime.action_registry_unavailable"})
			return
		}
		registry := s.runtime.authorizationActions()
		if registry == nil {
			response.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(response).Encode(map[string]string{"code": "runtime.action_registry_unavailable"})
			return
		}
		snapshot, err := registry.QueryPermissionUsages(request.Context(), query)
		if err != nil {
			response.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(response).Encode(map[string]string{"code": "runtime.action_usage_query_invalid"})
			return
		}
		_ = json.NewEncoder(response).Encode(snapshot)
	})
	return mux
}

func ensureRuntimeUsageRequestEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

var _ modulehttp.Adapter = runtimeDiscoveryHTTPAdapter{}
var _ modulehttp.Adapter = runtimeActionHTTPAdapter{}
