package businessevents

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	apperror "github.com/domainry/domainry-foundation/apperror"
	businesseventapplication "github.com/domainry/domainry-runtime/runtime/application/businessevent"
	businesseventmodel "github.com/domainry/domainry-runtime/runtime/domain/businessevent/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

const Route = "/business-events/stream"

var filterValuePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

type BusinessEventsDependencies struct {
	Service                   *businesseventapplication.BusinessEventApplicationService
	Principal                 func(*http.Request) principalmodel.Principal
	WriteServiceError         func(http.ResponseWriter, *http.Request, error)
	SecurityAuditForPrincipal func(*http.Request, principalmodel.Principal, string, string, map[string]any)
	HeartbeatInterval         time.Duration
	RetryInterval             time.Duration
}

type BusinessEventsHandler struct {
	service           *businesseventapplication.BusinessEventApplicationService
	principal         func(*http.Request) principalmodel.Principal
	writeServiceError func(http.ResponseWriter, *http.Request, error)
	audit             func(*http.Request, principalmodel.Principal, string, string, map[string]any)
	heartbeat         time.Duration
	retry             time.Duration
}

func NewBusinessEventsHandler(deps BusinessEventsDependencies) *BusinessEventsHandler {
	heartbeat := deps.HeartbeatInterval
	if heartbeat <= 0 {
		heartbeat = 15 * time.Second
	}
	retry := deps.RetryInterval
	if retry <= 0 {
		retry = 3 * time.Second
	}
	return &BusinessEventsHandler{service: deps.Service, principal: deps.Principal, writeServiceError: deps.WriteServiceError, audit: deps.SecurityAuditForPrincipal, heartbeat: heartbeat, retry: retry}
}

func (h *BusinessEventsHandler) stream(w http.ResponseWriter, r *http.Request) {
	principal := h.principal(r)
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Header.Get("Authorization"))), "bearer ") {
		h.auditEvent(r, principal, "business_event_stream_rejected", "Business event stream rejected", map[string]any{"reason": "session_required"})
		h.writeServiceError(w, r, apperror.New(apperror.KindForbidden, "backend.event_stream.identity_required", nil, nil))
		return
	}
	filter, err := parseFilter(r)
	if err != nil {
		h.auditEvent(r, principal, "business_event_stream_rejected", "Business event stream rejected", map[string]any{"reason": "filter_invalid"})
		h.writeServiceError(w, r, err)
		return
	}
	lastEventID := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if len(lastEventID) > 256 || strings.ContainsAny(lastEventID, "\r\n") {
		h.auditEvent(r, principal, "business_event_stream_rejected", "Business event stream rejected", map[string]any{"reason": "last_event_id_invalid"})
		h.writeServiceError(w, r, apperror.New(apperror.KindBadRequest, "backend.event_stream.filter_invalid", nil, nil))
		return
	}
	subscription, err := h.service.Open(r.Context(), principal, lastEventID)
	if err != nil {
		h.auditEvent(r, principal, "business_event_stream_rejected", "Business event stream rejected", map[string]any{"reason": apperror.CodeOf(err)})
		h.writeServiceError(w, r, err)
		return
	}
	defer subscription.Close()

	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprintf(w, "retry: %d\n\n", h.retry.Milliseconds()); err != nil {
		return
	}
	if subscription.NeedsResync {
		resync := businesseventmodel.BusinessEvent{ID: subscription.CurrentEvent, Type: businesseventmodel.EventTypeResync, Reason: "replay_unavailable", OccurredAt: time.Now().UTC()}
		if err := writeEvent(w, resync); err != nil {
			return
		}
	}
	for _, event := range subscription.Replay {
		if filter.Matches(event) {
			if err := writeEvent(w, event); err != nil {
				return
			}
		}
	}
	if err := controller.Flush(); err != nil {
		return
	}

	h.auditEvent(r, principal, "business_event_stream_connected", "Business event stream connected", map[string]any{
		"object_filter_count": len(filter.ObjectKeys), "event_type_filter_count": len(filter.EventTypes),
		"last_event_id_present": lastEventID != "", "backplane_mode": h.service.BackplaneMode(r.Context()),
	})
	disconnectReason := "client_closed"
	defer func() {
		auditRequest := r.WithContext(context.WithoutCancel(r.Context()))
		h.auditEvent(auditRequest, principal, "business_event_stream_disconnected", "Business event stream disconnected", map[string]any{"reason": disconnectReason})
	}()

	heartbeat := time.NewTimer(h.heartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprintf(w, ": heartbeat %d\n\n", time.Now().UTC().Unix()); err != nil {
				disconnectReason = "write_failed"
				return
			}
			if err := controller.Flush(); err != nil {
				disconnectReason = "flush_failed"
				return
			}
			heartbeat.Reset(h.heartbeat)
		case event, ok := <-subscription.Events:
			if !ok {
				disconnectReason = "slow_consumer_or_backplane_closed"
				return
			}
			if !filter.Matches(event) {
				continue
			}
			if err := writeEvent(w, event); err != nil {
				disconnectReason = "write_failed"
				return
			}
			if err := controller.Flush(); err != nil {
				disconnectReason = "flush_failed"
				return
			}
		}
	}
}

func writeEvent(w http.ResponseWriter, event businesseventmodel.BusinessEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if event.ID != "" {
		if _, err := fmt.Fprintf(w, "id: %s\n", event.ID); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(w, "event: business.%s\ndata: %s\n\n", event.Type, payload)
	return err
}

func parseFilter(r *http.Request) (businesseventmodel.Filter, error) {
	objects, err := parseValues(r.URL.Query().Get("objects"), nil)
	if err != nil {
		return businesseventmodel.Filter{}, err
	}
	types, err := parseValues(r.URL.Query().Get("types"), map[string]struct{}{businesseventmodel.EventTypeRefresh: {}})
	if err != nil {
		return businesseventmodel.Filter{}, err
	}
	return businesseventmodel.Filter{ObjectKeys: objects, EventTypes: types}, nil
}

func parseValues(raw string, allowed map[string]struct{}) (map[string]struct{}, error) {
	values := map[string]struct{}{}
	if strings.TrimSpace(raw) == "" {
		return values, nil
	}
	for _, value := range strings.Split(raw, ",") {
		value = strings.TrimSpace(value)
		if len(values) >= 32 || !filterValuePattern.MatchString(value) {
			return nil, apperror.New(apperror.KindBadRequest, "backend.event_stream.filter_invalid", nil, nil)
		}
		if allowed != nil {
			if _, ok := allowed[value]; !ok {
				return nil, apperror.New(apperror.KindBadRequest, "backend.event_stream.filter_invalid", nil, nil)
			}
		}
		values[value] = struct{}{}
	}
	return values, nil
}

func (h *BusinessEventsHandler) auditEvent(r *http.Request, principal principalmodel.Principal, event, summary string, metadata map[string]any) {
	if h.audit != nil {
		h.audit(r, principal, event, summary, metadata)
	}
}
