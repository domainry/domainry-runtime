package openapi

import "net/http"

func (h *OpenAPIHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /openapi.json", h.openAPISpec)
}
