package notifications

import (
	"context"
	"net/http"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// NotificationsHandler contains only Runtime-owned Notification BFF routes:
// the cross-surface Inbox projection and Runtime Integration Outbox ledger.
// Notification product administration HTTP is owned by the module Surface.
type NotificationsHandler struct {
	inbox              NotificationInbox
	deliveryLedger     NotificationDeliveryLedger
	principal          func(*http.Request) principalmodel.Principal
	writeJSON          func(http.ResponseWriter, int, any)
	writeError         func(http.ResponseWriter, *http.Request, int, string, ...string)
	writeServiceError  func(http.ResponseWriter, *http.Request, error)
	decodeJSON         func(http.ResponseWriter, *http.Request, any) bool
	authenticated      func(http.HandlerFunc) http.HandlerFunc
	streamPollInterval time.Duration
	streamHeartbeat    time.Duration
}

type NotificationsDependencies struct {
	Inbox              NotificationInbox
	DeliveryLedger     NotificationDeliveryLedger
	Principal          func(*http.Request) principalmodel.Principal
	WriteJSON          func(http.ResponseWriter, int, any)
	WriteError         func(http.ResponseWriter, *http.Request, int, string, ...string)
	WriteServiceError  func(http.ResponseWriter, *http.Request, error)
	DecodeJSON         func(http.ResponseWriter, *http.Request, any) bool
	Authenticated      func(http.HandlerFunc) http.HandlerFunc
	StreamPollInterval time.Duration
	StreamHeartbeat    time.Duration
}

func NewNotificationsHandler(deps NotificationsDependencies) *NotificationsHandler {
	poll, heartbeat := deps.StreamPollInterval, deps.StreamHeartbeat
	if poll <= 0 {
		poll = 2 * time.Second
	}
	if heartbeat <= 0 {
		heartbeat = 15 * time.Second
	}
	return &NotificationsHandler{
		inbox: deps.Inbox, deliveryLedger: deps.DeliveryLedger,
		principal: deps.Principal, writeJSON: deps.WriteJSON, writeError: deps.WriteError,
		writeServiceError: deps.WriteServiceError, decodeJSON: deps.DecodeJSON,
		authenticated: deps.Authenticated, streamPollInterval: poll, streamHeartbeat: heartbeat,
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
