package http

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	agentruntime "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
)

func TestInteractiveAgentRunnerUsesDistinctTypedContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("Idempotency-Key") != "message-1" || !strings.Contains(string(body), `"interactive_run_id":"interactive-1"`) || !strings.Contains(string(body), `"route_type":"agent_task"`) || !strings.Contains(string(body), `"runtime_context"`) {
			t.Fatalf("headers=%v body=%s", r.Header, body)
		}
		_, _ = w.Write([]byte(`{"data":{"id":"external-1","status":"handoff","model":"model-1","usage":{"tokens":9},"route":{"route_type":"task","target_key":"customer.review","target_version":"1.0.0","input":{"record_id":"one"},"idempotency_key":"handoff-1"},"evidence_refs":["audit-1"]}}`))
	}))
	defer server.Close()
	runner := NewInteractiveAgentRunner(Config{BaseURL: server.URL, APIKey: "secret", AgentID: 7})
	result, err := runner.Run(t.Context(), agentruntime.InteractiveAgentRunRequest{
		RunID: "interactive-1", SessionID: "session-1", IdempotencyKey: "message-1", Message: "review",
		Context: agentmodel.GlobalAgentContext{ContextRevision: "context-1"}, Candidates: []agentruntime.AgentRouteCandidate{{RouteType: agentmodel.AgentRouteTask, TargetKey: "customer.review", Version: "1.0.0"}},
	})
	if err != nil || result.ExternalRunID != "external-1" || result.Route == nil || result.Route.TargetKey != "customer.review" || result.Model != "model-1" || len(result.EvidenceRefs) != 1 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestInteractiveAgentRunnerRejectsProviderAndContractFailures(t *testing.T) {
	if _, err := NewInteractiveAgentRunner(Config{}).Run(t.Context(), agentruntime.InteractiveAgentRunRequest{}); apperror.CodeOf(err) != "agent.interactive.runner_not_configured" {
		t.Fatalf("configuration err=%v", err)
	}
	for name, response := range map[string]struct {
		status int
		body   string
		code   string
	}{
		"provider": {status: 503, body: `{}`, code: "agent.interactive.provider_http_503"},
		"json":     {status: 200, body: `{`, code: "agent.interactive.response_invalid"},
		"handoff":  {status: 200, body: `{"handoff":{"contract_version":"interactive-handoff-v1","route_type":"root","target_key":"bad","idempotency_key":"one"}}`, code: "agent.interactive.response_invalid"},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(response.status)
				_, _ = w.Write([]byte(response.body))
			}))
			defer server.Close()
			_, err := NewInteractiveAgentRunner(Config{BaseURL: server.URL, APIKey: "secret", AgentID: 1}).Run(t.Context(), agentruntime.InteractiveAgentRunRequest{})
			if apperror.CodeOf(err) != response.code {
				t.Fatalf("code=%q err=%v", apperror.CodeOf(err), err)
			}
		})
	}
}
