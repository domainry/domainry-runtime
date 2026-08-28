package metadata

import "net/http"

func (h *MetadataHandler) listCapabilities(w http.ResponseWriter, r *http.Request) {
	projection, err := h.capabilities.MetadataProjection(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.legacyHeaders(w)
	h.writeJSON(w, http.StatusOK, projection)
}

func (h *MetadataHandler) executionCapabilities(w http.ResponseWriter, r *http.Request) {
	catalog, err := h.capabilities.ExecutionCapabilities(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.legacyHeaders(w)
	h.writeJSON(w, http.StatusOK, catalog)
}
