package integrations

import "net/http"

func (h *IntegrationsHandler) RegisterRoutes(mux *http.ServeMux) {
	authenticated := h.authenticated
	if authenticated == nil {
		authenticated = h.admin
		h.authenticated = authenticated
	}
	mux.HandleFunc("GET /business/integration-intents/{messageID}", h.authenticated(h.getBusinessIntegrationIntent))
}
