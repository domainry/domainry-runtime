package notifications

import "net/http"

func (h *NotificationsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /business/notifications/{notificationID}/actions/{actionKey}/resolve", h.authenticated(h.resolveInboxAction))
	mux.HandleFunc("GET /portal/notifications/{notificationID}/actions/{actionKey}/resolve", h.authenticated(h.resolveInboxAction))
	mux.HandleFunc("GET /notifications/deliveries", h.authenticated(h.listDeliveries))
}
