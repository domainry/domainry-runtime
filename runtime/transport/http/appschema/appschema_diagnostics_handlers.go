package appschema

import (
	"net/http"
)

func (h *ApplicationSchemaHandler) opsMetadataDiagnostics(w http.ResponseWriter, r *http.Request) {
	result, err := h.runtimeCatalog.OpsMetadataDiagnostics(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}
