package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCapturedAndCopiedHTTPResponseCoversStatusAndHeaderEdges(t *testing.T) {
	capture := &capturedHTTPResponse{header: make(http.Header)}
	capture.Header().Add("X-Test", "one")
	capture.Header().Add("X-Test", "two")
	capture.WriteHeader(http.StatusCreated)
	capture.WriteHeader(http.StatusTeapot)
	if _, err := capture.Write([]byte("created")); err != nil {
		t.Fatal(err)
	}
	if capture.status != http.StatusCreated {
		t.Fatalf("status=%d", capture.status)
	}
	response := httptest.NewRecorder()
	copyHTTPResponse(response, capture)
	if response.Code != http.StatusCreated || response.Body.String() != "created" || len(response.Header().Values("X-Test")) != 2 {
		t.Fatalf("response=%d headers=%v body=%q", response.Code, response.Header(), response.Body.String())
	}

	implicit := &capturedHTTPResponse{header: make(http.Header)}
	if _, err := implicit.Write([]byte("ok")); err != nil || implicit.status != http.StatusOK {
		t.Fatalf("implicit=%#v err=%v", implicit, err)
	}
	empty := httptest.NewRecorder()
	copyHTTPResponse(empty, &capturedHTTPResponse{header: make(http.Header)})
	if empty.Code != http.StatusOK {
		t.Fatalf("empty status=%d", empty.Code)
	}
}

func TestListenerOpenAPIHandlerCoversPassThroughProjectionAndFailureEdges(t *testing.T) {
	router := &HTTPRouter{}
	request := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	tests := []struct {
		name       string
		status     int
		body       string
		wantStatus int
		contains   string
	}{
		{name: "upstream status", status: http.StatusTeapot, body: "upstream", wantStatus: http.StatusTeapot, contains: "upstream"},
		{name: "invalid json", status: http.StatusOK, body: "{", wantStatus: http.StatusServiceUnavailable, contains: "openapi.listener_projection_failed"},
		{name: "missing paths", status: http.StatusOK, body: `{"openapi":"3.1.0"}`, wantStatus: http.StatusServiceUnavailable, contains: "openapi.listener_projection_failed"},
		{name: "unknown group", status: http.StatusOK, body: `{"paths":{}}`, wantStatus: http.StatusServiceUnavailable, contains: "openapi.listener_projection_failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			full := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-Upstream", "present")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			group := ListenerRouteGroupTenantAdmin
			if tc.name == "unknown group" {
				group = ListenerRouteGroup("unknown")
			}
			response := httptest.NewRecorder()
			router.listenerOpenAPIHandler(group, full).ServeHTTP(response, request)
			if response.Code != tc.wantStatus || !strings.Contains(response.Body.String(), tc.contains) {
				t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
			}
			if tc.name == "upstream status" && response.Header().Get("X-Upstream") != "present" {
				t.Fatalf("headers=%v", response.Header())
			}
		})
	}

	document := `{"openapi":"3.1.0","paths":{"/unknown":{"get":{},"parameters":[]},"invalid":"value"}}`
	full := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(document))
	})
	response := httptest.NewRecorder()
	router.listenerOpenAPIHandler(ListenerRouteGroupTenantAdmin, full).ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"paths":{}`) {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}

	calls := 0
	publicRouter := &HTTPRouter{openAPIHTTP: routerCountingRegistrar{calls: &calls}}
	response = httptest.NewRecorder()
	publicRouter.listenerOpenAPIHandler(ListenerRouteGroupPublic, http.NotFoundHandler()).ServeHTTP(response, request)
	if calls != 1 || response.Code != http.StatusNotFound {
		t.Fatalf("public registration calls=%d status=%d", calls, response.Code)
	}
}

func TestOpenAPIProjectionAndAuthoringSemanticsRemainingEdges(t *testing.T) {
	if err := projectOpenAPIForListenerGroup(map[string]any{}, ListenerRouteGroupTenantAdmin); err == nil {
		t.Fatal("missing paths accepted")
	}
	if err := projectOpenAPIForListenerGroup(map[string]any{"paths": map[string]any{}}, ListenerRouteGroup("unknown")); err == nil {
		t.Fatal("unknown group accepted")
	}
	document := map[string]any{"paths": map[string]any{
		"/invalid": "not-a-path-item",
		"/unknown": map[string]any{"parameters": []any{}, "get": map[string]any{}},
	}}
	if err := projectOpenAPIForListenerGroup(document, ListenerRouteGroupTenantAdmin); err != nil {
		t.Fatal(err)
	}
	if len(document["paths"].(map[string]any)) != 0 {
		t.Fatalf("projected paths=%#v", document["paths"])
	}
	if got := runtimeAuthoringSemantics(http.StatusNotFound, "backend.not_found"); got.Class != "request" || got.Retryable {
		t.Fatalf("default semantics=%#v", got)
	}
}
