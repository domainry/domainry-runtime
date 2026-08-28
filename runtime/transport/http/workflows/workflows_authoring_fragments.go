package workflows

import (
	"net/http"
	"strings"
)

func (h *WorkflowsHandler) validateAuthoringFragment(w http.ResponseWriter, r *http.Request) {
	payload := map[string]any{}
	if !h.decodeJSON(w, r, &payload) {
		return
	}
	result, err := h.definitions.ValidateAuthoringFragment(r.Context(), strings.TrimSpace(r.PathValue("capabilityKey")), payload, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}
