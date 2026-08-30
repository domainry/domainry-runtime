package appschema

import (
	"net/http"
	"strings"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

func (h *ApplicationSchemaHandler) listLocalizedTexts(w http.ResponseWriter, r *http.Request) {
	principal := h.principal(r)
	query := localizedTextQuery(r, principal.WorkspaceID)
	values, err := h.localizedTexts.ListLocalizedTexts(r.Context(), query, principal)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeMetadataCachedJSON(w, r, map[string]any{"localized_texts": values})
}
func (h *ApplicationSchemaHandler) localizedTextCoverage(w http.ResponseWriter, r *http.Request) {
	result, err := h.localizedTexts.LocalizedTextCoverage(r.Context(), strings.TrimSpace(r.URL.Query().Get("locale")), strings.TrimSpace(r.URL.Query().Get("fallback_locale")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeMetadataCachedJSON(w, r, result)
}
func (h *ApplicationSchemaHandler) exportLocalizedTexts(w http.ResponseWriter, r *http.Request) {
	principal := h.principal(r)
	values, err := h.localizedTexts.ListLocalizedTexts(r.Context(), localizedTextQuery(r, principal.WorkspaceID), principal)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	payload := localizedTextCSV(values)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="localized-texts.csv"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}
func (h *ApplicationSchemaHandler) exportLocalizedTextsXLSX(w http.ResponseWriter, r *http.Request) {
	principal := h.principal(r)
	values, err := h.localizedTexts.ListLocalizedTexts(r.Context(), localizedTextQuery(r, principal.WorkspaceID), principal)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	payload := localizedTextXLSX(values)
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", `attachment; filename="localized-texts.xlsx"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}
func localizedTextQuery(r *http.Request, defaultWorkspaceID string) appschemamodel.LocalizedTextQuery {
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	if workspaceID == "" {
		workspaceID = strings.TrimSpace(defaultWorkspaceID)
	}
	return appschemamodel.LocalizedTextQuery{WorkspaceID: workspaceID, EntityType: strings.TrimSpace(r.URL.Query().Get("entity_type")), EntityKey: strings.TrimSpace(r.URL.Query().Get("entity_key")), Property: strings.TrimSpace(r.URL.Query().Get("property")), Locale: strings.TrimSpace(r.URL.Query().Get("locale"))}
}
