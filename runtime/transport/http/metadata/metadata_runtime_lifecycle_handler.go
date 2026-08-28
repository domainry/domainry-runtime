package metadata

import "net/http"

func (h *MetadataHandler) metadataMigrationPlan(w http.ResponseWriter, r *http.Request) {
	steps, err := h.runtimeCatalog.MetadataMigrationPlan(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"steps": steps, "count": len(steps)})
}

func (h *MetadataHandler) metadataObjectRecordCount(w http.ResponseWriter, r *http.Request) {
	count, err := h.runtimeCatalog.MetadataObjectRecordCount(r.Context(), r.PathValue("objectKey"), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"object_key": r.PathValue("objectKey"), "count": count})
}

func (h *MetadataHandler) reloadMetadata(w http.ResponseWriter, r *http.Request) {
	schema, err := h.runtimeCatalog.ReloadMetadata(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"schema": schema})
}
