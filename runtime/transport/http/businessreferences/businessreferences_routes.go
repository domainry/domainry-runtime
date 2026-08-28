package businessreferences

import "net/http"

func (h *BusinessReferencesHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /domain-reference-graph", h.businessReferenceGraph)
	mux.HandleFunc("GET /domain-references/{resourceType}/{resourceKey}", h.businessReferenceImpact)
}
