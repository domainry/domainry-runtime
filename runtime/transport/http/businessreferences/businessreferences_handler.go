package businessreferences

import (
	changeplanapplication "github.com/domainry/domainry-runtime/runtime/application/changeplan"
	"net/http"
	"strings"

	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanprojection "github.com/domainry/domainry-runtime/runtime/domain/changeplan/projection"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type BusinessReferencesHandler struct {
	service           *changeplanapplication.ChangePlanReferenceApplicationService
	principal         func(*http.Request) principalmodel.Principal
	writeJSON         func(http.ResponseWriter, int, any)
	writeError        func(http.ResponseWriter, *http.Request, int, string, ...string)
	writeServiceError func(http.ResponseWriter, *http.Request, error)
}

type BusinessReferencesDependencies struct {
	Service           *changeplanapplication.ChangePlanReferenceApplicationService
	Principal         func(*http.Request) principalmodel.Principal
	WriteJSON         func(http.ResponseWriter, int, any)
	WriteError        func(http.ResponseWriter, *http.Request, int, string, ...string)
	WriteServiceError func(http.ResponseWriter, *http.Request, error)
}

func NewBusinessReferencesHandler(deps BusinessReferencesDependencies) *BusinessReferencesHandler {
	return &BusinessReferencesHandler{
		service: deps.Service, principal: deps.Principal,
		writeJSON: deps.WriteJSON, writeError: deps.WriteError, writeServiceError: deps.WriteServiceError,
	}
}

func (h *BusinessReferencesHandler) Graph(r *http.Request) (changeplanmodel.ReferenceGraph, error) {
	graph, err := h.service.Graph(r.Context(), h.principal(r))
	if err != nil {
		return changeplanmodel.ReferenceGraph{}, err
	}
	return graph, nil
}

func (h *BusinessReferencesHandler) businessReferenceGraph(w http.ResponseWriter, r *http.Request) {
	graph, err := h.Graph(r)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, graph)
}

func (h *BusinessReferencesHandler) businessReferenceImpact(w http.ResponseWriter, r *http.Request) {
	graph, err := h.Graph(r)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	resourceType := strings.TrimSpace(r.PathValue("resourceType"))
	resourceKey := strings.TrimSpace(r.PathValue("resourceKey"))
	if resourceType == "" || resourceKey == "" {
		h.writeError(w, r, http.StatusBadRequest, "backend.reference.identity_required")
		return
	}
	h.writeJSON(w, http.StatusOK, changeplanprojection.ChangePlanReferenceImpact(graph, resourceType, resourceKey))
}
