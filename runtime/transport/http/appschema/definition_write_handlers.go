package appschema

import (
	"net/http"
	"strings"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

func (h *ApplicationSchemaHandler) validateApplicationDefinition(w http.ResponseWriter, r *http.Request) {
	var req appschemamodel.ApplicationDefinitionUpsertRequest
	if !h.decodeJSON(w, r, &req) {
		return
	}
	result, err := h.definitions.ValidateApplicationDefinition(r.Context(), strings.TrimSpace(r.PathValue("resourceType")), strings.TrimSpace(r.PathValue("resourceKey")), req, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeMetadataAuthoringValidationResult(w, r, result)
}
