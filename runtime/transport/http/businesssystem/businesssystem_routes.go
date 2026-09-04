package businesssystem

import "net/http"

func (h *BusinessSystemHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /business-system/snapshot", h.businessSystemSnapshot)
	mux.HandleFunc("POST /business-system/validation", h.validateRuntimeAuthoring)
	mux.HandleFunc("POST /business-system/delivery-verification", h.verifyRuntimeAuthoringDelivery)
}
