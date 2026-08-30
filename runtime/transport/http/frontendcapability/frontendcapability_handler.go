package frontendcapability

import (
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	"net/http"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type FrontendCapabilityHandler struct {
	service           *deploymentapplication.DeploymentFrontendCapabilityApplicationService
	principal         func(*http.Request) principalmodel.Principal
	writeJSON         func(http.ResponseWriter, int, any)
	writeError        func(http.ResponseWriter, *http.Request, int, string, ...string)
	writeServiceError func(http.ResponseWriter, *http.Request, error)
	decodeJSON        func(http.ResponseWriter, *http.Request, any) bool
	admin             func(http.HandlerFunc) http.HandlerFunc
	authenticated     func(http.HandlerFunc) http.HandlerFunc
}

type FrontendCapabilityDependencies struct {
	Service           *deploymentapplication.DeploymentFrontendCapabilityApplicationService
	Principal         func(*http.Request) principalmodel.Principal
	WriteJSON         func(http.ResponseWriter, int, any)
	WriteError        func(http.ResponseWriter, *http.Request, int, string, ...string)
	WriteServiceError func(http.ResponseWriter, *http.Request, error)
	DecodeJSON        func(http.ResponseWriter, *http.Request, any) bool
	Admin             func(http.HandlerFunc) http.HandlerFunc
	Authenticated     func(http.HandlerFunc) http.HandlerFunc
}

func NewFrontendCapabilityHandler(deps FrontendCapabilityDependencies) *FrontendCapabilityHandler {
	return &FrontendCapabilityHandler{
		service: deps.Service, principal: deps.Principal, writeJSON: deps.WriteJSON,
		writeError: deps.WriteError, writeServiceError: deps.WriteServiceError,
		decodeJSON: deps.DecodeJSON, admin: deps.Admin, authenticated: deps.Authenticated,
	}
}

func (h *FrontendCapabilityHandler) getOpsStatus(w http.ResponseWriter, r *http.Request) {
	principal := h.principal(r)
	if !principal.Known || !principal.HasExactPermission("runtime_ops.capability_status.read") {
		h.writeError(w, r, http.StatusForbidden, "auth.permission_denied")
		return
	}
	result, err := h.service.Snapshot(r.Context(), principal)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *FrontendCapabilityHandler) validateManifest(w http.ResponseWriter, r *http.Request) {
	var request deploymentmodel.FrontendCapabilityManifest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	result, err := h.service.ValidateManifest(r.Context(), request, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *FrontendCapabilityHandler) getManifest(w http.ResponseWriter, r *http.Request) {
	principal := h.principal(r)
	if !principal.Known || !principal.HasPermission("workspace.admin") {
		h.writeError(w, r, http.StatusForbidden, "auth.permission_denied")
		return
	}
	result, err := h.service.Snapshot(r.Context(), principal)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}
