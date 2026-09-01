package runtime

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulehttp"
)

// ModuleHTTPSurfaces returns routes owned by in-process module Bindings.
// Runtime-owned orchestration and cross-capability routes remain on Runtime's
// HTTP router.
func (runtime *Runtime) ModuleHTTPSurfaces() []modulehttp.Surface {
	if runtime == nil {
		return nil
	}
	surfaces := runtime.moduleBindings.HTTPSurfaces()
	surfaces = append(surfaces, runtimeModuleInventorySurface{runtime: runtime})
	return append([]modulehttp.Surface(nil), surfaces...)
}

// runtimeModuleBindingInventory is the one process-composition inventory from
// which Runtime derives both module HTTP surfaces and module authorization
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

func (inventory runtimeModuleBindingInventory) HTTPSurfaces() []modulehttp.Surface {
	var surfaces []modulehttp.Surface
	for _, binding := range inventory.bindings {
		if provider, ok := binding.(modulehttp.Provider); ok && provider != nil {
			surfaces = append(surfaces, provider.HTTPSurfaces()...)
		}
	}
	return append([]modulehttp.Surface(nil), surfaces...)
}

// AuthorizationActions collects one complete authorization
// contribution per module. Existing HTTP-only modules use Route.Action as
// their source manifest. Modules with RPC/job/agent Actions implement
// action.Provider; their HTTP Surfaces must then be exact projections of that
// complete manifest.
func (inventory runtimeModuleBindingInventory) AuthorizationActions() ([]actioncontract.ActionDefinition, error) {
	definitions := []actioncontract.ActionDefinition{}
	for _, binding := range inventory.bindings {
		actionProvider, hasCompleteManifest := binding.(actioncontract.Provider)
		httpProvider, hasHTTPSurfaces := binding.(modulehttp.Provider)
		if !hasCompleteManifest && !hasHTTPSurfaces {
			continue
		}
		if hasHTTPSurfaces {
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
			if err := modulehttp.ValidateAuthorizationProjection(provided, httpProvider); err != nil {
				return nil, fmt.Errorf("validate module authorization projection: %w", err)
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

type runtimeModuleInventorySurface struct{ runtime *Runtime }

func (runtimeModuleInventorySurface) ContractVersion() string { return modulehttp.ContractVersion }
func (runtimeModuleInventorySurface) Owner() string           { return "runtime" }
func (runtimeModuleInventorySurface) Name() string            { return "module_inventory" }
func (surface runtimeModuleInventorySurface) Routes() []modulehttp.Route {
	audience := ""
	if surface.runtime != nil {
		audience = surface.runtime.cfg.IdentityAudience
	}
	actions := runtimeModuleInventoryActions(audience)
	routes := make([]modulehttp.Route, 0, len(actions))
	for _, action := range actions {
		routes = append(routes, modulehttp.Route{Action: action})
	}
	return routes
}

func runtimeModuleInventoryActions(identityAudience string) []actioncontract.ActionDefinition {
	return []actioncontract.ActionDefinition{runtimeModuleInventoryAction(), runtimePermissionUsageQueryAction(identityAudience)}
}

func runtimeModuleInventoryAction() actioncontract.ActionDefinition {
	return actioncontract.ActionDefinition{
		Key: "runtime.modules.list", Owner: "runtime:builtin", SourceKind: "builtin_surface", CapabilityKey: "runtime.modules", CapabilityLabel: "Runtime modules",
		OperationKey: "list", OperationLabel: "List Runtime modules", Label: "List Runtime modules", Exposures: []actioncontract.Exposure{actioncontract.ExposureOps},
		Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationExactRolePermission},
		HTTP:          &actioncontract.HTTPBinding{Method: "GET", RouteTemplate: "/operations/modules"},
		Permission:    &actioncontract.PermissionDefinition{Key: "runtime.modules.list", Owner: "runtime:builtin", ResourceKey: "runtime.modules", ActionKey: "list", Label: "List Runtime modules", Category: "Runtime", LifecycleStatus: actioncontract.LifecycleActive},
		EffectClass:   actioncontract.EffectRead, RiskLevel: actioncontract.RiskLow, IdempotencyDecision: "not_applicable", AuditClass: "runtime_module_inventory_read", LifecycleStatus: actioncontract.LifecycleActive,
	}
}

func runtimePermissionUsageQueryAction(identityAudience string) actioncontract.ActionDefinition {
	return actioncontract.ActionDefinition{
		Key: "runtime.authorization.action_usages.query", Owner: "runtime:builtin", SourceKind: "builtin_surface", CapabilityKey: "runtime.authorization", CapabilityLabel: "Runtime authorization",
		OperationKey: "action_usages.query", OperationLabel: "Query Action usages", Label: "Query live Action usages", Exposures: []actioncontract.Exposure{actioncontract.ExposureTenantAdmin, actioncontract.ExposureOps},
		Authorization: actioncontract.Authorization{Strategy: actioncontract.AuthorizationServiceIdentity, PolicyKey: "runtime.authorization.action_usages.query", Audiences: []string{strings.TrimSpace(identityAudience)}},
		HTTP:          &actioncontract.HTTPBinding{Method: http.MethodPost, RouteTemplate: "/operations/authorization/action-usages/query"},
		EffectClass:   actioncontract.EffectRead, RiskLevel: actioncontract.RiskLow, IdempotencyDecision: "not_applicable", AuditClass: "runtime_action_usage_read", LifecycleStatus: actioncontract.LifecycleActive,
	}
}

func (s runtimeModuleInventorySurface) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /operations/modules", func(response http.ResponseWriter, request *http.Request) {
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
	mux.HandleFunc("POST /operations/authorization/action-usages/query", func(response http.ResponseWriter, request *http.Request) {
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
			_ = json.NewEncoder(response).Encode(map[string]string{"code": "runtime.authorization_registry_unavailable"})
			return
		}
		registry := s.runtime.authorizationActions()
		if registry == nil {
			response.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(response).Encode(map[string]string{"code": "runtime.authorization_registry_unavailable"})
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

var _ modulehttp.Surface = runtimeModuleInventorySurface{}
