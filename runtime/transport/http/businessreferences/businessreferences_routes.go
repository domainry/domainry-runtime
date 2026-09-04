package businessreferences

import "net/http"

func (h *BusinessReferencesHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /business-references/graph", h.businessReferenceGraph)
	mux.HandleFunc("GET /business-references/{resourceType}/{resourceKey}", h.businessReferenceImpact)
}
