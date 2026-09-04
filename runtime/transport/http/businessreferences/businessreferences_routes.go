package businessreferences

import "net/http"

func (h *BusinessReferencesHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /references", h.businessReferenceGraph)
	mux.HandleFunc("GET /references/{resourceType}/{resourceKey}/impact", h.businessReferenceImpact)
}
