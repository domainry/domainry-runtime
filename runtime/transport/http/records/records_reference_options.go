package records

import (
	"net/http"
	"strings"

	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
)

func (h *RecordsHandler) referenceOptions(w http.ResponseWriter, r *http.Request) {
	request := recordapplication.RecordReferenceOptionRequest{
		Query: strings.TrimSpace(r.URL.Query().Get("query")), Page: intQuery(r.URL.Query().Get("page")), Limit: intQuery(r.URL.Query().Get("limit")),
		Locale: recordRequestLocale(r), FallbackLocale: recordFallbackLocale(r),
	}
	page, err := h.queries.ReferenceOptions(
		r.Context(), strings.TrimSpace(r.PathValue("objectKey")), strings.TrimSpace(r.PathValue("fieldKey")), request, h.principal(r),
	)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, page)
}
