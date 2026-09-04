package publicationhandoff

import "net/http"

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /publication-handoff/messages/{messageID}", h.authenticated(h.getBusinessPublicationHandoff))
}
