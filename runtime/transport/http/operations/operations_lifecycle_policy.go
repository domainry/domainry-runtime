package operations

import (
	"net/http"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

func (h *OperationsHandler) lifecyclePolicies(w http.ResponseWriter, r *http.Request) {
	items, err := h.lifecycle.ListPolicies(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (h *OperationsHandler) lifecyclePublishPolicy(w http.ResponseWriter, r *http.Request) {
	var input lifecyclemodel.PolicyVersion
	if !h.decodeJSON(w, r, &input) {
		return
	}
	if input.Status == "" {
		input.Status = lifecyclemodel.PolicyStatusPublished
	}
	if input.PublishedAt.IsZero() {
		input.PublishedAt = time.Now().UTC()
	}
	result, err := h.lifecycle.PublishPolicy(r.Context(), input, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, result)
}

func (h *OperationsHandler) lifecycleCreateLegalHold(w http.ResponseWriter, r *http.Request) {
	var input lifecyclemodel.LegalHold
	if !h.decodeJSON(w, r, &input) {
		return
	}
	input.WorkspaceID = h.principal(r).WorkspaceID
	result, err := h.lifecycle.CreateLegalHold(r.Context(), input, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, result)
}

func (h *OperationsHandler) lifecycleEndLegalHold(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Authority string    `json:"authority"`
		Evidence  string    `json:"evidence"`
		EndedAt   time.Time `json:"ended_at"`
	}
	if !h.decodeJSON(w, r, &input) {
		return
	}
	principal := h.principal(r)
	result, err := h.lifecycle.EndLegalHold(r.Context(), principal.WorkspaceID, r.PathValue("holdID"), input.Authority, input.Evidence, input.EndedAt, principal)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}
