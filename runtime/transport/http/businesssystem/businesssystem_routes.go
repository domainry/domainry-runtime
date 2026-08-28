package businesssystem

import "net/http"

func (h *BusinessSystemHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /domain-system-snapshot", h.businessSystemSnapshot)
	mux.HandleFunc("POST /domain-system-validation", h.validateRuntimeAuthoring)
	mux.HandleFunc("POST /domain-system-delivery-verification", h.verifyRuntimeAuthoringDelivery)
}
