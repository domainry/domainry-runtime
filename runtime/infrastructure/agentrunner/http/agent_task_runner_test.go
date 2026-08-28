package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentruntime "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
)

func TestAgentTaskRunnerStartPollCancelContract(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("Idempotency-Key") != "idem-1" {
			t.Fatalf("headers=%v", r.Header)
		}
		switch requests {
		case 1:
			body, _ := io.ReadAll(r.Body)
			if r.Method != http.MethodPost || !strings.Contains(string(body), `"task_run_id":"run-1"`) || !strings.Contains(string(body), "instruction") {
				t.Fatalf("start request=%s %s", r.Method, body)
			}
			_, _ = w.Write([]byte(`{"data":{"id":"remote-1","status":"queued"}}`))
		case 2:
			if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/remote-1") {
				t.Fatalf("poll request=%s %s", r.Method, r.URL.Path)
			}
			_, _ = w.Write([]byte(`{"run_id":"remote-1","status":"completed","output":{"score":9},"usage":{"tokens":12}}`))
		case 3:
			if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/remote-1/cancel") {
				t.Fatalf("cancel request=%s %s", r.Method, r.URL.Path)
			}
			_, _ = w.Write([]byte(`{"run_id":"remote-1","status":"cancelled"}`))
		}
	}))
	defer server.Close()
	runner := NewAgentTaskRunner(Config{BaseURL: server.URL + "/", APIKey: "secret", AgentID: 7, Timeout: time.Second})
	started, err := runner.Start(t.Context(), agentruntime.AgentTaskRunnerRequest{TaskRunID: "run-1", WorkspaceID: "workspace", Task: agentmodel.AgentTaskDefinition{Key: "task", Version: "1", Instruction: "instruction"}, Input: map[string]any{"id": "one"}, IdempotencyKey: "idem-1"})
	if err != nil || started.ExternalRunID != "remote-1" || started.Status != agentruntime.AgentProviderRunAccepted {
		t.Fatalf("started=%#v err=%v", started, err)
	}
	polled, err := runner.Poll(t.Context(), "remote-1", "idem-1")
	if err != nil || polled.Status != agentruntime.AgentProviderRunCompleted || polled.Outcome != "success" || polled.Output["score"] != float64(9) {
		t.Fatalf("polled=%#v err=%v", polled, err)
	}
	cancelled, err := runner.Cancel(t.Context(), "remote-1", "idem-1")
	if err != nil || cancelled.Status != agentruntime.AgentProviderRunCancelled {
		t.Fatalf("cancelled=%#v err=%v", cancelled, err)
	}
}

func TestAgentTaskRunnerClassifiesConfigurationHTTPTransportAndContractFailures(t *testing.T) {
	if result, err := NewAgentTaskRunner(Config{}).Poll(t.Context(), "run", ""); err == nil || result.ErrorCode != "agent.runner.not_configured" {
		t.Fatalf("configuration result=%#v err=%v", result, err)
	}
	for name, test := range map[string]struct {
		handler   http.HandlerFunc
		code      string
		retryable bool
	}{
		"five hundred": {func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }, "agent.runner.provider_http_503", true},
		"rate limited": {func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(429) }, "agent.runner.provider_http_429", true},
		"bad request":  {func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(400) }, "agent.runner.provider_http_400", false},
		"invalid json": {func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("bad")) }, "agent.runner.response_invalid", false},
		"no status":    {func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"id":"run"}`)) }, "agent.runner.response_invalid", false},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(test.handler)
			defer server.Close()
			result, err := NewAgentTaskRunner(Config{BaseURL: server.URL, APIKey: "key", AgentID: 1}).Poll(t.Context(), "run", "")
			if err == nil || result.ErrorCode != test.code || result.Retryable != test.retryable {
				t.Fatalf("result=%#v err=%v", result, err)
			}
		})
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := NewAgentTaskRunner(Config{BaseURL: "https://agent.invalid", APIKey: "key", AgentID: 1}).Poll(cancelled, "run", "")
	if err == nil || result.ErrorCode != "agent.runner.transport_failed" || !result.Retryable {
		t.Fatalf("transport result=%#v err=%v", result, err)
	}
}

func TestDecodeAgentResultStatusAliasesAndInvalidJSON(t *testing.T) {
	for input, status := range map[string]agentruntime.AgentProviderRunStatus{"pending": agentruntime.AgentProviderRunAccepted, "processing": agentruntime.AgentProviderRunRunning, "succeeded": agentruntime.AgentProviderRunCompleted, "error": agentruntime.AgentProviderRunFailed, "canceled": agentruntime.AgentProviderRunCancelled, "unknown": agentruntime.AgentProviderRunUnknown} {
		result, err := decodeAgentResult([]byte(`{"state":"` + input + `"}`))
		if err != nil || result.Status != status {
			t.Fatalf("input=%s result=%#v err=%v", input, result, err)
		}
	}
	if _, err := decodeAgentResult(nil); err == nil {
		t.Fatal("empty response accepted")
	}
	if result, err := decodeAgentResult([]byte(`{"status":"failed","retryable":true}`)); err != nil || !result.Retryable {
		t.Fatalf("retryable result=%#v err=%v", result, err)
	}
	if result, err := decodeAgentResult([]byte(`{"status":"completed","outcome":"review"}`)); err != nil || result.Outcome != "review" {
		t.Fatalf("outcome result=%#v err=%v", result, err)
	}
	if value := firstAgentString(map[string]any{"first": " ", "second": "value"}, "first", "second"); value != "value" {
		t.Fatalf("value=%q", value)
	}
}
