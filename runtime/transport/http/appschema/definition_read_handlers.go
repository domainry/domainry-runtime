package appschema

import (
	"net/http"
	"strings"
)

func (h *ApplicationSchemaHandler) getApplicationDefinition(w http.ResponseWriter, r *http.Request) {
	definition, found, err := h.definitions.GetApplicationDefinition(r.Context(), strings.TrimSpace(r.PathValue("resourceType")), strings.TrimSpace(r.PathValue("resourceKey")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if !found {
		h.writeError(w, r, http.StatusNotFound, "backend.metadata.definition_not_found")
		return
	}
	h.writeMetadataCachedJSON(w, r, definition)
}
