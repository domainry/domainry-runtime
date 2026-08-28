package records

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

// businessRecordSync is deliberately content-free. It only tells an
// authenticated Business Workspace client to refetch its already-authorized
// read models; record data and object identities never cross this stream.
type businessRecordSync struct {
	Cursor    string `json:"cursor"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

func (h *RecordsHandler) streamBusinessRecords(w http.ResponseWriter, r *http.Request) {
	state, err := h.businessRecordSyncState(r)
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
	event := "business.ready"
	if state.Cursor != lastCursor {
		event = "business.sync"
	}
	if !writeBusinessRecordSSE(w, event, state.Cursor, state) {
		return
	}
	lastCursor = state.Cursor
	pollInterval := h.streamPollInterval
	if pollInterval <= 0 {
		pollInterval = 500 * time.Millisecond
	}
	heartbeatInterval := h.streamHeartbeat
	if heartbeatInterval <= 0 {
		heartbeatInterval = 15 * time.Second
	}
	poll, heartbeat := time.NewTicker(pollInterval), time.NewTicker(heartbeatInterval)
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
			next, err := h.businessRecordSyncState(r)
			if err != nil {
				return
			}
			if next.Cursor != lastCursor {
				if !writeBusinessRecordSSE(w, "business.sync", next.Cursor, next) {
					return
				}
				lastCursor = next.Cursor
			}
		}
	}
}

func (h *RecordsHandler) businessRecordSyncState(r *http.Request) (businessRecordSync, error) {
	// The stream publishes only an opaque workspace cursor. Requiring the
	// identity.audit.view permission here would make ordinary business actors
	// unable to receive record invalidations even though they can read the
	// affected records through their own object permissions and data scope.
	queryScope, err := principalmodel.QueryScopeForPrincipal(h.principal(r))
	if err != nil || !queryScope.WorkspaceID().Valid() {
		return businessRecordSync{}, apperror.New(apperror.KindForbidden, "backend.workspace_scope_required", err, nil)
	}
	events, err := h.audit.ListAuditEvents(r.Context(), queryScope.WorkspaceID().String(), auditmodel.AuditEventQuery{Limit: 1})
	if err != nil {
		return businessRecordSync{}, err
	}
	identity := struct {
		ID        string `json:"id"`
		UpdatedAt string `json:"updated_at"`
	}{}
	if len(events) > 0 {
		identity.ID, identity.UpdatedAt = events[0].ID, events[0].CreatedAt
	}
	raw, _ := json.Marshal(identity)
	return businessRecordSync{
		Cursor:    base64.RawURLEncoding.EncodeToString(raw),
		UpdatedAt: identity.UpdatedAt,
	}, nil
}

func writeBusinessRecordSSE(w http.ResponseWriter, event, id string, value businessRecordSync) bool {
	raw, _ := json.Marshal(value)
	if _, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", id, event, raw); err != nil {
		return false
	}
	return http.NewResponseController(w).Flush() == nil
}
