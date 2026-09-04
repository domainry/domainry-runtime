package publicationhandoff

import "net/http"

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /publication-handoffs/{messageID}", h.authenticated(h.getBusinessPublicationHandoff))
}
