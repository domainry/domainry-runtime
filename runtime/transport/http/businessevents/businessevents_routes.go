package businessevents

import "net/http"

func (h *BusinessEventsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /events/business", h.stream)
}
