package notifications

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
)

type notificationSSEErrorWriter struct {
	header         http.Header
	writeErr       error
	flushErr       error
	writes         int
	flushes        int
	failWriteAfter int
	failFlushAfter int
}

func (w *notificationSSEErrorWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}
func (*notificationSSEErrorWriter) WriteHeader(int) {}
func (w *notificationSSEErrorWriter) Write(value []byte) (int, error) {
	if w.writeErr != nil && w.writes >= w.failWriteAfter {
		return 0, w.writeErr
	}
	w.writes++
	return len(value), nil
}
func (w *notificationSSEErrorWriter) FlushError() error {
	if w.flushErr != nil && w.flushes >= w.failFlushAfter {
		return w.flushErr
	}
	w.flushes++
	return nil
}

func inboxHandlerRequest(method, path string) *http.Request {
	request := httptest.NewRequest(method, path, nil)
	request.SetPathValue("notificationID", " item-1 ")
	request.SetPathValue("actionKey", " test.open ")
	request.SetPathValue("viewKey", " saved ")
	request.SetPathValue("delegationID", " delegation-1 ")
	return request
}

func TestNotificationInboxStreamEmitsSafeSyncCursorAndResumes(t *testing.T) {
	repo := &notificationHTTPRepository{inboxItem: notificationmodel.NotificationInboxItem{
		ID: "item-1", WorkspaceID: "workspace-1", RecipientUserID: "reviewer", Surface: "business_workspace", UpdatedAt: "2026-07-28T01:00:00Z",
	}}
	handler, _, _ := newNotificationHTTPHandler(repo)
	request := httptest.NewRequest(http.MethodGet, "/business/notifications/stream", nil)
	ctx, cancel := context.WithCancel(request.Context())
	cancel()
	request = request.WithContext(ctx)
	response := httptest.NewRecorder()
	handler.streamInbox(response, request)
	body := response.Body.String()
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/event-stream" || response.Header().Get("Cache-Control") != "no-store" || !strings.Contains(body, "event: notification.sync") || strings.Contains(body, "item-1") {
		t.Fatalf("status=%d headers=%v body=%q", response.Code, response.Header(), body)
	}
	idLine := ""
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "id: ") {
			idLine = strings.TrimPrefix(line, "id: ")
			break
		}
	}
	if idLine == "" {
		t.Fatal("stream cursor missing")
	}
	resume := httptest.NewRequest(http.MethodGet, "/business/notifications/stream", nil).WithContext(ctx)
	resume.Header.Set("Last-Event-ID", idLine)
	resumed := httptest.NewRecorder()
	handler.streamInbox(resumed, resume)
	if value := resumed.Body.String(); !strings.Contains(value, "event: notification.ready") || strings.Contains(value, "event: notification.sync") {
		t.Fatalf("resume body=%q", value)
	}
}

func TestNotificationInboxStreamCoversErrorsCursorOverridePollingAndHeartbeat(t *testing.T) {
	serviceErr := errors.New("inbox unavailable")
	for name, repo := range map[string]*notificationHTTPRepository{
		"list":   {inboxListErr: serviceErr},
		"facets": {inboxFacetsErr: serviceErr},
	} {
		t.Run(name, func(t *testing.T) {
			handler, captured, _ := newNotificationHTTPHandler(repo)
			response := httptest.NewRecorder()
			handler.streamInbox(response, httptest.NewRequest(http.MethodGet, "/business/notifications/stream", nil))
			if captured.err == nil {
				t.Fatalf("stream error was not forwarded: recorder=%q", response.Body.String())
			}
		})
	}

	repo := &notificationHTTPRepository{}
	handler, _, _ := newNotificationHTTPHandler(repo)
	state, err := handler.inboxSyncState(httptest.NewRequest(http.MethodGet, "/business/notifications/stream", nil), "business_workspace", handler.principal(httptest.NewRequest(http.MethodGet, "/", nil)))
	if err != nil || state.UpdatedAt != "" || state.Cursor == "" {
		t.Fatalf("empty state=%+v err=%v", state, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest(http.MethodGet, "/business/notifications/stream?cursor="+state.Cursor, nil).WithContext(ctx)
	request.Header.Set("Last-Event-ID", "ignored-by-query")
	handler.streamPollInterval, handler.streamHeartbeat = time.Millisecond, time.Millisecond
	response := httptest.NewRecorder()
	handler.streamInbox(response, request)
	if body := response.Body.String(); !strings.Contains(body, "event: notification.ready") || !strings.Contains(body, ": keepalive") {
		t.Fatalf("poll/heartbeat body=%q", body)
	}

	if writeNotificationInboxSSE(&notificationSSEErrorWriter{writeErr: serviceErr}, "notification.sync", "cursor", notificationInboxSync{}) {
		t.Fatal("write failure unexpectedly succeeded")
	}
	if writeNotificationInboxSSE(&notificationSSEErrorWriter{flushErr: serviceErr}, "notification.sync", "cursor", notificationInboxSync{}) {
		t.Fatal("flush failure unexpectedly succeeded")
	}
}

func TestNotificationInboxRemainingSuccessAndQueryEdges(t *testing.T) {
	if recipient := notificationInboxRecipientFilter(notificationmodel.NotificationInboxScopeDelegated, "team", " owner "); recipient != " owner " {
		t.Fatalf("delegated recipient=%q", recipient)
	}
	handler, _, _ := newNotificationHTTPHandler(&notificationHTTPRepository{emptyInbox: true})
	state, err := handler.inboxSyncState(httptest.NewRequest(http.MethodGet, "/business/notifications/stream", nil), "business_workspace", handler.principal(httptest.NewRequest(http.MethodGet, "/", nil)))
	if err != nil || state.UpdatedAt != "" || state.Cursor == "" {
		t.Fatalf("empty state=%+v err=%v", state, err)
	}
	base := notificationmodel.NotificationInboxItem{ID: "item-1", UpdatedAt: "2026-07-28T01:00:00Z"}
	changed := notificationmodel.NotificationInboxItem{ID: "item-2", UpdatedAt: "2026-07-28T01:01:00Z"}
	handler, _, _ = newNotificationHTTPHandler(&notificationHTTPRepository{inboxItems: []notificationmodel.NotificationInboxItem{base, changed}})
	handler.streamPollInterval, handler.streamHeartbeat = time.Millisecond, time.Hour
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Millisecond)
	defer cancel()
	handler.streamInbox(&notificationSSEErrorWriter{}, httptest.NewRequest(http.MethodGet, "/business/notifications/stream", nil).WithContext(ctx))
	custom := NewNotificationsHandler(NotificationsDependencies{StreamPollInterval: time.Second, StreamHeartbeat: 2 * time.Second})
	if custom.streamPollInterval != time.Second || custom.streamHeartbeat != 2*time.Second {
		t.Fatalf("stream intervals poll=%s heartbeat=%s", custom.streamPollInterval, custom.streamHeartbeat)
	}
}

func TestNotificationInboxStreamCoversUnavailableAndLiveLoopFailures(t *testing.T) {
	serviceErr := errors.New("stream failure")
	unavailable, captured, _ := newNotificationHTTPHandler(&notificationHTTPRepository{})
	unavailable.inbox = nil
	unavailable.streamInbox(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/business/notifications/stream", nil))
	if captured.code != "backend.notification.inbox_unavailable" {
		t.Fatalf("unavailable code=%q", captured.code)
	}

	for name, writer := range map[string]*notificationSSEErrorWriter{
		"initial sync write": {writeErr: serviceErr},
		"initial sync flush": {flushErr: serviceErr},
	} {
		t.Run(name, func(t *testing.T) {
			handler, _, _ := newNotificationHTTPHandler(&notificationHTTPRepository{})
			handler.streamInbox(writer, httptest.NewRequest(http.MethodGet, "/business/notifications/stream", nil))
		})
	}

	baseItem := notificationmodel.NotificationInboxItem{ID: "item-1", UpdatedAt: "2026-07-28T01:00:00Z"}
	changedItem := notificationmodel.NotificationInboxItem{ID: "item-2", UpdatedAt: "2026-07-28T01:01:00Z"}
	cases := []struct {
		name   string
		repo   *notificationHTTPRepository
		writer *notificationSSEErrorWriter
	}{
		{name: "poll state error", repo: &notificationHTTPRepository{inboxItem: baseItem, inboxListErrors: []error{nil, serviceErr}}, writer: &notificationSSEErrorWriter{}},
		{name: "poll changed write error", repo: &notificationHTTPRepository{inboxItems: []notificationmodel.NotificationInboxItem{baseItem, changedItem}}, writer: &notificationSSEErrorWriter{writeErr: serviceErr, failWriteAfter: 1}},
		{name: "heartbeat write error", repo: &notificationHTTPRepository{inboxItem: baseItem}, writer: &notificationSSEErrorWriter{writeErr: serviceErr, failWriteAfter: 1}},
		{name: "heartbeat flush error", repo: &notificationHTTPRepository{inboxItem: baseItem}, writer: &notificationSSEErrorWriter{flushErr: serviceErr, failFlushAfter: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler, _, _ := newNotificationHTTPHandler(tc.repo)
			if strings.HasPrefix(tc.name, "poll") {
				handler.streamPollInterval, handler.streamHeartbeat = time.Millisecond, time.Hour
			} else {
				handler.streamPollInterval, handler.streamHeartbeat = time.Hour, time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
			defer cancel()
			handler.streamInbox(tc.writer, httptest.NewRequest(http.MethodGet, "/business/notifications/stream", nil).WithContext(ctx))
		})
	}

	readyRepo := &notificationHTTPRepository{}
	readyHandler, _, _ := newNotificationHTTPHandler(readyRepo)
	state, err := readyHandler.inboxSyncState(httptest.NewRequest(http.MethodGet, "/business/notifications/stream", nil), "business_workspace", readyHandler.principal(httptest.NewRequest(http.MethodGet, "/", nil)))
	if err != nil {
		t.Fatal(err)
	}
	readyRequest := httptest.NewRequest(http.MethodGet, "/business/notifications/stream?cursor="+state.Cursor, nil)
	readyHandler.streamInbox(&notificationSSEErrorWriter{writeErr: serviceErr}, readyRequest)
}

func TestNotificationInboxHandlerHelpersAndUnavailable(t *testing.T) {
	if surface := notificationInboxSurface(httptest.NewRequest(http.MethodGet, "/portal/notifications", nil)); surface != "consumer_portal" {
		t.Fatalf("surface=%s", surface)
	}
	if surface := notificationInboxSurface(httptest.NewRequest(http.MethodGet, "/business/notifications", nil)); surface != "business_workspace" {
		t.Fatalf("surface=%s", surface)
	}
	query := notificationInboxQuery(httptest.NewRequest(http.MethodGet, "/business/notifications?limit=bad&mailbox=unread&query=x&category=a&source=b&severity=c&action_state=open&from=f&to=t&scope=team&team_member_id=u", nil))
	if query.Limit != 0 || query.Mailbox != "unread" || query.Scope != "team" || query.TeamMemberID != "u" || len(query.Categories) != 1 {
		t.Fatalf("query=%+v", query)
	}

	handler, response, _ := newNotificationHTTPHandler(&notificationHTTPRepository{})
	handler.inbox = nil
	calls := []func(){
		func() {
			handler.listInbox(httptest.NewRecorder(), inboxHandlerRequest(http.MethodGet, "/business/notifications"))
		},
		func() {
			handler.getInboxItem(httptest.NewRecorder(), inboxHandlerRequest(http.MethodGet, "/business/notifications/item-1"))
		},
		func() {
			handler.inboxFacets(httptest.NewRecorder(), inboxHandlerRequest(http.MethodGet, "/business/notifications/facets"))
		},
		func() {
			handler.resolveInboxAction(httptest.NewRecorder(), inboxHandlerRequest(http.MethodGet, "/business/notifications/item-1/actions/test.open/resolve"))
		},
		func() {
			handler.inboxUnreadCount(httptest.NewRecorder(), inboxHandlerRequest(http.MethodGet, "/business/notifications/unread-count"))
		},
		func() {
			handler.getMyNotificationPreference(httptest.NewRecorder(), inboxHandlerRequest(http.MethodGet, "/business/notification-preferences"))
		},
		func() {
			handler.saveMyNotificationPreference(httptest.NewRecorder(), inboxHandlerRequest(http.MethodPut, "/business/notification-preferences"))
		},
		func() {
			handler.readInboxItem(httptest.NewRecorder(), inboxHandlerRequest(http.MethodPost, "/business/notifications/item-1/read"))
		},
		func() {
			handler.unreadInboxItem(httptest.NewRecorder(), inboxHandlerRequest(http.MethodPost, "/business/notifications/item-1/unread"))
		},
		func() {
			handler.archiveInboxItem(httptest.NewRecorder(), inboxHandlerRequest(http.MethodPost, "/business/notifications/item-1/archive"))
		},
		func() {
			handler.restoreInboxItem(httptest.NewRecorder(), inboxHandlerRequest(http.MethodPost, "/business/notifications/item-1/restore"))
		},
		func() {
			handler.acknowledgeInboxAlert(httptest.NewRecorder(), inboxHandlerRequest(http.MethodPost, "/business/notifications/item-1/acknowledge"))
		},
		func() {
			handler.readAllInboxItems(httptest.NewRecorder(), inboxHandlerRequest(http.MethodPost, "/business/notifications/read-all"))
		},
		func() {
			handler.listInboxSavedViews(httptest.NewRecorder(), inboxHandlerRequest(http.MethodGet, "/business/notifications/saved-views"))
		},
		func() {
			handler.saveInboxSavedView(httptest.NewRecorder(), inboxHandlerRequest(http.MethodPut, "/business/notifications/saved-views/saved"))
		},
		func() {
			handler.deleteInboxSavedView(httptest.NewRecorder(), inboxHandlerRequest(http.MethodDelete, "/business/notifications/saved-views/saved"))
		},
		func() {
			handler.listInboxDelegations(httptest.NewRecorder(), inboxHandlerRequest(http.MethodGet, "/business/notifications/delegations"))
		},
		func() {
			handler.saveInboxDelegation(httptest.NewRecorder(), inboxHandlerRequest(http.MethodPut, "/business/notifications/delegations/delegation-1"))
		},
		func() {
			handler.deleteInboxDelegation(httptest.NewRecorder(), inboxHandlerRequest(http.MethodDelete, "/business/notifications/delegations/delegation-1"))
		},
		func() {
			handler.listDelegatedInboxOwners(httptest.NewRecorder(), inboxHandlerRequest(http.MethodGet, "/business/notifications/delegated-owners"))
		},
	}
	for _, call := range calls {
		response.status, response.code = 0, ""
		call()
		if response.status != http.StatusServiceUnavailable || response.code != "backend.notification.inbox_unavailable" {
			t.Fatalf("response=%+v", response)
		}
	}
}

func TestNotificationInboxHandlersSuccessDecodeAndErrors(t *testing.T) {
	action := notificationmodel.NotificationInboxActionRef{Key: "test.open", Kind: "route", Label: "Open", ResourceType: "test", ResourceID: "resource-1"}
	item := notificationmodel.NotificationInboxItem{ID: "item-1", ActionState: notificationmodel.NotificationActionOpen, AlertState: notificationmodel.NotificationAlertFiring, Actions: []notificationmodel.NotificationInboxActionRef{action}}
	repo := &notificationHTTPRepository{inboxItem: item}
	handler, response, _ := newNotificationHTTPHandler(repo)
	success := []struct {
		name string
		call func(*httptest.ResponseRecorder)
	}{
		{"list", func(w *httptest.ResponseRecorder) {
			handler.listInbox(w, inboxHandlerRequest(http.MethodGet, "/business/notifications"))
		}},
		{"get", func(w *httptest.ResponseRecorder) {
			handler.getInboxItem(w, inboxHandlerRequest(http.MethodGet, "/business/notifications/item-1"))
		}},
		{"facets", func(w *httptest.ResponseRecorder) {
			handler.inboxFacets(w, inboxHandlerRequest(http.MethodGet, "/business/notifications/facets"))
		}},
		{"resolve", func(w *httptest.ResponseRecorder) {
			handler.resolveInboxAction(w, inboxHandlerRequest(http.MethodGet, "/business/notifications/item-1/actions/test.open/resolve"))
		}},
		{"unread count", func(w *httptest.ResponseRecorder) {
			handler.inboxUnreadCount(w, inboxHandlerRequest(http.MethodGet, "/business/notifications/unread-count"))
		}},
		{"preference", func(w *httptest.ResponseRecorder) {
			handler.getMyNotificationPreference(w, inboxHandlerRequest(http.MethodGet, "/business/notification-preferences"))
		}},
		{"read", func(w *httptest.ResponseRecorder) {
			handler.readInboxItem(w, inboxHandlerRequest(http.MethodPost, "/business/notifications/item-1/read"))
		}},
		{"unread", func(w *httptest.ResponseRecorder) {
			handler.unreadInboxItem(w, inboxHandlerRequest(http.MethodPost, "/business/notifications/item-1/unread"))
		}},
		{"archive", func(w *httptest.ResponseRecorder) {
			handler.archiveInboxItem(w, inboxHandlerRequest(http.MethodPost, "/business/notifications/item-1/archive"))
		}},
		{"restore", func(w *httptest.ResponseRecorder) {
			handler.restoreInboxItem(w, inboxHandlerRequest(http.MethodPost, "/business/notifications/item-1/restore"))
		}},
		{"ack", func(w *httptest.ResponseRecorder) {
			handler.acknowledgeInboxAlert(w, inboxHandlerRequest(http.MethodPost, "/business/notifications/item-1/acknowledge"))
		}},
		{"read all", func(w *httptest.ResponseRecorder) {
			handler.readAllInboxItems(w, inboxHandlerRequest(http.MethodPost, "/business/notifications/read-all"))
		}},
		{"list views", func(w *httptest.ResponseRecorder) {
			handler.listInboxSavedViews(w, inboxHandlerRequest(http.MethodGet, "/business/notifications/saved-views"))
		}},
		{"delete view", func(w *httptest.ResponseRecorder) {
			handler.deleteInboxSavedView(w, inboxHandlerRequest(http.MethodDelete, "/business/notifications/saved-views/saved"))
		}},
	}
	for _, test := range success {
		t.Run(test.name, func(t *testing.T) {
			response.status, response.err = 0, nil
			writer := httptest.NewRecorder()
			test.call(writer)
			if test.name == "delete view" {
				if writer.Code != http.StatusNoContent {
					t.Fatalf("status=%d", writer.Code)
				}
				return
			}
			if response.status != http.StatusOK || response.err != nil {
				t.Fatalf("response=%+v", response)
			}
		})
	}

	request := httptest.NewRequest(http.MethodPut, "/business/notification-preferences", strings.NewReader(`{"enabled_channels":{"email":true}}`))
	handler.saveMyNotificationPreference(httptest.NewRecorder(), request)
	if response.status != http.StatusOK {
		t.Fatalf("preference response=%+v", response)
	}
	viewRequest := inboxHandlerRequest(http.MethodPut, "/business/notifications/saved-views/saved")
	viewRequest.Body = ioNopCloser(`{"name":"Saved","mailbox":"inbox","scope":"mine"}`)
	handler.saveInboxSavedView(httptest.NewRecorder(), viewRequest)
	if response.status != http.StatusOK {
		t.Fatalf("view response=%+v", response)
	}

	badPreference := httptest.NewRequest(http.MethodPut, "/business/notification-preferences", strings.NewReader(`{`))
	handler.saveMyNotificationPreference(httptest.NewRecorder(), badPreference)
	if response.status != http.StatusBadRequest {
		t.Fatalf("decode response=%+v", response)
	}
	badView := inboxHandlerRequest(http.MethodPut, "/business/notifications/saved-views/saved")
	badView.Body = ioNopCloser(`{`)
	handler.saveInboxSavedView(httptest.NewRecorder(), badView)
	if response.status != http.StatusBadRequest {
		t.Fatalf("view decode response=%+v", response)
	}

	failure := errors.New("inbox failed")
	repo.err = failure
	errorCalls := success[:len(success)-1]
	for _, test := range errorCalls {
		response.err = nil
		test.call(httptest.NewRecorder())
		if !errors.Is(response.err, failure) {
			t.Fatalf("%s error=%v", test.name, response.err)
		}
	}
	response.err = nil
	handler.deleteInboxSavedView(httptest.NewRecorder(), inboxHandlerRequest(http.MethodDelete, "/business/notifications/saved-views/saved"))
	if !errors.Is(response.err, failure) {
		t.Fatalf("delete error=%v", response.err)
	}
	response.err = nil
	preferenceError := httptest.NewRequest(http.MethodPut, "/business/notification-preferences", strings.NewReader(`{"enabled_channels":{"email":true}}`))
	handler.saveMyNotificationPreference(httptest.NewRecorder(), preferenceError)
	if !errors.Is(response.err, failure) {
		t.Fatalf("preference error=%v", response.err)
	}
	response.err = nil
	viewError := inboxHandlerRequest(http.MethodPut, "/business/notifications/saved-views/saved")
	viewError.Body = ioNopCloser(`{"name":"Saved","mailbox":"inbox","scope":"mine"}`)
	handler.saveInboxSavedView(httptest.NewRecorder(), viewError)
	if !errors.Is(response.err, failure) {
		t.Fatalf("view error=%v", response.err)
	}
}

func TestNotificationInboxDelegationHandlersSuccessDecodeAndErrors(t *testing.T) {
	repo := &notificationHTTPRepository{delegations: []notificationmodel.NotificationInboxDelegation{{ID: "delegation-1"}}, delegatedOwners: []string{"owner-1"}}
	handler, response, _ := newNotificationHTTPHandler(repo)
	list := inboxHandlerRequest(http.MethodGet, "/business/notifications/delegations")
	handler.listInboxDelegations(httptest.NewRecorder(), list)
	if response.status != http.StatusOK {
		t.Fatalf("list response=%+v", response)
	}
	save := inboxHandlerRequest(http.MethodPut, "/business/notifications/delegations/delegation-1")
	save.Body = ioNopCloser(`{"delegate_user_id":"delegate-1","enabled":true}`)
	handler.saveInboxDelegation(httptest.NewRecorder(), save)
	if response.status != http.StatusOK {
		t.Fatalf("save response=%+v", response)
	}
	handler.deleteInboxDelegation(httptest.NewRecorder(), inboxHandlerRequest(http.MethodDelete, "/business/notifications/delegations/delegation-1"))
	if response.status != http.StatusOK && response.status != 0 {
		t.Fatalf("delete response=%+v", response)
	}
	writer := httptest.NewRecorder()
	handler.deleteInboxDelegation(writer, inboxHandlerRequest(http.MethodDelete, "/business/notifications/delegations/delegation-1"))
	if writer.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d", writer.Code)
	}
	handler.listDelegatedInboxOwners(httptest.NewRecorder(), inboxHandlerRequest(http.MethodGet, "/business/notifications/delegated-owners"))
	if response.status != http.StatusOK {
		t.Fatalf("owners response=%+v", response)
	}
	bad := inboxHandlerRequest(http.MethodPut, "/business/notifications/delegations/delegation-1")
	bad.Body = ioNopCloser(`{`)
	handler.saveInboxDelegation(httptest.NewRecorder(), bad)
	if response.status != http.StatusBadRequest {
		t.Fatalf("decode response=%+v", response)
	}
	repo.err = errors.New("delegation failed")
	checks := []func(){
		func() { handler.listInboxDelegations(httptest.NewRecorder(), list) },
		func() {
			request := inboxHandlerRequest(http.MethodPut, "/business/notifications/delegations/delegation-1")
			request.Body = ioNopCloser(`{"delegate_user_id":"delegate-1","enabled":true}`)
			handler.saveInboxDelegation(httptest.NewRecorder(), request)
		},
		func() {
			handler.deleteInboxDelegation(httptest.NewRecorder(), inboxHandlerRequest(http.MethodDelete, "/business/notifications/delegations/delegation-1"))
		},
		func() {
			handler.listDelegatedInboxOwners(httptest.NewRecorder(), inboxHandlerRequest(http.MethodGet, "/business/notifications/delegated-owners"))
		},
	}
	for _, check := range checks {
		response.err = nil
		check()
		if !errors.Is(response.err, repo.err) {
			t.Fatalf("handler error=%v", response.err)
		}
	}
}

func ioNopCloser(value string) *readCloser { return &readCloser{Reader: strings.NewReader(value)} }

type readCloser struct{ *strings.Reader }

func (r *readCloser) Close() error { return nil }
