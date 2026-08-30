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

func (h *ApplicationSchemaHandler) listApplicationDefinitions(w http.ResponseWriter, r *http.Request) {
	resourceType := strings.TrimSpace(r.PathValue("resourceType"))
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	principal := h.principal(r)
	if workspaceID == "" {
		workspaceID = strings.TrimSpace(principal.WorkspaceID)
	}
	definitions, err := h.definitions.ListApplicationDefinitions(r.Context(), resourceType, workspaceID, principal)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeMetadataCachedJSON(w, r, map[string]any{"definitions": definitions, "resource_type": resourceType})
}
