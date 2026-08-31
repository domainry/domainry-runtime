package runtime

import (
	"encoding/json"
	"net/http"

	"github.com/domainry/domainry-foundation/modulehttp"
)

// ModuleHTTPSurfaces returns routes owned by in-process module Bindings.
// Runtime-owned orchestration and BFF routes remain on Runtime's HTTP router.
func (runtime *Runtime) ModuleHTTPSurfaces() []modulehttp.Surface {
	if runtime == nil {
		return nil
	}
	bindings := []any{
		runtime.notificationBinding, runtime.partyBinding, runtime.integrationBinding,
		runtime.schedulerBinding, runtime.monitoringBinding, runtime.dataExchangeBinding,
		runtime.agentBinding, runtime.lifecycleBinding, runtime.auditBinding,
		runtime.metadataBinding, runtime.reportBinding,
	}
	var surfaces []modulehttp.Surface
	for _, binding := range bindings {
		if provider, ok := binding.(modulehttp.Provider); ok && provider != nil {
			surfaces = append(surfaces, provider.HTTPSurfaces()...)
		}
	}
	surfaces = append(surfaces, runtimeModuleInventorySurface{runtime: runtime})
	return append([]modulehttp.Surface(nil), surfaces...)
}

type runtimeModuleInventorySurface struct{ runtime *Runtime }

func (runtimeModuleInventorySurface) ContractVersion() string { return modulehttp.ContractVersion }
func (runtimeModuleInventorySurface) Owner() string           { return "runtime" }
func (runtimeModuleInventorySurface) Name() string            { return "module_inventory" }
func (runtimeModuleInventorySurface) Routes() []modulehttp.Route {
	return []modulehttp.Route{{
		Pattern: "GET /operations/modules", Exposures: []modulehttp.Exposure{modulehttp.ExposureOps},
		Authentication: modulehttp.AuthenticationAuthenticated,
		AnyPermissions: []string{"workspace.admin", "runtime_ops.capability_status.read"},
	}}
}
func (s runtimeModuleInventorySurface) Handler() http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
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
}

var _ modulehttp.Surface = runtimeModuleInventorySurface{}
