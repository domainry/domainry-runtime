package metadata

import (
	"net/http"
	"strings"
)

func (h *MetadataHandler) getDictionaryItems(w http.ResponseWriter, r *http.Request) {
	dictionaryKey := strings.TrimSpace(r.PathValue("dictionaryKey"))
	locale := strings.TrimSpace(r.URL.Query().Get("locale"))
	result, _, err := h.runtimeCatalog.DictionaryItems(r.Context(), dictionaryKey, locale, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=5, must-revalidate")
	h.writeJSON(w, http.StatusOK, result)
}
