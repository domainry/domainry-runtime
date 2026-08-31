package notifications

import "net/http"

func (h *NotificationsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /notifications/deliveries", h.authenticated(h.listDeliveries))
}
