package http

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	agentruntime "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type agentRoundTripFunc func(*http.Request) (*http.Response, error)

func (f agentRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type failingAgentBody struct{}

func (failingAgentBody) Read([]byte) (int, error) { return 0, errors.New("read failure") }
func (failingAgentBody) Close() error             { return nil }

func agentResponseClient(status int, body io.ReadCloser) *http.Client {
	return &http.Client{Transport: agentRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: body}, nil
	})}
}

func TestAgentTaskRunnerRemainingFailureBranches(t *testing.T) {
	for _, candidate := range []*AgentTaskRunner{nil, {config: Config{BaseURL: "https://agent.example"}}, {config: Config{BaseURL: "https://agent.example", APIKey: "key"}}} {
		if _, err := candidate.call(t.Context(), http.MethodGet, "/run", nil, ""); err == nil {
			t.Fatal("incomplete task runner accepted")
		}
	}
	runner := NewAgentTaskRunner(Config{BaseURL: "https://agent.example", APIKey: "key", AgentID: 1})
	if _, err := runner.Start(t.Context(), agentruntime.AgentTaskRunnerRequest{Input: map[string]any{"bad": make(chan int)}}); err == nil {
		t.Fatal("invalid input accepted")
	}
	if _, err := runner.call(t.Context(), http.MethodPost, "/run", map[string]any{"bad": make(chan int)}, ""); err == nil {
		t.Fatal("invalid payload accepted")
	}
	invalidURL := NewAgentTaskRunner(Config{BaseURL: "://invalid", APIKey: "key", AgentID: 1})
	if _, err := invalidURL.Poll(t.Context(), "run", ""); err == nil {
		t.Fatal("invalid URL accepted")
	}
	readRunner := NewAgentTaskRunner(Config{BaseURL: "https://agent.example", APIKey: "key", AgentID: 1, Client: agentResponseClient(http.StatusOK, failingAgentBody{})})
	if result, err := readRunner.Poll(t.Context(), "run", ""); err == nil || result.ErrorCode != "agent.runner.response_read_failed" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	largeRunner := NewAgentTaskRunner(Config{BaseURL: "https://agent.example", APIKey: "key", AgentID: 1, Client: agentResponseClient(http.StatusOK, io.NopCloser(strings.NewReader(strings.Repeat("x", maxAgentResponseBytes+1))))})
	if result, err := largeRunner.Poll(t.Context(), "run", ""); err == nil || result.ErrorCode != "agent.runner.response_too_large" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if _, err := agentTaskMessage(agentruntime.AgentTaskRunnerRequest{Input: map[string]any{"bad": make(chan int), "task": agentmodel.AgentTaskDefinition{}}}); err == nil {
		t.Fatal("message input accepted")
	}
}

func TestInteractiveAgentRunnerRemainingFailureBranches(t *testing.T) {
	for _, candidate := range []*InteractiveAgentRunner{nil, {config: Config{BaseURL: "https://agent.example"}}, {config: Config{BaseURL: "https://agent.example", APIKey: "key"}}} {
		if _, err := candidate.Run(t.Context(), agentruntime.InteractiveAgentRunRequest{}); apperror.CodeOf(err) != "agent.interactive.runner_not_configured" {
			t.Fatalf("configuration err=%v", err)
		}
	}
	invalidURL := NewInteractiveAgentRunner(Config{BaseURL: "://invalid", APIKey: "key", AgentID: 1})
	if _, err := invalidURL.Run(t.Context(), agentruntime.InteractiveAgentRunRequest{}); err == nil {
		t.Fatal("invalid URL accepted")
	}
	readRunner := NewInteractiveAgentRunner(Config{BaseURL: "https://agent.example", APIKey: "key", AgentID: 1, Client: agentResponseClient(http.StatusOK, failingAgentBody{})})
	if _, err := readRunner.Run(t.Context(), agentruntime.InteractiveAgentRunRequest{}); apperror.CodeOf(err) != "agent.interactive.response_read_failed" {
		t.Fatalf("err=%v", err)
	}
	largeRunner := NewInteractiveAgentRunner(Config{BaseURL: "https://agent.example", APIKey: "key", AgentID: 1, Client: agentResponseClient(http.StatusOK, io.NopCloser(strings.NewReader(strings.Repeat("x", maxAgentResponseBytes+1))))})
	if _, err := largeRunner.Run(t.Context(), agentruntime.InteractiveAgentRunRequest{}); apperror.CodeOf(err) != "agent.interactive.response_too_large" {
		t.Fatalf("err=%v", err)
	}
	provider := NewInteractiveAgentRunner(Config{BaseURL: "https://agent.example", APIKey: "key", AgentID: 1, Client: agentResponseClient(http.StatusTeapot, io.NopCloser(strings.NewReader(`{}`)))})
	if _, err := provider.Run(t.Context(), agentruntime.InteractiveAgentRunRequest{}); apperror.CodeOf(err) != "agent.interactive.provider_http_418" {
		t.Fatalf("err=%v", err)
	}
	result, err := decodeInteractiveAgentResult([]byte(`{"evidence_refs":[" one ",3,""],"route":{"route_type":"task","input":{"id":"one"}}}`))
	if err != nil || result.Status != "completed" || len(result.EvidenceRefs) != 1 || result.Route == nil {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if _, err := decodeInteractiveAgentResult(nil); err == nil {
		t.Fatal("empty interactive response accepted")
	}
	if result, err := decodeInteractiveAgentResult([]byte(`{"status":"completed","handoff":{"contract_version":"interactive-handoff-v1","route_type":"agent_task","target_key":"review","idempotency_key":"one"}}`)); err != nil || result.Handoff == nil {
		t.Fatalf("handoff=%#v err=%v", result.Handoff, err)
	}
	if values := interactiveAgentStrings("not-list"); values != nil {
		t.Fatalf("values=%v", values)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	transport := NewInteractiveAgentRunner(Config{BaseURL: "https://agent.invalid", APIKey: "key", AgentID: 1})
	if _, err := transport.Run(cancelled, agentruntime.InteractiveAgentRunRequest{}); apperror.CodeOf(err) != "agent.interactive.transport_failed" {
		t.Fatalf("err=%v", err)
	}
}
