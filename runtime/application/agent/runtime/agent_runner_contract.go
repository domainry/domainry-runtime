package runtime

import (
	"context"
	"time"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
)

type AgentProviderRunStatus string

const (
	AgentProviderRunAccepted  AgentProviderRunStatus = "accepted"
	AgentProviderRunRunning   AgentProviderRunStatus = "running"
	AgentProviderRunCompleted AgentProviderRunStatus = "completed"
	AgentProviderRunFailed    AgentProviderRunStatus = "failed"
	AgentProviderRunCancelled AgentProviderRunStatus = "cancelled"
	AgentProviderRunUnknown   AgentProviderRunStatus = "unknown"
)

type AgentTaskRunnerRequest struct {
	TaskRunID           string
	ProcessID           string
	WorkspaceID         string
	Task                agentmodel.AgentTaskDefinition
	Identity            agentmodel.AgentExecutionIdentity
	Input               map[string]any
	ExecutionCredential string
	CorrelationID       string
	IdempotencyKey      string
	Deadline            time.Time
}

type AgentTaskRunnerResult struct {
	ExternalRunID string
	Status        AgentProviderRunStatus
	Outcome       string
	Output        map[string]any
	RawEvidence   []byte
	Model         string
	Usage         map[string]any
	ErrorClass    string
	ErrorCode     string
	Retryable     bool
}

type AgentTaskRunner interface {
	Start(context.Context, AgentTaskRunnerRequest) (AgentTaskRunnerResult, error)
	Poll(context.Context, string, string) (AgentTaskRunnerResult, error)
	Cancel(context.Context, string, string) (AgentTaskRunnerResult, error)
}

type InteractiveAgentRunRequest struct {
	RunID               string                        `json:"run_id"`
	SessionID           string                        `json:"session_id"`
	Context             agentmodel.GlobalAgentContext `json:"context"`
	Message             string                        `json:"message"`
	ExecutionCredential string                        `json:"execution_credential,omitempty"`
	IdempotencyKey      string                        `json:"idempotency_key"`
	Candidates          []AgentRouteCandidate         `json:"candidates"`
	MaxSteps            int                           `json:"max_steps,omitempty"`
	MaxToolCalls        int                           `json:"max_tool_calls,omitempty"`
	Deadline            time.Time                     `json:"deadline"`
}

type AgentRouteCandidate struct {
	RouteType string `json:"route_type"`
	TargetKey string `json:"target_key"`
	Version   string `json:"version,omitempty"`
}

type AgentRouteResult struct {
	RouteType      string         `json:"route_type"`
	TargetKey      string         `json:"target_key"`
	TargetVersion  string         `json:"target_version,omitempty"`
	Input          map[string]any `json:"input,omitempty"`
	Reason         string         `json:"reason,omitempty"`
	IdempotencyKey string         `json:"idempotency_key,omitempty"`
}

type InteractiveAgentResult struct {
	RunID         string                              `json:"run_id"`
	ExternalRunID string                              `json:"external_run_id,omitempty"`
	Status        string                              `json:"status"`
	Message       string                              `json:"message,omitempty"`
	Structured    map[string]any                      `json:"structured,omitempty"`
	Model         string                              `json:"model,omitempty"`
	Usage         map[string]any                      `json:"usage,omitempty"`
	Route         *AgentRouteResult                   `json:"route,omitempty"`
	Handoff       *agentmodel.InteractiveAgentHandoff `json:"handoff,omitempty"`
	EvidenceRefs  []string                            `json:"evidence_refs,omitempty"`
}

type InteractiveAgentRunner interface {
	Run(context.Context, InteractiveAgentRunRequest) (InteractiveAgentResult, error)
}

type AgentRouter interface {
	Route(context.Context, agentmodel.GlobalAgentContext, string, []AgentRouteCandidate) (AgentRouteResult, error)
}
