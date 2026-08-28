package http

import (
	"encoding/json"
	"fmt"
	"strings"

	agentruntime "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
)

func decodeAgentResult(raw []byte) (agentruntime.AgentTaskRunnerResult, error) {
	var payload map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &payload) != nil {
		return agentruntime.AgentTaskRunnerResult{RawEvidence: append([]byte(nil), raw...)}, fmt.Errorf("invalid agent response JSON")
	}
	data := payload
	if nested, ok := payload["data"].(map[string]any); ok {
		data = nested
	}
	result := agentruntime.AgentTaskRunnerResult{
		ExternalRunID: firstAgentString(data, "external_run_id", "run_id", "id"),
		Status:        normalizeAgentStatus(firstAgentString(data, "status", "state")),
		Outcome:       firstAgentString(data, "outcome", "result_type"),
		Model:         firstAgentString(data, "model", "model_name"),
		ErrorClass:    firstAgentString(data, "error_class"),
		ErrorCode:     firstAgentString(data, "error_code", "code"),
		RawEvidence:   append([]byte(nil), raw...),
	}
	result.Output, _ = firstAgentMap(data, "output", "structured_output", "result")
	result.Usage, _ = firstAgentMap(data, "usage")
	if retryable, ok := data["retryable"].(bool); ok {
		result.Retryable = retryable
	}
	if result.Status == agentruntime.AgentProviderRunCompleted {
		if result.Outcome == "" {
			result.Outcome = "success"
		}
	}
	if result.Status == "" {
		return result, fmt.Errorf("agent response status is missing")
	}
	return result, nil
}

func normalizeAgentStatus(value string) agentruntime.AgentProviderRunStatus {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "accepted", "queued", "pending":
		return agentruntime.AgentProviderRunAccepted
	case "running", "in_progress", "processing":
		return agentruntime.AgentProviderRunRunning
	case "completed", "succeeded", "success":
		return agentruntime.AgentProviderRunCompleted
	case "failed", "error":
		return agentruntime.AgentProviderRunFailed
	case "cancelled", "canceled":
		return agentruntime.AgentProviderRunCancelled
	case "unknown":
		return agentruntime.AgentProviderRunUnknown
	default:
		return ""
	}
}

func firstAgentString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok {
			if strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

func firstAgentMap(payload map[string]any, keys ...string) (map[string]any, bool) {
	for _, key := range keys {
		if value, ok := payload[key].(map[string]any); ok {
			return value, true
		}
	}
	return nil, false
}
