package agentdialog

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type agentDialogRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn agentDialogRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

type agentDialogHTTPResult struct {
	status int
	code   string
	audits int
}

func newAgentDialogProxyHandler(config Config) (*AgentDialogHandler, *agentDialogHTTPResult) {
	result := &agentDialogHTTPResult{}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user-1", WorkspaceID: "workspace-1"}, RequestID: "request-1"}, accessfixture.Bundle{Key: "operator"})
	return NewAgentDialogHandler(AgentDialogDependencies{
		Config:    config,
		Principal: func(*http.Request) principalmodel.Principal { return principal },
		WriteError: func(_ http.ResponseWriter, _ *http.Request, status int, code string, _ ...string) {
			result.status, result.code = status, code
		},
		DecodeJSON: func(_ http.ResponseWriter, request *http.Request, value any) bool {
			return json.NewDecoder(request.Body).Decode(value) == nil
		},
		SecurityAudit: func(*http.Request, string, string, map[string]any) { result.audits++ },
	}), result
}

func withAgentDialogClient(t *testing.T, fn agentDialogRoundTripFunc) {
	t.Helper()
	previous := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: fn}
	t.Cleanup(func() { http.DefaultClient = previous })
}

func TestAgentDialogRunAndStatusProxy(t *testing.T) {
	var requests []*http.Request
	withAgentDialogClient(t, func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request)
		return &http.Response{StatusCode: http.StatusAccepted, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil
	})
	handler, result := newAgentDialogProxyHandler(Config{BaseURL: "https://agent.example", APIKey: "secret", AgentID: 42})

	w := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/agent-dialog/run", strings.NewReader(`{"message":"hello"}`))
	request.Header.Set("X-Workspace-ID", "workspace-header")
	request.Header.Set("X-Surface-Key", "customers")
	handler.agentDialogRun(w, request)
	if w.Code != http.StatusAccepted || w.Body.String() != `{"ok":true}` || len(requests) != 1 {
		t.Fatalf("run response = %d %q requests %d error %#v", w.Code, w.Body.String(), len(requests), result)
	}
	if requests[0].Method != http.MethodPost || requests[0].Header.Get("Authorization") != "Bearer secret" || requests[0].Header.Get("Content-Type") != "application/json" {
		t.Fatalf("upstream run request = %#v", requests[0])
	}
	body, _ := io.ReadAll(requests[0].Body)
	for _, text := range []string{`"response_mode":"blocking"`, `"agent_id":42`, `workspace:workspace-header:user:user-1:dialog:customers`, `"workspace_id":"workspace-1"`} {
		if !strings.Contains(string(body), text) {
			t.Fatalf("upstream payload missing %q: %s", text, body)
		}
	}

	w = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/agent-dialog/runs/run%2F1", nil)
	request.SetPathValue("runID", " run/1 ")
	handler.agentDialogRunStatus(w, request)
	if w.Code != http.StatusAccepted || len(requests) != 2 || requests[1].Method != http.MethodGet || !strings.HasSuffix(requests[1].URL.EscapedPath(), "/run%2F1") {
		t.Fatalf("status response = %d request %#v", w.Code, requests[1])
	}
	if requests[1].Header.Get("Content-Type") != "" {
		t.Fatalf("GET content type = %q", requests[1].Header.Get("Content-Type"))
	}
}

func TestAgentDialogRunPreservesExplicitResponseMode(t *testing.T) {
	withAgentDialogClient(t, func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusAccepted, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})
	handler, _ := newAgentDialogProxyHandler(Config{BaseURL: "https://agent.example", APIKey: "secret", AgentID: 42})
	response := httptest.NewRecorder()
	handler.agentDialogRun(response, httptest.NewRequest(http.MethodPost, "/agent-dialog/run", strings.NewReader(`{"message":"hello","response_mode":"async"}`)))
	if response.Code != http.StatusAccepted {
		t.Fatalf("explicit response mode status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestAgentDialogRunStreamSuccess(t *testing.T) {
	withAgentDialogClient(t, func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "Cache-Control": []string{"no-cache"}, "X-Secret": []string{"hidden"}},
			Body:       io.NopCloser(strings.NewReader("data: one\n\ndata: two\n\n")),
		}, nil
	})
	handler, _ := newAgentDialogProxyHandler(Config{BaseURL: "https://agent.example", APIKey: "secret", AgentID: 42})
	w := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/agent-dialog/run/stream", strings.NewReader(`{"message":"hello","response_mode":"streaming"}`))
	handler.agentDialogRunStream(w, request)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "data: two") || w.Header().Get("Content-Type") != "text/event-stream" || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Secret") != "" || w.Header().Get("X-Accel-Buffering") != "no" {
		t.Fatalf("stream response = %d headers %#v body %q", w.Code, w.Header(), w.Body.String())
	}
}

func TestAgentDialogRunStreamMarshalAndUpstreamErrors(t *testing.T) {
	t.Run("marshal", func(t *testing.T) {
		handler, result := newAgentDialogProxyHandler(Config{BaseURL: "https://agent.example", APIKey: "secret", AgentID: 42})
		handler.decodeJSON = func(_ http.ResponseWriter, _ *http.Request, value any) bool {
			payload := value.(*agentDialogRunRequest)
			payload.Metadata = map[string]any{"invalid": make(chan int)}
			return true
		}
		w := httptest.NewRecorder()
		handler.agentDialogRunStream(w, httptest.NewRequest(http.MethodPost, "/agent-dialog/run/stream", nil))
		if result.status != http.StatusBadRequest || result.code != "backend.invalid_json" {
			t.Fatalf("stream marshal failure = %#v", result)
		}
	})

	t.Run("upstream status", func(t *testing.T) {
		withAgentDialogClient(t, func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":"rate_limited"}`))}, nil
		})
		handler, _ := newAgentDialogProxyHandler(Config{BaseURL: "https://agent.example", APIKey: "secret", AgentID: 42})
		w := httptest.NewRecorder()
		handler.agentDialogRunStream(w, httptest.NewRequest(http.MethodPost, "/agent-dialog/run/stream", strings.NewReader(`{"message":"hello"}`)))
		if w.Code != http.StatusTooManyRequests || w.Body.String() != `{"error":"rate_limited"}` {
			t.Fatalf("stream upstream error = %d %q", w.Code, w.Body.String())
		}
	})
}

func TestAgentDialogProxyFailures(t *testing.T) {
	t.Run("client failure", func(t *testing.T) {
		withAgentDialogClient(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })
		handler, result := newAgentDialogProxyHandler(Config{BaseURL: "https://agent.example", APIKey: "secret", AgentID: 42})
		for _, call := range []func(http.ResponseWriter, *http.Request){handler.agentDialogRun, handler.agentDialogRunStream, handler.agentDialogRunStatus} {
			result.status, result.code = 0, ""
			w := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/agent-dialog", strings.NewReader(`{"message":"hello"}`))
			request.SetPathValue("runID", "run-1")
			call(w, request)
			if result.status != http.StatusBadGateway || result.code != "agent_dialog.upstream_failed" {
				t.Fatalf("client failure = %#v", result)
			}
		}
		if result.audits != 3 {
			t.Fatalf("security audits = %d", result.audits)
		}
	})

	t.Run("not configured", func(t *testing.T) {
		handler, result := newAgentDialogProxyHandler(Config{})
		for _, call := range []func(http.ResponseWriter, *http.Request){handler.agentDialogRun, handler.agentDialogRunStream, handler.agentDialogRunStatus} {
			result.status, result.code = 0, ""
			w := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/agent-dialog", strings.NewReader(`{"message":"hello"}`))
			request.SetPathValue("runID", "run-1")
			call(w, request)
			if result.status != http.StatusServiceUnavailable || result.code != "agent_dialog.not_configured" {
				t.Fatalf("not configured = %#v", result)
			}
		}
		agentIDMissing, agentIDResult := newAgentDialogProxyHandler(Config{APIKey: "secret"})
		if upstream, ok := agentIDMissing.newAgentDialogUpstreamRequest(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/agent-dialog", nil), "/run", nil); ok || upstream != nil || agentIDResult.code != "agent_dialog.not_configured" {
			t.Fatalf("missing agent ID = (%#v, %v) result %#v", upstream, ok, agentIDResult)
		}
	})

	handler, result := newAgentDialogProxyHandler(Config{BaseURL: "://invalid", APIKey: "secret", AgentID: 42})
	w := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/agent-dialog", nil)
	if upstream, ok := handler.newAgentDialogUpstreamRequest(w, request, "/run", nil); ok || upstream != nil || result.code != "agent_dialog.invalid_upstream" {
		t.Fatalf("invalid upstream = (%#v, %v) result %#v", upstream, ok, result)
	}
}

func TestAgentDialogUpstreamErrorRelay(t *testing.T) {
	for _, test := range []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
		wantBody    string
		wantCode    string
	}{
		{name: "json", contentType: "application/json; charset=utf-8", body: `{"error":"denied"}`, wantStatus: http.StatusUnprocessableEntity, wantBody: `{"error":"denied"}`},
		{name: "empty json", contentType: "application/json", wantStatus: http.StatusBadGateway, wantCode: "agent_dialog.upstream_failed"},
		{name: "plain text", contentType: "text/plain", body: "failed", wantStatus: http.StatusBadGateway, wantCode: "agent_dialog.upstream_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, result := newAgentDialogProxyHandler(Config{})
			w := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/agent-dialog", nil)
			response := &http.Response{StatusCode: http.StatusUnprocessableEntity, Header: http.Header{"Content-Type": []string{test.contentType}}, Body: io.NopCloser(strings.NewReader(test.body))}
			handler.writeAgentDialogUpstreamError(w, request, response)
			if test.wantBody != "" {
				if w.Code != test.wantStatus || w.Body.String() != test.wantBody {
					t.Fatalf("relay = %d %q", w.Code, w.Body.String())
				}
			} else if result.status != test.wantStatus || result.code != test.wantCode {
				t.Fatalf("mapped error = %#v", result)
			}
		})
	}
}

func TestAgentDialogProxyHelpers(t *testing.T) {
	handler, _ := newAgentDialogProxyHandler(Config{BaseURL: "https://agent.example", APIKey: "secret", AgentID: 7, Timeout: time.Second})
	request := httptest.NewRequest(http.MethodPost, "/agent-dialog", nil)
	request.Header.Set("X-Workspace-ID", " workspace/1 ")
	request.Header.Set("X-Surface-Key", " ")
	payload := agentDialogRunRequest{Message: " hello ", ResponseMode: " async ", TimeoutSeconds: 5, NewSession: true, Metadata: map[string]any{"caller": "ui"}, Context: map[string]any{"object_key": "customer"}}
	upstream := handler.agentDialogUpstreamPayload(request, payload)
	if upstream["response_mode"] != "async" || upstream["timeout_seconds"] != 5 || upstream["new_session"] != true || !strings.Contains(upstream["message"].(string), "Server-scoped runtime context") {
		t.Fatalf("upstream payload = %#v", upstream)
	}
	if got := handler.agentDialogExternalSessionID(request, " requested ", "user"); got != "requested" {
		t.Fatalf("requested session = %q", got)
	}
	if got := handler.agentDialogExternalSessionID(request, "", ""); got != "workspace:workspace1:user:anonymous:dialog:workspace" {
		t.Fatalf("derived session = %q", got)
	}

	w := httptest.NewRecorder()
	upstreamRequest, ok := handler.newAgentDialogUpstreamRequest(w, request, "/run", []byte(`{}`))
	if !ok || upstreamRequest.Method != http.MethodPost || upstreamRequest.Header.Get("Accept") == "" {
		t.Fatalf("timed upstream request = %#v ok %v", upstreamRequest, ok)
	}
	if _, deadline := upstreamRequest.Context().Deadline(); !deadline {
		t.Fatal("configured timeout did not set a deadline")
	}

	for key, want := range map[string]bool{"Content-Type": true, " cache-control ": true, "Authorization": false} {
		if got := agentDialogRelayHeaderAllowed(key); got != want {
			t.Errorf("header %q allowed = %v, want %v", key, got, want)
		}
	}
	if agentDialogPathEscape(" a/b ") != "a%2Fb" {
		t.Fatalf("escaped path = %q", agentDialogPathEscape(" a/b "))
	}
	for input, want := range map[string]string{"": "default", " abc-_. ": "abc-_.", "///": "3", "a b/c": "abc", "A{": "A"} {
		if got := agentDialogSessionPart(input); got != want {
			t.Errorf("session part %q = %q, want %q", input, got, want)
		}
	}
	if timeNowUnixNano() <= 0 {
		t.Fatal("timeNowUnixNano must return current time")
	}

	response := &http.Response{StatusCode: http.StatusCreated, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}
	relay := httptest.NewRecorder()
	relayAgentDialogJSONResponse(relay, request, response)
	if relay.Code != http.StatusCreated || relay.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("default relay = %d %#v", relay.Code, relay.Header())
	}
	response = &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/problem+json"}}, Body: io.NopCloser(strings.NewReader(`{}`))}
	relay = httptest.NewRecorder()
	relayAgentDialogJSONResponse(relay, request, response)
	if relay.Header().Get("Content-Type") != "application/problem+json" {
		t.Fatalf("custom relay content type = %q", relay.Header().Get("Content-Type"))
	}
	flush := httptest.NewRecorder()
	if n, err := (agentDialogFlushWriter{w: flush}).Write([]byte("event")); err != nil || n != 5 || flush.Body.String() != "event" {
		t.Fatalf("flush writer = n %d err %v body %q", n, err, flush.Body.String())
	}
}

func TestAgentDialogProxyRejectsInvalidInputAndMarshal(t *testing.T) {
	handler, result := newAgentDialogProxyHandler(Config{BaseURL: "https://agent.example", APIKey: "secret", AgentID: 42})
	for _, call := range []func(http.ResponseWriter, *http.Request){handler.agentDialogRun, handler.agentDialogRunStream} {
		w := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/agent-dialog", strings.NewReader(`{`))
		call(w, request)
	}
	w := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/agent-dialog", nil)
	handler.agentDialogRunStatus(w, request)
	if result.status != http.StatusBadRequest || result.code != "agent_dialog.run_id_required" {
		t.Fatalf("missing run id = %#v", result)
	}
	result.status, result.code = 0, ""
	handler.proxyAgentDialogJSON(w, request, "/run", agentDialogRunRequest{Metadata: map[string]any{"invalid": make(chan int)}})
	if result.status != http.StatusBadRequest || result.code != "backend.invalid_json" {
		t.Fatalf("marshal failure = %#v", result)
	}
}
