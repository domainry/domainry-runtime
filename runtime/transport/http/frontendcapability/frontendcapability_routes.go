package frontendcapability

import "net/http"

func (h *FrontendCapabilityHandler) RegisterRoutes(mux *http.ServeMux) {
	authenticated := h.authenticated
	if authenticated == nil {
		authenticated = h.admin
	}
	mux.HandleFunc("GET /frontend-capability-manifest", authenticated(h.getManifest))
	mux.HandleFunc("POST /frontend-capability-manifest/validate", authenticated(h.validateManifest))
	mux.HandleFunc("PUT /frontend-capability-manifest", authenticated(h.registerManifest))
	mux.HandleFunc("GET /operations/capability-status", authenticated(h.getOpsStatus))
}
