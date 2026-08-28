package agentmodel

import (
	"strings"
	"time"
)

type AgentInteractiveRunStatus string

const (
	AgentInteractiveRunRunning   AgentInteractiveRunStatus = "running"
	AgentInteractiveRunCompleted AgentInteractiveRunStatus = "completed"
	AgentInteractiveRunHandedOff AgentInteractiveRunStatus = "handed_off"
	AgentInteractiveRunFailed    AgentInteractiveRunStatus = "failed"
	AgentInteractiveRunCancelled AgentInteractiveRunStatus = "cancelled"
)

func (status AgentInteractiveRunStatus) Terminal() bool {
	return status == AgentInteractiveRunCompleted || status == AgentInteractiveRunHandedOff || status == AgentInteractiveRunFailed || status == AgentInteractiveRunCancelled
}

type AgentInteractiveRun struct {
	ID                  string                            `json:"id"`
	ExternalRunID       string                            `json:"external_run_id,omitempty"`
	SessionID           string                            `json:"session_id"`
	WorkspaceID         string                            `json:"workspace_id"`
	UserID              string                            `json:"user_id"`
	RoleKey             string                            `json:"role_key"`
	Surface             string                            `json:"surface"`
	RouteKey            string                            `json:"route_key"`
	AgentKey            string                            `json:"agent_key"`
	EntrypointKey       string                            `json:"entrypoint_key"`
	ContextRevision     string                            `json:"context_revision"`
	Context             GlobalAgentContext                `json:"context"`
	Authorization       AgentAuthorizationEvidence        `json:"authorization"`
	Status              AgentInteractiveRunStatus         `json:"status"`
	RouteType           string                            `json:"route_type,omitempty"`
	RoutedTargetKey     string                            `json:"routed_target_key,omitempty"`
	RoutedTargetVersion string                            `json:"routed_target_version,omitempty"`
	ProcessID           string                            `json:"process_id,omitempty"`
	TaskRunID           string                            `json:"task_run_id,omitempty"`
	ProposalID          string                            `json:"proposal_id,omitempty"`
	StructuredResult    map[string]any                    `json:"structured_result,omitempty"`
	Model               string                            `json:"model,omitempty"`
	Usage               map[string]any                    `json:"usage,omitempty"`
	ToolCallCount       int                               `json:"tool_call_count"`
	ToolInvocations     []AgentTaskToolInvocationEvidence `json:"tool_invocations,omitempty"`
	ErrorCode           string                            `json:"error_code,omitempty"`
	CorrelationID       string                            `json:"correlation_id,omitempty"`
	IdempotencyKey      string                            `json:"idempotency_key"`
	CreatedAt           time.Time                         `json:"created_at"`
	UpdatedAt           time.Time                         `json:"updated_at"`
	CompletedAt         *time.Time                        `json:"completed_at,omitempty"`
	Revision            int64                             `json:"revision"`
}

func (run AgentInteractiveRun) ValidForCreate() bool {
	required := []string{run.ID, run.SessionID, run.WorkspaceID, run.UserID, run.RoleKey, run.Surface, run.EntrypointKey, run.ContextRevision, run.IdempotencyKey}
	for _, value := range required {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return run.Status == AgentInteractiveRunRunning && !run.CreatedAt.IsZero() && run.Context.ContextRevision == run.ContextRevision && run.Context.EntrypointKey == run.EntrypointKey && run.Context.AgentKey == run.AgentKey
}
