package integrations

import "net/http"

func (h *IntegrationsHandler) RegisterRoutes(mux *http.ServeMux) {
	authenticated := h.authenticated
	if authenticated == nil {
		authenticated = h.admin
		h.authenticated = authenticated
	}
	mux.HandleFunc("GET /business/integration-intents/{messageID}", h.authenticated(h.getBusinessIntegrationIntent))
	mux.HandleFunc("GET /business/notifications/web-push/readiness", h.authenticated(h.webPushReadiness))
	mux.HandleFunc("GET /business/notifications/web-push/subscriptions", h.authenticated(h.listWebPushSubscriptions))
	mux.HandleFunc("PUT /business/notifications/web-push/subscriptions/{subscriptionID}", h.authenticated(h.upsertWebPushSubscription))
	mux.HandleFunc("POST /business/notifications/web-push/subscriptions/{subscriptionID}/revoke", h.authenticated(h.revokeWebPushSubscription))
	mux.HandleFunc("POST /integrations/web-push/subscriptions/cleanup-expired", h.admin(h.cleanupWebPushSubscriptions))
}
