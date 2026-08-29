package party

import (
	"net/http"
	"strings"

	partymodel "github.com/domainry/domainry-party-sdk/contract"
	partyapplication "github.com/domainry/domainry-runtime/runtime/application/party"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type PartyDependencies struct {
	Service           *partyapplication.PartyApplicationService
	Catalog           *partyapplication.PartyCatalogApplicationService
	Authenticated     func(http.HandlerFunc) http.HandlerFunc
	Principal         func(*http.Request) principalmodel.Principal
	WriteJSON         func(http.ResponseWriter, int, any)
	WriteError        func(http.ResponseWriter, *http.Request, int, string, ...string)
	WriteServiceError func(http.ResponseWriter, *http.Request, error)
	DecodeJSON        func(http.ResponseWriter, *http.Request, any) bool
	SecurityAudit     func(*http.Request, string, string, map[string]any)
}

type PartyHandler struct {
	dependencies PartyDependencies
}

func NewPartyHandler(dependencies PartyDependencies) *PartyHandler {
	return &PartyHandler{dependencies: dependencies}
}

func (h *PartyHandler) list(w http.ResponseWriter, r *http.Request) {
	values, err := h.dependencies.Service.List(r.Context(), h.dependencies.Principal(r))
	if err != nil {
		h.dependencies.WriteServiceError(w, r, err)
		return
	}
	h.dependencies.WriteJSON(w, http.StatusOK, map[string]any{"items": values, "total": len(values)})
}

func (h *PartyHandler) get(w http.ResponseWriter, r *http.Request) {
	value, found, err := h.dependencies.Service.Get(r.Context(), strings.TrimSpace(r.PathValue("partyID")), h.dependencies.Principal(r))
	if err != nil {
		h.dependencies.WriteServiceError(w, r, err)
		return
	}
	if !found {
		h.dependencies.WriteError(w, r, http.StatusNotFound, "backend.party.not_found")
		return
	}
	h.dependencies.WriteJSON(w, http.StatusOK, value)
}

func (h *PartyHandler) upsert(w http.ResponseWriter, r *http.Request) {
	value := partymodel.Aggregate{}
	if !h.dependencies.DecodeJSON(w, r, &value) {
		return
	}
	value.Party.ID = strings.TrimSpace(r.PathValue("partyID"))
	result, err := h.dependencies.Service.Upsert(r.Context(), value, h.dependencies.Principal(r))
	if err != nil {
		h.dependencies.WriteServiceError(w, r, err)
		return
	}
	h.dependencies.SecurityAudit(r, "party_upserted", result.Party.ID, map[string]any{"kind": result.Party.Kind, "status": result.Party.Status})
	h.dependencies.WriteJSON(w, http.StatusOK, result)
}
