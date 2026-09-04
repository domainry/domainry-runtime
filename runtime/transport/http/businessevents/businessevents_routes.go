package businessevents

import "net/http"

func (h *BusinessEventsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /business-events/stream", h.stream)
}
