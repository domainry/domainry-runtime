package capabilities

import "net/http"

func (h *CapabilitiesHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /tenant-admin/platform-capabilities", h.platformCapabilities)
	mux.HandleFunc("GET /tenant-admin/platform-capabilities/index", h.capabilityIndex)
	mux.HandleFunc("GET /tenant-admin/platform-capabilities/domains/{domainKey}", h.capabilityDomain)
	mux.HandleFunc("GET /tenant-admin/platform-capabilities/capabilities/{capabilityKey}", h.capabilityDetail)
	mux.HandleFunc("GET /tenant-admin/platform-capabilities/references/{kind}", h.capabilityReferences)
}
