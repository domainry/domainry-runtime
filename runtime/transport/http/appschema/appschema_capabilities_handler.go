package appschema

import "net/http"

func (h *ApplicationSchemaHandler) listCapabilities(w http.ResponseWriter, r *http.Request) {
	projection, err := h.capabilities.ApplicationSchemaProjection(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.legacyHeaders(w)
	h.writeJSON(w, http.StatusOK, projection)
}

func (h *ApplicationSchemaHandler) executionCapabilities(w http.ResponseWriter, r *http.Request) {
	catalog, err := h.capabilities.ExecutionCapabilities(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.legacyHeaders(w)
	h.writeJSON(w, http.StatusOK, catalog)
}
