package publicationhandoff

import (
	"net/http"
	"strings"
)

func (h *Handler) getBusinessPublicationHandoff(w http.ResponseWriter, r *http.Request) {
	result, err := h.intents.GetBusinessPublicationHandoff(r.Context(), strings.TrimSpace(r.PathValue("messageID")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}
