package notifications

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	surfacemodel "github.com/domainry/domainry-runtime/runtime/domain/surface/model"
)

type notificationInboxSync struct {
	Cursor    string `json:"cursor"`
	Unread    int    `json:"unread"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

func (h *NotificationsHandler) streamInbox(w http.ResponseWriter, r *http.Request) {
	if !h.notificationInboxAvailable(w, r) {
		return
	}
	surface, principal := notificationInboxSurface(r), h.principal(r)
	state, err := h.inboxSyncState(r, surface, principal)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	lastCursor := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if value := strings.TrimSpace(r.URL.Query().Get("cursor")); value != "" {
		lastCursor = value
	}
	if state.Cursor != lastCursor {
		if !writeNotificationInboxSSE(w, "notification.sync", state.Cursor, state) {
			return
		}
		lastCursor = state.Cursor
	} else if !writeNotificationInboxSSE(w, "notification.ready", state.Cursor, state) {
		return
	}
	poll := time.NewTicker(h.streamPollInterval)
	heartbeat := time.NewTicker(h.streamHeartbeat)
	defer poll.Stop()
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil || http.NewResponseController(w).Flush() != nil {
				return
			}
		case <-poll.C:
			next, err := h.inboxSyncState(r, surface, principal)
			if err != nil {
				return
			}
			if next.Cursor != lastCursor {
				if !writeNotificationInboxSSE(w, "notification.sync", next.Cursor, next) {
					return
				}
				lastCursor = next.Cursor
			}
		}
	}
}

func (h *NotificationsHandler) inboxSyncState(r *http.Request, surface surfacemodel.ProductSurface, principal principalmodel.Principal) (notificationInboxSync, error) {
	query := notificationmodel.NotificationInboxQuery{Scope: notificationmodel.NotificationInboxScopeMine, Mailbox: notificationmodel.NotificationMailboxInbox, Limit: 1}
	page, err := h.inbox.ListInbox(r.Context(), query, "", surface, principal)
	if err != nil {
		return notificationInboxSync{}, err
	}
	facets, err := h.inbox.InboxFacets(r.Context(), query, surface, principal)
	if err != nil {
		return notificationInboxSync{}, err
	}
	state := notificationInboxSync{Unread: facets.Unread}
	identity := struct {
		UpdatedAt string `json:"updated_at"`
		ID        string `json:"id"`
		Unread    int    `json:"unread"`
	}{Unread: facets.Unread}
	if len(page.Items) > 0 {
		state.UpdatedAt = page.Items[0].UpdatedAt
		identity.UpdatedAt, identity.ID = page.Items[0].UpdatedAt, page.Items[0].ID
	}
	raw, _ := json.Marshal(identity)
	state.Cursor = base64.RawURLEncoding.EncodeToString(raw)
	return state, nil
}

func writeNotificationInboxSSE(w http.ResponseWriter, event, id string, value notificationInboxSync) bool {
	raw, _ := json.Marshal(value)
	if _, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", id, event, raw); err != nil {
		return false
	}
	return http.NewResponseController(w).Flush() == nil
}
