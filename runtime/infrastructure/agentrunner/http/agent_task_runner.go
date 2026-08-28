package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	agentruntime "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
)

const maxAgentResponseBytes = 2 << 20

type Config struct {
	BaseURL string
	APIKey  string
	AgentID int
	Timeout time.Duration
	Client  *http.Client
}

type AgentTaskRunner struct{ config Config }

func NewAgentTaskRunner(config Config) *AgentTaskRunner {
	config.BaseURL = strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	config.APIKey = strings.TrimSpace(config.APIKey)
	if config.Client == nil {
		config.Client = &http.Client{Timeout: config.Timeout}
	}
	return &AgentTaskRunner{config: config}
}

func (r *AgentTaskRunner) Start(ctx context.Context, request agentruntime.AgentTaskRunnerRequest) (agentruntime.AgentTaskRunnerResult, error) {
	message, err := agentTaskMessage(request)
	if err != nil {
		return agentruntime.AgentTaskRunnerResult{}, err
	}
	payload := map[string]any{
		"agent_id": r.config.AgentID, "message": message, "response_mode": "async",
		"external_session_id": "agent-task:" + request.WorkspaceID + ":" + request.TaskRunID,
		"metadata": map[string]any{
			"source": "domainry-agent-task-worker", "workspace_id": request.WorkspaceID,
			"process_id": request.ProcessID, "task_run_id": request.TaskRunID,
			"task_key": request.Task.Key, "task_version": request.Task.Version,
			"correlation_id": request.CorrelationID, "idempotency_key": request.IdempotencyKey,
			"execution_credential": request.ExecutionCredential,
			"tool_endpoint":        "/agent-dialog/task-tools/invoke",
		},
	}
	return r.call(ctx, http.MethodPost, "/api/v1/agent-runs", payload, request.IdempotencyKey)
}

func (r *AgentTaskRunner) Poll(ctx context.Context, externalRunID, idempotencyKey string) (agentruntime.AgentTaskRunnerResult, error) {
	return r.call(ctx, http.MethodGet, "/api/v1/agent-runs/"+url.PathEscape(strings.TrimSpace(externalRunID)), nil, idempotencyKey)
}

func (r *AgentTaskRunner) Cancel(ctx context.Context, externalRunID, idempotencyKey string) (agentruntime.AgentTaskRunnerResult, error) {
	return r.call(ctx, http.MethodPost, "/api/v1/agent-runs/"+url.PathEscape(strings.TrimSpace(externalRunID))+"/cancel", map[string]any{}, idempotencyKey)
}

func (r *AgentTaskRunner) call(ctx context.Context, method, path string, payload any, idempotencyKey string) (agentruntime.AgentTaskRunnerResult, error) {
	if r == nil {
		return agentruntime.AgentTaskRunnerResult{ErrorClass: "configuration", ErrorCode: "agent.runner.not_configured"}, fmt.Errorf("agent task runner is not configured")
	}
	if r.config.BaseURL == "" {
		return agentruntime.AgentTaskRunnerResult{ErrorClass: "configuration", ErrorCode: "agent.runner.not_configured"}, fmt.Errorf("agent task runner is not configured")
	}
	if r.config.APIKey == "" {
		return agentruntime.AgentTaskRunnerResult{ErrorClass: "configuration", ErrorCode: "agent.runner.not_configured"}, fmt.Errorf("agent task runner is not configured")
	}
	if r.config.AgentID <= 0 {
		return agentruntime.AgentTaskRunnerResult{ErrorClass: "configuration", ErrorCode: "agent.runner.not_configured"}, fmt.Errorf("agent task runner is not configured")
	}
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return agentruntime.AgentTaskRunnerResult{}, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, r.config.BaseURL+path, body)
	if err != nil {
		return agentruntime.AgentTaskRunnerResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+r.config.APIKey)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key := strings.TrimSpace(idempotencyKey); key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := r.config.Client.Do(req)
	if err != nil {
		return agentruntime.AgentTaskRunnerResult{ErrorClass: "transport", ErrorCode: "agent.runner.transport_failed", Retryable: true}, err
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxAgentResponseBytes+1))
	if readErr != nil {
		return agentruntime.AgentTaskRunnerResult{ErrorClass: "transport", ErrorCode: "agent.runner.response_read_failed", Retryable: true}, readErr
	}
	if len(raw) > maxAgentResponseBytes {
		return agentruntime.AgentTaskRunnerResult{ErrorClass: "provider_contract", ErrorCode: "agent.runner.response_too_large"}, fmt.Errorf("agent response exceeds %d bytes", maxAgentResponseBytes)
	}
	result, decodeErr := decodeAgentResult(raw)
	if resp.StatusCode/100 != 2 {
		result.ErrorClass, result.ErrorCode = "provider_http", "agent.runner.provider_http_"+strconv.Itoa(resp.StatusCode)
		result.Retryable = resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		return result, fmt.Errorf("agent provider returned HTTP %d", resp.StatusCode)
	}
	if decodeErr != nil {
		result.ErrorClass, result.ErrorCode = "provider_contract", "agent.runner.response_invalid"
		return result, decodeErr
	}
	return result, nil
}

func agentTaskMessage(request agentruntime.AgentTaskRunnerRequest) (string, error) {
	input, err := json.Marshal(request.Input)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(request.Task.Instruction) + "\n\nReturn only the declared structured result.\nInput:\n" + string(input), nil
}
