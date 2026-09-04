package businesssystem

import "net/http"

func (h *BusinessSystemHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /authoring/snapshot", h.businessSystemSnapshot)
	mux.HandleFunc("POST /authoring/validate", h.validateRuntimeAuthoring)
	mux.HandleFunc("POST /authoring/verify-delivery", h.verifyRuntimeAuthoringDelivery)
}
