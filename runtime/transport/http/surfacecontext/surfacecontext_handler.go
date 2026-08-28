package surfacecontext

import (
	surfacecontextapplication "github.com/domainry/domainry-runtime/runtime/application/surfacecontext"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	surfacecontextmodel "github.com/domainry/domainry-runtime/runtime/domain/surfacecontext/model"

	"net/http"
	"strings"
)

type SurfaceContextHandler struct {
	service    *surfacecontextapplication.SurfaceContextApplicationService
	principal  func(*http.Request) principalmodel.Principal
	writeJSON  func(http.ResponseWriter, int, any)
	writeError func(http.ResponseWriter, *http.Request, int, string, ...string)
	decodeJSON func(http.ResponseWriter, *http.Request, any) bool
}

type SurfaceContextDependencies struct {
	Service    *surfacecontextapplication.SurfaceContextApplicationService
	Principal  func(*http.Request) principalmodel.Principal
	WriteJSON  func(http.ResponseWriter, int, any)
	WriteError func(http.ResponseWriter, *http.Request, int, string, ...string)
	DecodeJSON func(http.ResponseWriter, *http.Request, any) bool
}

func NewSurfaceContextHandler(deps SurfaceContextDependencies) *SurfaceContextHandler {
	return &SurfaceContextHandler{service: deps.Service, principal: deps.Principal, writeJSON: deps.WriteJSON, writeError: deps.WriteError, decodeJSON: deps.DecodeJSON}
}

func (h *SurfaceContextHandler) surfaceContext(w http.ResponseWriter, r *http.Request) {
	surfaceKey := strings.TrimSpace(r.PathValue("surfaceKey"))
	if surfaceKey == "" {
		h.writeError(w, r, http.StatusBadRequest, "bad_request", "surface key is required")
		return
	}
	var request surfacecontextmodel.SurfaceContextRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	request.SurfaceKey = surfaceKey
	h.writeJSON(w, http.StatusOK, h.service.Context(r.Context(), request, h.principal(r)))
}
