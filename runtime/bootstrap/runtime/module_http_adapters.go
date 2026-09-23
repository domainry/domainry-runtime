package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulehttp"
	publicresourceapplication "github.com/domainry/domainry-runtime/runtime/application/publicresource"
	hostsurfacemodel "github.com/domainry/domainry-runtime/runtime/domain/hostsurface/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
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
		runtimePublicResourceHTTPAdapter{runtime: runtime},
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
// their source manifest. HTTP Actions are admitted only when the module mounts
// a source-owned Adapter; an unmounted service endpoint is not a Runtime
// authorization surface. Non-HTTP module Actions remain available to Runtime
// orchestration.
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
			if definition.HTTP != nil && !hasHTTPAdapters {
				continue
			}
			definitions = append(definitions, definition)
		}
	}
	return definitions, nil
}

type runtimeDiscoveryHTTPAdapter struct{ runtime *Runtime }

func (runtimeDiscoveryHTTPAdapter) ContractVersion() string { return modulehttp.ContractVersion }
func (runtimeDiscoveryHTTPAdapter) Owner() string           { return "discovery" }
func (runtimeDiscoveryHTTPAdapter) Name() string            { return "modules" }
func (runtimeDiscoveryHTTPAdapter) Routes() []modulehttp.Route {
	return []modulehttp.Route{{Action: runtimeModuleInventoryAction()}}
}

type runtimeActionHTTPAdapter struct{ runtime *Runtime }

type runtimePublicResourceHTTPAdapter struct{ runtime *Runtime }

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

func (runtimePublicResourceHTTPAdapter) ContractVersion() string { return modulehttp.ContractVersion }
func (runtimePublicResourceHTTPAdapter) Owner() string           { return "public-resources" }
func (runtimePublicResourceHTTPAdapter) Name() string            { return "resources" }
func (adapter runtimePublicResourceHTTPAdapter) Routes() []modulehttp.Route {
	routes := []modulehttp.Route{{Action: hostsurfacemodel.PublicResourceReadAction()}}
	if adapter.runtime.effectiveSchemaCapabilities().Uploads {
		routes = append(routes, modulehttp.Route{Action: hostsurfacemodel.PublicResourceFileReadAction()})
	}
	return routes
}

func runtimeModuleInventoryActions(identityAudience string, selected ...persistence.RuntimeSchemaCapabilities) []actioncontract.ActionDefinition {
	actions := []actioncontract.ActionDefinition{runtimeModuleInventoryAction(), runtimePermissionUsageQueryAction(identityAudience), hostsurfacemodel.PublicResourceReadAction()}
	if selectedRuntimeSchemaCapabilities(selected).Uploads {
		actions = append(actions, hostsurfacemodel.PublicResourceFileReadAction())
	}
	return actions
}

func runtimeModuleInventoryAction() actioncontract.ActionDefinition {
	return hostsurfacemodel.ModuleInventoryAction()
}

func runtimePermissionUsageQueryAction(identityAudience string) actioncontract.ActionDefinition {
	return hostsurfacemodel.PermissionUsageQueryAction(identityAudience)
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

func (s runtimePublicResourceHTTPAdapter) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /public-resources/{resourceKey}/{accessKey}", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("Cache-Control", "no-store")
		if s.runtime == nil || s.runtime.publicResources == nil {
			response.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(response).Encode(map[string]string{"code": "runtime.public_resource_unavailable"})
			return
		}
		resource, err := s.runtime.publicResources.Get(request.Context(), request.PathValue("resourceKey"), request.PathValue("accessKey"))
		if err != nil {
			writePublicResourceError(response, err)
			return
		}
		_ = json.NewEncoder(response).Encode(resource)
	})
	if !s.runtime.effectiveSchemaCapabilities().Uploads {
		return mux
	}
	mux.HandleFunc("GET /public-resources/{resourceKey}/{accessKey}/files/{fieldKey}", func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Cache-Control", "no-store")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("Content-Security-Policy", "sandbox")
		if s.runtime == nil || s.runtime.publicResources == nil {
			response.Header().Set("Content-Type", "application/json")
			response.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(response).Encode(map[string]string{"code": "runtime.public_resource_unavailable"})
			return
		}
		file, err := s.runtime.publicResources.OpenFile(request.Context(), request.PathValue("resourceKey"), request.PathValue("accessKey"), request.PathValue("fieldKey"))
		if err != nil {
			response.Header().Set("Content-Type", "application/json")
			writePublicResourceError(response, err)
			return
		}
		disposition := "attachment"
		if strings.HasPrefix(strings.ToLower(file.ContentType), "image/") {
			disposition = "inline"
		}
		response.Header().Set("Content-Type", file.ContentType)
		response.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": file.Filename}))
		response.Header().Set("Content-Length", fmt.Sprint(len(file.Content)))
		_, _ = response.Write(file.Content)
	})
	return mux
}

func writePublicResourceError(response http.ResponseWriter, err error) {
	if errors.Is(err, publicresourceapplication.ErrNotFound) || errors.Is(err, publicresourceapplication.ErrRequestInvalid) {
		response.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(response).Encode(map[string]string{"code": "runtime.public_resource_not_found"})
		return
	}
	response.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(response).Encode(map[string]string{"code": "runtime.public_resource_unavailable"})
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
var _ modulehttp.Adapter = runtimePublicResourceHTTPAdapter{}
