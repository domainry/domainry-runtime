package notifications

import (
	"net/http"
	"strconv"
	"strings"

	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	surfacemodel "github.com/domainry/domainry-runtime/runtime/domain/surface/model"
)

func notificationInboxSurface(r *http.Request) surfacemodel.ProductSurface {
	if strings.HasPrefix(r.URL.Path, "/portal/") {
		return surfacemodel.ProductSurfaceConsumerPortal
	}
	return surfacemodel.ProductSurfaceBusinessWorkspace
}

func notificationInboxQuery(r *http.Request) notificationmodel.NotificationInboxQuery {
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	return notificationmodel.NotificationInboxQuery{
		Mailbox: query.Get("mailbox"), Query: query.Get("query"), Categories: query["category"], Sources: query["source"],
		Severities: query["severity"], ActionStates: query["action_state"], From: query.Get("from"), To: query.Get("to"), Limit: limit,
		Scope: query.Get("scope"), RecipientUserID: notificationInboxRecipientFilter(query.Get("scope"), query.Get("team_member_id"), query.Get("delegated_owner_id")),
	}
}

func notificationInboxRecipientFilter(scope, teamMemberID, delegatedOwnerID string) string {
	if strings.TrimSpace(scope) == notificationmodel.NotificationInboxScopeDelegated {
		return delegatedOwnerID
	}
	return teamMemberID
}

func (h *NotificationsHandler) notificationInboxAvailable(w http.ResponseWriter, r *http.Request) bool {
	if h.inbox != nil {
		return true
	}
	h.writeError(w, r, http.StatusServiceUnavailable, "backend.notification.inbox_unavailable")
	return false
}

func (h *NotificationsHandler) listInbox(w http.ResponseWriter, r *http.Request) {
	if !h.notificationInboxAvailable(w, r) {
		return
	}
	value, err := h.inbox.ListInbox(r.Context(), notificationInboxQuery(r), r.URL.Query().Get("cursor"), notificationInboxSurface(r), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}

func (h *NotificationsHandler) getInboxItem(w http.ResponseWriter, r *http.Request) {
	if !h.notificationInboxAvailable(w, r) {
		return
	}
	value, err := h.inbox.GetInboxItem(r.Context(), strings.TrimSpace(r.PathValue("notificationID")), notificationInboxQuery(r), notificationInboxSurface(r), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}

func (h *NotificationsHandler) inboxFacets(w http.ResponseWriter, r *http.Request) {
	if !h.notificationInboxAvailable(w, r) {
		return
	}
	value, err := h.inbox.InboxFacets(r.Context(), notificationInboxQuery(r), notificationInboxSurface(r), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}

func (h *NotificationsHandler) resolveInboxAction(w http.ResponseWriter, r *http.Request) {
	if !h.notificationInboxAvailable(w, r) {
		return
	}
	value, err := h.inbox.ResolveInboxAction(
		r.Context(), strings.TrimSpace(r.PathValue("notificationID")), strings.TrimSpace(r.PathValue("actionKey")),
		notificationInboxQuery(r), notificationInboxSurface(r), h.principal(r),
	)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}

func (h *NotificationsHandler) inboxUnreadCount(w http.ResponseWriter, r *http.Request) {
	if !h.notificationInboxAvailable(w, r) {
		return
	}
	query := notificationInboxQuery(r)
	query.Mailbox = notificationmodel.NotificationMailboxInbox
	value, err := h.inbox.InboxFacets(r.Context(), query, notificationInboxSurface(r), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]int{"unread": value.Unread})
}

func (h *NotificationsHandler) getMyNotificationPreference(w http.ResponseWriter, r *http.Request) {
	if !h.notificationInboxAvailable(w, r) {
		return
	}
	value, err := h.inbox.GetMyNotificationPreference(r.Context(), notificationInboxSurface(r), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}

func (h *NotificationsHandler) saveMyNotificationPreference(w http.ResponseWriter, r *http.Request) {
	if !h.notificationInboxAvailable(w, r) {
		return
	}
	var value notificationmodel.NotificationRecipientPreference
	if !h.decodeJSON(w, r, &value) {
		return
	}
	saved, err := h.inbox.SaveMyNotificationPreference(r.Context(), value, notificationInboxSurface(r), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, saved)
}

func (h *NotificationsHandler) readInboxItem(w http.ResponseWriter, r *http.Request) {
	h.setInboxItemRead(w, r, true)
}

func (h *NotificationsHandler) unreadInboxItem(w http.ResponseWriter, r *http.Request) {
	h.setInboxItemRead(w, r, false)
}

func (h *NotificationsHandler) setInboxItemRead(w http.ResponseWriter, r *http.Request, read bool) {
	if !h.notificationInboxAvailable(w, r) {
		return
	}
	value, err := h.inbox.SetInboxRead(r.Context(), strings.TrimSpace(r.PathValue("notificationID")), read, notificationInboxSurface(r), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}

func (h *NotificationsHandler) archiveInboxItem(w http.ResponseWriter, r *http.Request) {
	h.setInboxItemArchived(w, r, true)
}

func (h *NotificationsHandler) restoreInboxItem(w http.ResponseWriter, r *http.Request) {
	h.setInboxItemArchived(w, r, false)
}

func (h *NotificationsHandler) acknowledgeInboxAlert(w http.ResponseWriter, r *http.Request) {
	if !h.notificationInboxAvailable(w, r) {
		return
	}
	value, err := h.inbox.AcknowledgeInboxAlert(r.Context(), strings.TrimSpace(r.PathValue("notificationID")), notificationInboxSurface(r), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}

func (h *NotificationsHandler) setInboxItemArchived(w http.ResponseWriter, r *http.Request, archived bool) {
	if !h.notificationInboxAvailable(w, r) {
		return
	}
	value, err := h.inbox.SetInboxArchived(r.Context(), strings.TrimSpace(r.PathValue("notificationID")), archived, notificationInboxSurface(r), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}

func (h *NotificationsHandler) readAllInboxItems(w http.ResponseWriter, r *http.Request) {
	if !h.notificationInboxAvailable(w, r) {
		return
	}
	count, err := h.inbox.MarkAllInboxRead(r.Context(), notificationInboxQuery(r), notificationInboxSurface(r), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]int{"updated": count})
}

func (h *NotificationsHandler) listInboxSavedViews(w http.ResponseWriter, r *http.Request) {
	if !h.notificationInboxAvailable(w, r) {
		return
	}
	values, err := h.inbox.ListInboxSavedViews(r.Context(), notificationInboxSurface(r), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"views": values, "count": len(values)})
}

func (h *NotificationsHandler) saveInboxSavedView(w http.ResponseWriter, r *http.Request) {
	if !h.notificationInboxAvailable(w, r) {
		return
	}
	var value notificationmodel.NotificationInboxSavedView
	if !h.decodeJSON(w, r, &value) {
		return
	}
	value.Key = strings.TrimSpace(r.PathValue("viewKey"))
	saved, err := h.inbox.SaveInboxSavedView(r.Context(), value, notificationInboxSurface(r), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, saved)
}

func (h *NotificationsHandler) deleteInboxSavedView(w http.ResponseWriter, r *http.Request) {
	if !h.notificationInboxAvailable(w, r) {
		return
	}
	if err := h.inbox.DeleteInboxSavedView(r.Context(), strings.TrimSpace(r.PathValue("viewKey")), notificationInboxSurface(r), h.principal(r)); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *NotificationsHandler) listInboxDelegations(w http.ResponseWriter, r *http.Request) {
	if !h.notificationInboxAvailable(w, r) {
		return
	}
	values, err := h.inbox.ListMyInboxDelegations(r.Context(), notificationInboxSurface(r), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"delegations": values, "count": len(values)})
}

func (h *NotificationsHandler) saveInboxDelegation(w http.ResponseWriter, r *http.Request) {
	if !h.notificationInboxAvailable(w, r) {
		return
	}
	var value notificationmodel.NotificationInboxDelegation
	if !h.decodeJSON(w, r, &value) {
		return
	}
	value.ID = strings.TrimSpace(r.PathValue("delegationID"))
	saved, err := h.inbox.SaveMyInboxDelegation(r.Context(), value, notificationInboxSurface(r), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, saved)
}

func (h *NotificationsHandler) deleteInboxDelegation(w http.ResponseWriter, r *http.Request) {
	if !h.notificationInboxAvailable(w, r) {
		return
	}
	if err := h.inbox.DeleteMyInboxDelegation(r.Context(), strings.TrimSpace(r.PathValue("delegationID")), notificationInboxSurface(r), h.principal(r)); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *NotificationsHandler) listDelegatedInboxOwners(w http.ResponseWriter, r *http.Request) {
	if !h.notificationInboxAvailable(w, r) {
		return
	}
	owners, err := h.inbox.ListMyDelegatedInboxOwners(r.Context(), notificationInboxSurface(r), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"owner_user_ids": owners, "count": len(owners)})
}
