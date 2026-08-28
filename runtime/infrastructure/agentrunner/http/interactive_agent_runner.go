package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	agentruntime "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
)

type InteractiveAgentRunner struct{ config Config }

func NewInteractiveAgentRunner(config Config) *InteractiveAgentRunner {
	normalized := NewAgentTaskRunner(config)
	return &InteractiveAgentRunner{config: normalized.config}
}

func (r *InteractiveAgentRunner) Run(ctx context.Context, request agentruntime.InteractiveAgentRunRequest) (agentruntime.InteractiveAgentResult, error) {
	if r == nil {
		return agentruntime.InteractiveAgentResult{}, apperror.New(apperror.KindUnavailable, "agent.interactive.runner_not_configured", nil, nil)
	}
	if r.config.BaseURL == "" {
		return agentruntime.InteractiveAgentResult{}, apperror.New(apperror.KindUnavailable, "agent.interactive.runner_not_configured", nil, nil)
	}
	if r.config.APIKey == "" {
		return agentruntime.InteractiveAgentResult{}, apperror.New(apperror.KindUnavailable, "agent.interactive.runner_not_configured", nil, nil)
	}
	if r.config.AgentID <= 0 {
		return agentruntime.InteractiveAgentResult{}, apperror.New(apperror.KindUnavailable, "agent.interactive.runner_not_configured", nil, nil)
	}
	payload := map[string]any{
		"agent_id": r.config.AgentID, "message": request.Message, "response_mode": "blocking", "external_session_id": request.SessionID,
		"metadata": map[string]any{
			"source": "domainry-interactive-agent", "interactive_run_id": request.RunID, "idempotency_key": request.IdempotencyKey,
			"runtime_context": request.Context, "route_candidates": request.Candidates, "max_steps": request.MaxSteps, "max_tool_calls": request.MaxToolCalls,
			"execution_credential": request.ExecutionCredential,
		},
	}
	body, _ := json.Marshal(payload)
	upstream, err := http.NewRequestWithContext(ctx, http.MethodPost, r.config.BaseURL+"/api/v1/agent-runs", bytes.NewReader(body))
	if err != nil {
		return agentruntime.InteractiveAgentResult{}, err
	}
	upstream.Header.Set("Authorization", "Bearer "+r.config.APIKey)
	upstream.Header.Set("Content-Type", "application/json")
	upstream.Header.Set("Accept", "application/json")
	upstream.Header.Set("Idempotency-Key", strings.TrimSpace(request.IdempotencyKey))
	response, err := r.config.Client.Do(upstream)
	if err != nil {
		return agentruntime.InteractiveAgentResult{}, apperror.New(apperror.KindUnavailable, "agent.interactive.transport_failed", err, nil)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxAgentResponseBytes+1))
	if err != nil {
		return agentruntime.InteractiveAgentResult{}, apperror.New(apperror.KindUnavailable, "agent.interactive.response_read_failed", err, nil)
	}
	if len(raw) > maxAgentResponseBytes {
		return agentruntime.InteractiveAgentResult{}, apperror.New(apperror.KindBadRequest, "agent.interactive.response_too_large", nil, nil)
	}
	if response.StatusCode/100 != 2 {
		return agentruntime.InteractiveAgentResult{}, apperror.New(apperror.KindUnavailable, "agent.interactive.provider_http_"+fmt.Sprint(response.StatusCode), nil, nil)
	}
	result, err := decodeInteractiveAgentResult(raw)
	if err != nil {
		return result, apperror.New(apperror.KindBadRequest, "agent.interactive.response_invalid", err, nil)
	}
	return result, nil
}

func decodeInteractiveAgentResult(raw []byte) (agentruntime.InteractiveAgentResult, error) {
	var payload map[string]any
	if len(raw) == 0 {
		return agentruntime.InteractiveAgentResult{}, fmt.Errorf("invalid interactive Agent response")
	}
	if json.Unmarshal(raw, &payload) != nil {
		return agentruntime.InteractiveAgentResult{}, fmt.Errorf("invalid interactive Agent response")
	}
	data := payload
	if nested, ok := payload["data"].(map[string]any); ok {
		data = nested
	}
	result := agentruntime.InteractiveAgentResult{
		ExternalRunID: firstAgentString(data, "external_run_id", "run_id", "id"), Status: firstAgentString(data, "status", "state"),
		Message: firstAgentString(data, "message", "content", "answer"), Model: firstAgentString(data, "model", "model_name"),
	}
	result.Structured, _ = firstAgentMap(data, "structured", "structured_output", "output", "result")
	result.Usage, _ = firstAgentMap(data, "usage")
	result.EvidenceRefs = interactiveAgentStrings(data["evidence_refs"])
	if route, ok := data["route"].(map[string]any); ok {
		result.Route = &agentruntime.AgentRouteResult{RouteType: firstAgentString(route, "route_type"), TargetKey: firstAgentString(route, "target_key"), TargetVersion: firstAgentString(route, "target_version", "version"), Reason: firstAgentString(route, "reason"), IdempotencyKey: firstAgentString(route, "idempotency_key")}
		result.Route.Input, _ = firstAgentMap(route, "input")
	}
	if handoff, ok := data["handoff"].(map[string]any); ok {
		encoded, _ := json.Marshal(handoff)
		var decoded agentmodel.InteractiveAgentHandoff
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			return result, err
		}
		result.Handoff = &decoded
	}
	if result.Status == "" {
		result.Status = "completed"
	}
	return result, nil
}

func interactiveAgentStrings(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
			result = append(result, strings.TrimSpace(text))
		}
	}
	return result
}

var _ agentruntime.InteractiveAgentRunner = (*InteractiveAgentRunner)(nil)
