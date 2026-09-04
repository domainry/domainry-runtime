package capabilities

import "net/http"

func (h *CapabilitiesHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /capabilities", h.platformCapabilities)
	mux.HandleFunc("GET /capabilities/index", h.capabilityIndex)
	mux.HandleFunc("GET /capabilities/domains/{domainKey}", h.capabilityDomain)
	mux.HandleFunc("GET /capabilities/{capabilityKey}", h.capabilityDetail)
	mux.HandleFunc("GET /capabilities/references/{kind}", h.capabilityReferences)
}
