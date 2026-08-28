package party

import (
	"net/http"
	"strings"

	partymodel "github.com/domainry/domainry-runtime/runtime/domain/party/model"
)

func (h *PartyHandler) listJobs(w http.ResponseWriter, r *http.Request) {
	values, err := h.dependencies.Catalog.ListJobs(r.Context(), h.dependencies.Principal(r))
	if err != nil {
		h.dependencies.WriteServiceError(w, r, err)
		return
	}
	h.dependencies.WriteJSON(w, http.StatusOK, map[string]any{"items": values, "total": len(values)})
}

func (h *PartyHandler) getJob(w http.ResponseWriter, r *http.Request) {
	value, found, err := h.dependencies.Catalog.GetJob(r.Context(), strings.TrimSpace(r.PathValue("jobID")), h.dependencies.Principal(r))
	if err != nil {
		h.dependencies.WriteServiceError(w, r, err)
		return
	}
	if !found {
		h.dependencies.WriteError(w, r, http.StatusNotFound, "backend.party.job_not_found")
		return
	}
	h.dependencies.WriteJSON(w, http.StatusOK, value)
}

func (h *PartyHandler) upsertJob(w http.ResponseWriter, r *http.Request) {
	value := partymodel.JobCatalogItem{}
	if !h.dependencies.DecodeJSON(w, r, &value) {
		return
	}
	value.ID = strings.TrimSpace(r.PathValue("jobID"))
	result, err := h.dependencies.Catalog.UpsertJob(r.Context(), value, h.dependencies.Principal(r))
	if err != nil {
		h.dependencies.WriteServiceError(w, r, err)
		return
	}
	h.dependencies.SecurityAudit(r, "party_job_upserted", result.ID, map[string]any{"code": result.Code, "status": result.Status})
	h.dependencies.WriteJSON(w, http.StatusOK, result)
}

func (h *PartyHandler) listPositions(w http.ResponseWriter, r *http.Request) {
	values, err := h.dependencies.Catalog.ListPositions(r.Context(), h.dependencies.Principal(r))
	if err != nil {
		h.dependencies.WriteServiceError(w, r, err)
		return
	}
	h.dependencies.WriteJSON(w, http.StatusOK, map[string]any{"items": values, "total": len(values)})
}

func (h *PartyHandler) getPosition(w http.ResponseWriter, r *http.Request) {
	value, found, err := h.dependencies.Catalog.GetPosition(r.Context(), strings.TrimSpace(r.PathValue("positionID")), h.dependencies.Principal(r))
	if err != nil {
		h.dependencies.WriteServiceError(w, r, err)
		return
	}
	if !found {
		h.dependencies.WriteError(w, r, http.StatusNotFound, "backend.party.position_not_found")
		return
	}
	h.dependencies.WriteJSON(w, http.StatusOK, value)
}

func (h *PartyHandler) upsertPosition(w http.ResponseWriter, r *http.Request) {
	value := partymodel.Position{}
	if !h.dependencies.DecodeJSON(w, r, &value) {
		return
	}
	value.ID = strings.TrimSpace(r.PathValue("positionID"))
	result, err := h.dependencies.Catalog.UpsertPosition(r.Context(), value, h.dependencies.Principal(r))
	if err != nil {
		h.dependencies.WriteServiceError(w, r, err)
		return
	}
	h.dependencies.SecurityAudit(r, "party_position_upserted", result.ID, map[string]any{"code": result.Code, "status": result.Status})
	h.dependencies.WriteJSON(w, http.StatusOK, result)
}
