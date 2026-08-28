package businessseeds

import "net/http"

func (h *BusinessSeedHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /business-seeds/{seedKey}/validate", h.validate)
	mux.HandleFunc("PUT /business-seeds/{seedKey}", h.apply)
	mux.HandleFunc("GET /business-seeds/{seedKey}", h.get)
	mux.HandleFunc("GET /business-seeds/{seedKey}/versions", h.versions)
}
