package appschema

import "net/http"

func (h *ApplicationSchemaHandler) metadataMigrationPlan(w http.ResponseWriter, r *http.Request) {
	steps, err := h.runtimeCatalog.ApplicationSchemaMigrationPlan(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"steps": steps, "count": len(steps)})
}

func (h *ApplicationSchemaHandler) metadataObjectRecordCount(w http.ResponseWriter, r *http.Request) {
	count, err := h.runtimeCatalog.ApplicationSchemaObjectRecordCount(r.Context(), r.PathValue("objectKey"), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"object_key": r.PathValue("objectKey"), "count": count})
}
