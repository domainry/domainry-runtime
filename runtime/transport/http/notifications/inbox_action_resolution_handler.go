package notifications

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	surfacemodel "github.com/domainry/domainry-runtime/runtime/domain/surface/model"
)

// NotificationInboxActionResolver is the Runtime-owned cross-resource BFF
// seam. Notification owns Inbox state; Runtime authorizes the resolved target
// against the current record, workflow, report, or other resource policy.
type NotificationInboxActionResolver interface {
	ResolveInboxAction(context.Context, string, string, notificationmodel.NotificationInboxQuery, surfacemodel.ProductSurface, principalmodel.Principal) (notificationmodel.NotificationInboxResolvedAction, error)
}

func (h *NotificationsHandler) resolveInboxAction(w http.ResponseWriter, r *http.Request) {
	if h.actionResolver == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "backend.notification.inbox_action_unavailable")
		return
	}
	value, err := h.actionResolver.ResolveInboxAction(
		r.Context(), strings.TrimSpace(r.PathValue("notificationID")), strings.TrimSpace(r.PathValue("actionKey")),
		notificationInboxActionQuery(r), notificationInboxActionSurface(r), h.principal(r),
	)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}

func notificationInboxActionSurface(r *http.Request) surfacemodel.ProductSurface {
	if strings.HasPrefix(r.URL.Path, "/portal/") {
		return surfacemodel.ProductSurfaceConsumerPortal
	}
	return surfacemodel.ProductSurfaceBusinessWorkspace
}

func notificationInboxActionQuery(r *http.Request) notificationmodel.NotificationInboxQuery {
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	memberID := query.Get("team_member_id")
	if strings.TrimSpace(query.Get("scope")) == notificationmodel.NotificationInboxScopeDelegated {
		memberID = query.Get("delegated_owner_id")
	}
	return notificationmodel.NotificationInboxQuery{
		Mailbox: query.Get("mailbox"), Query: query.Get("query"), Categories: query["category"], Sources: query["source"],
		Severities: query["severity"], ActionStates: query["action_state"], From: query.Get("from"), To: query.Get("to"),
		Limit: limit, Scope: query.Get("scope"), TeamMemberID: memberID,
	}
}
