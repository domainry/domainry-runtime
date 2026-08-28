package metadata

import (
	"context"
	"net/http"
	"strings"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func (h *MetadataHandler) listLocalizedTexts(w http.ResponseWriter, r *http.Request) {
	principal := h.principal(r)
	query := localizedTextQuery(r, principal.WorkspaceID)
	values, err := h.localizedTexts.ListLocalizedTexts(r.Context(), query, principal)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeMetadataCachedJSON(w, r, map[string]any{"localized_texts": values})
}
func (h *MetadataHandler) upsertLocalizedText(w http.ResponseWriter, r *http.Request) {
	var req metadatamodel.LocalizedTextUpsertRequest
	if !h.decodeJSON(w, r, &req) {
		return
	}
	principal := h.principal(r)
	if strings.TrimSpace(req.WorkspaceID) == "" {
		req.WorkspaceID = strings.TrimSpace(principal.WorkspaceID)
	}
	value, err := h.localizedTexts.UpsertLocalizedText(r.Context(), req, principal)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"localized_text": value})
}
func (h *MetadataHandler) localizedTextCoverage(w http.ResponseWriter, r *http.Request) {
	result, err := h.localizedTexts.LocalizedTextCoverage(r.Context(), strings.TrimSpace(r.URL.Query().Get("locale")), strings.TrimSpace(r.URL.Query().Get("fallback_locale")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeMetadataCachedJSON(w, r, result)
}
func (h *MetadataHandler) exportLocalizedTexts(w http.ResponseWriter, r *http.Request) {
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
func (h *MetadataHandler) exportLocalizedTextsXLSX(w http.ResponseWriter, r *http.Request) {
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
func localizedTextQuery(r *http.Request, defaultWorkspaceID string) metadatamodel.LocalizedTextQuery {
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	if workspaceID == "" {
		workspaceID = strings.TrimSpace(defaultWorkspaceID)
	}
	return metadatamodel.LocalizedTextQuery{WorkspaceID: workspaceID, EntityType: strings.TrimSpace(r.URL.Query().Get("entity_type")), EntityKey: strings.TrimSpace(r.URL.Query().Get("entity_key")), Property: strings.TrimSpace(r.URL.Query().Get("property")), Locale: strings.TrimSpace(r.URL.Query().Get("locale"))}
}
func (h *MetadataHandler) importLocalizedTexts(w http.ResponseWriter, r *http.Request) {
	requests, err := localizedTextUpsertRequestsFromCSV(r.Body)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "metadata.localized_texts.invalid_csv", "detail", err.Error())
		return
	}
	principal := h.principal(r)
	allowed, err := h.localizedTextImportAllowedKeys(r.Context(), principal)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if err := validateLocalizedTextUpsertRequests(requests, allowed); err != nil {
		h.writeError(w, r, http.StatusBadRequest, "metadata.localized_texts.invalid_csv", "detail", err.Error())
		return
	}
	imported := make([]metadatamodel.LocalizedText, 0, len(requests))
	for _, req := range requests {
		if strings.TrimSpace(req.WorkspaceID) == "" {
			req.WorkspaceID = strings.TrimSpace(principal.WorkspaceID)
		}
		value, err := h.localizedTexts.UpsertLocalizedText(r.Context(), req, principal)
		if err != nil {
			h.writeServiceError(w, r, err)
			return
		}
		imported = append(imported, value)
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"localized_texts": imported, "count": len(imported)})
}
func (h *MetadataHandler) localizedTextImportAllowedKeys(ctx context.Context, principal principalmodel.Principal) (map[string]bool, error) {
	coverage, err := h.localizedTexts.LocalizedTextCoverage(ctx, "__validation__", "", principal)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, item := range coverage.Items {
		allowed[localizedTextImportKey(item.EntityType, item.EntityKey, item.Property)] = true
	}
	return allowed, nil
}
