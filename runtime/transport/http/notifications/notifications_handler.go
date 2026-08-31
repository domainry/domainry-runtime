package notifications

import (
	"context"
	"net/http"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// NotificationsHandler exposes only Runtime's Integration Outbox delivery
// ledger. Notification product administration and Inbox HTTP are module-owned.
type NotificationsHandler struct {
	deliveryLedger    NotificationDeliveryLedger
	principal         func(*http.Request) principalmodel.Principal
	writeJSON         func(http.ResponseWriter, int, any)
	writeError        func(http.ResponseWriter, *http.Request, int, string, ...string)
	writeServiceError func(http.ResponseWriter, *http.Request, error)
	authenticated     func(http.HandlerFunc) http.HandlerFunc
}

type NotificationsDependencies struct {
	DeliveryLedger    NotificationDeliveryLedger
	Principal         func(*http.Request) principalmodel.Principal
	WriteJSON         func(http.ResponseWriter, int, any)
	WriteError        func(http.ResponseWriter, *http.Request, int, string, ...string)
	WriteServiceError func(http.ResponseWriter, *http.Request, error)
	Authenticated     func(http.HandlerFunc) http.HandlerFunc
}

func NewNotificationsHandler(deps NotificationsDependencies) *NotificationsHandler {
	return &NotificationsHandler{
		deliveryLedger: deps.DeliveryLedger,
		principal:      deps.Principal, writeJSON: deps.WriteJSON, writeError: deps.WriteError,
		writeServiceError: deps.WriteServiceError, authenticated: deps.Authenticated,
	}
}

type NotificationDeliveryLedger interface {
	ListIntegrationOutboxMessages(context.Context, string, string, int, principalmodel.Principal) ([]integrationmodel.IntegrationOutboxMessage, error)
}

func (h *NotificationsHandler) require(w http.ResponseWriter, r *http.Request, permission string) bool {
	principal := h.principal(r)
	if principal.Known && (principal.HasPermission("workspace.admin") || principal.HasPermission(permission)) {
		return true
	}
	h.writeError(w, r, http.StatusForbidden, "auth.permission_denied")
	return false
}
