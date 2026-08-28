package notifications

import accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
)

type notificationHandlerCall struct {
	name   string
	method string
	target string
	body   string
	path   map[string]string
	call   func(http.ResponseWriter, *http.Request)
	want   int
}

func notificationRequest(call notificationHandlerCall) (*httptest.ResponseRecorder, *http.Request) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(call.method, call.target, strings.NewReader(call.body))
	for key, value := range call.path {
		request.SetPathValue(key, value)
	}
	return recorder, request
}

func notificationHandlerCalls(handler *NotificationsHandler) []notificationHandlerCall {
	return []notificationHandlerCall{
		{name: "list publications", method: http.MethodGet, target: "/notifications/publications?template_key=order-ready", call: handler.listPublications, want: http.StatusOK},
		{name: "request publication", method: http.MethodPost, target: "/notifications/templates/order-ready/publications", body: `{}`, path: map[string]string{"templateKey": "order-ready"}, call: handler.requestPublication, want: http.StatusCreated},
		{name: "approve publication", method: http.MethodPost, target: "/notifications/publications/approve-1/approve", path: map[string]string{"publicationID": "approve-1"}, call: handler.approvePublication, want: http.StatusOK},
		{name: "reject publication", method: http.MethodPost, target: "/notifications/publications/reject-1/reject", body: `{"reason":"incorrect content"}`, path: map[string]string{"publicationID": "reject-1"}, call: handler.rejectPublication, want: http.StatusOK},
		{name: "cancel publication", method: http.MethodPost, target: "/notifications/publications/cancel-1/cancel", path: map[string]string{"publicationID": "cancel-1"}, call: handler.cancelPublication, want: http.StatusOK},
		{name: "capabilities", method: http.MethodGet, target: "/notifications/capabilities", call: handler.capabilities, want: http.StatusOK},
		{name: "list", method: http.MethodGet, target: "/notifications/templates", call: handler.list, want: http.StatusOK},
		{name: "get", method: http.MethodGet, target: "/notifications/templates/order-ready", path: map[string]string{"templateKey": "order-ready"}, call: handler.get, want: http.StatusOK},
		{name: "list versions", method: http.MethodGet, target: "/notifications/templates/order-ready/versions", path: map[string]string{"templateKey": "order-ready"}, call: handler.listVersions, want: http.StatusOK},
		{name: "restore version", method: http.MethodPost, target: "/notifications/templates/order-ready/versions/1/restore", body: `{}`, path: map[string]string{"templateKey": "order-ready", "version": " 1 "}, call: handler.restoreVersionDraft, want: http.StatusOK},
		{name: "save draft", method: http.MethodPut, target: "/notifications/templates/order-ready", body: `{"template":{"name":"Order ready","channel":"email","version":1,"default_locale":"en-US","locales":{"en-US":{"subject":"Order ready","text":"Ready"}}}}`, path: map[string]string{"templateKey": "order-ready"}, call: handler.saveDraft, want: http.StatusOK},
		{name: "legacy publish", method: http.MethodPost, target: "/notifications/templates/order-ready/publish", path: map[string]string{"templateKey": "order-ready"}, call: handler.publish, want: http.StatusConflict},
		{name: "disable", method: http.MethodPost, target: "/notifications/templates/order-ready/disable", body: `{}`, path: map[string]string{"templateKey": "order-ready"}, call: handler.disable, want: http.StatusOK},
		{name: "preview template default recipient", method: http.MethodPost, target: "/notifications/templates/preview", body: `{"template":{"key":"preview","name":"Preview","channel":"email","version":1,"default_locale":"en-US","locales":{"en-US":{"subject":"Preview","text":"Ready"}}}}`, call: handler.previewTemplate, want: http.StatusOK},
		{name: "preview template recipients", method: http.MethodPost, target: "/notifications/templates/preview", body: `{"template":{"key":"preview","name":"Preview","channel":"email","version":1,"default_locale":"en-US","locales":{"en-US":{"subject":"Preview","text":"Ready"}}},"recipients":["person@example.com"]}`, call: handler.previewTemplate, want: http.StatusOK},
		{name: "preview default recipient", method: http.MethodPost, target: "/notifications/templates/order-ready/preview", body: `{}`, path: map[string]string{"templateKey": "order-ready"}, call: handler.preview, want: http.StatusOK},
		{name: "preview recipients", method: http.MethodPost, target: "/notifications/templates/order-ready/preview", body: `{"recipients":["person@example.com"]}`, path: map[string]string{"templateKey": "order-ready"}, call: handler.preview, want: http.StatusOK},
	}
}

func TestNotificationHandlersSuccessContracts(t *testing.T) {
	repo := &notificationHTTPRepository{publications: notificationHTTPPublications()}
	handler, response, _ := newNotificationHTTPHandler(repo)
	for _, call := range notificationHandlerCalls(handler) {
		t.Run(call.name, func(t *testing.T) {
			response.status, response.code, response.value, response.err = 0, "", nil, nil
			w, request := notificationRequest(call)
			call.call(w, request)
			if response.status != call.want || response.err != nil {
				t.Fatalf("response = status %d code %q value %#v err %v, want %d", response.status, response.code, response.value, response.err, call.want)
			}
			if call.want == http.StatusOK || call.want == http.StatusCreated {
				if response.value == nil {
					t.Fatal("successful response must expose a result")
				}
			}
		})
	}
	if repo.lastCreated.TemplateKey != "order-ready" || repo.lastCreated.RequestedBy != "reviewer" {
		t.Fatalf("publication request = %#v", repo.lastCreated)
	}
}

func TestNotificationHandlersPermissionDenials(t *testing.T) {
	repo := &notificationHTTPRepository{publications: notificationHTTPPublications()}
	handler, response, principal := newNotificationHTTPHandler(repo)
	accessfixture.Set(principal, accessfixture.Bundle{})
	for _, call := range notificationHandlerCalls(handler) {
		t.Run(call.name, func(t *testing.T) {
			response.status, response.code, response.value, response.err = 0, "", nil, nil
			w, request := notificationRequest(call)
			call.call(w, request)
			if response.status != http.StatusForbidden || response.code != "auth.permission_denied" {
				t.Fatalf("denial = status %d code %q", response.status, response.code)
			}
		})
	}
}

func TestNotificationHandlersWorkspaceServiceErrors(t *testing.T) {
	repo := &notificationHTTPRepository{publications: notificationHTTPPublications()}
	handler, response, principal := newNotificationHTTPHandler(repo)
	principal.WorkspaceID = ""
	for _, call := range notificationHandlerCalls(handler) {
		if call.name == "legacy publish" {
			continue
		}
		t.Run(call.name, func(t *testing.T) {
			response.status, response.code, response.value, response.err = 0, "", nil, nil
			w, request := notificationRequest(call)
			call.call(w, request)
			if response.status != http.StatusInternalServerError || response.err == nil {
				t.Fatalf("service failure = status %d err %v", response.status, response.err)
			}
		})
	}
}

func TestNotificationHandlersJSONAndVersionRejections(t *testing.T) {
	handler, response, _ := newNotificationHTTPHandler(&notificationHTTPRepository{})
	decodeCalls := []func(http.ResponseWriter, *http.Request){
		handler.requestPublication, handler.rejectPublication, handler.restoreVersionDraft,
		handler.saveDraft, handler.disable, handler.previewTemplate, handler.preview,
	}
	for _, call := range decodeCalls {
		response.status, response.err = 0, nil
		requestCall := notificationHandlerCall{method: http.MethodPost, target: "/notifications/test", body: `{`, path: map[string]string{"templateKey": "order-ready", "publicationID": "reject-1", "version": "1"}}
		w, request := notificationRequest(requestCall)
		call(w, request)
		if response.status != http.StatusBadRequest || response.err == nil {
			t.Fatalf("invalid JSON = status %d err %v", response.status, response.err)
		}
	}
	for _, version := range []string{"invalid", "0"} {
		response.status, response.code, response.err = 0, "", nil
		requestCall := notificationHandlerCall{method: http.MethodPost, target: "/notifications/test", body: `{}`, path: map[string]string{"templateKey": "order-ready", "version": version}}
		w, request := notificationRequest(requestCall)
		handler.restoreVersionDraft(w, request)
		if response.status != http.StatusBadRequest || response.code != "backend.notification.template_version_invalid" {
			t.Fatalf("version %q = status %d code %q", version, response.status, response.code)
		}
	}
}

func TestNotificationGetNotFound(t *testing.T) {
	template := notificationHTTPTemplate()
	handler, response, _ := newNotificationHTTPHandler(&notificationHTTPRepository{record: notificationmodel.NotificationTemplateRecord{Key: "other", Draft: &template}})
	call := notificationHandlerCall{method: http.MethodGet, target: "/notifications/templates/missing", path: map[string]string{"templateKey": "missing"}}
	w, request := notificationRequest(call)
	handler.get(w, request)
	if response.status != http.StatusNotFound || response.code != "backend.notification.template_not_found" {
		t.Fatalf("not found = status %d code %q err %v", response.status, response.code, response.err)
	}
}

func TestNotificationRequireAnyAdminAndUnknown(t *testing.T) {
	handler, response, principal := newNotificationHTTPHandler(&notificationHTTPRepository{})
	accessfixture.Set(principal, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})
	w, request := notificationRequest(notificationHandlerCall{method: http.MethodGet, target: "/notifications"})
	if !handler.requireAny(w, request, "missing") {
		t.Fatal("workspace admin should satisfy notification permission")
	}
	principal.Known = false
	if handler.requireAny(w, request, "missing") || response.status != http.StatusForbidden {
		t.Fatalf("unknown principal = allowed with status %d", response.status)
	}
}
