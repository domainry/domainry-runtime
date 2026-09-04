package notifications

import "net/http"

func (h *NotificationsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /notification/inbox/{notificationID}/actions/{actionKey}/resolve", h.authenticated(h.resolveInboxAction))
	mux.HandleFunc("GET /notification/deliveries", h.authenticated(h.listDeliveries))
}
