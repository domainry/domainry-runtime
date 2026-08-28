package agentmodel

import (
	"strings"
	"time"
)

type AgentTaskLease struct {
	Owner        string    `json:"owner"`
	FencingToken int64     `json:"fencing_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func (lease AgentTaskLease) Matches(owner string, token int64) bool {
	return strings.TrimSpace(owner) != "" && lease.Owner == owner && lease.FencingToken == token && !lease.ExpiresAt.IsZero()
}

type AgentTaskRunStatus string

const (
	AgentTaskRunPending         AgentTaskRunStatus = "pending"
	AgentTaskRunRunning         AgentTaskRunStatus = "running"
	AgentTaskRunWaitingApproval AgentTaskRunStatus = "waiting_approval"
	AgentTaskRunRetryScheduled  AgentTaskRunStatus = "retry_scheduled"
	AgentTaskRunSucceeded       AgentTaskRunStatus = "succeeded"
	AgentTaskRunManualReview    AgentTaskRunStatus = "manual_review"
	AgentTaskRunRejected        AgentTaskRunStatus = "rejected"
	AgentTaskRunNoResult        AgentTaskRunStatus = "no_result"
	AgentTaskRunFailed          AgentTaskRunStatus = "failed"
	AgentTaskRunCancelled       AgentTaskRunStatus = "cancelled"
	AgentTaskRunDeadLetter      AgentTaskRunStatus = "dead_letter"
)

func (status AgentTaskRunStatus) Terminal() bool {
	switch status {
	case AgentTaskRunSucceeded, AgentTaskRunManualReview, AgentTaskRunRejected, AgentTaskRunNoResult, AgentTaskRunFailed, AgentTaskRunCancelled, AgentTaskRunDeadLetter:
		return true
	default:
		return false
	}
}

type AgentTaskAttempt struct {
	Number        int        `json:"number"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
	ExternalRunID string     `json:"external_run_id,omitempty"`
	ErrorClass    string     `json:"error_class,omitempty"`
	ErrorCode     string     `json:"error_code,omitempty"`
	Retryable     bool       `json:"retryable,omitempty"`
}

type AgentTaskExecutionEvidence struct {
	ManifestHash           string                            `json:"manifest_hash"`
	DefinitionSnapshotHash string                            `json:"definition_snapshot_hash"`
	TaskVersion            string                            `json:"task_version"`
	AgentKey               string                            `json:"agent_key"`
	PromptVersion          string                            `json:"prompt_version,omitempty"`
	Model                  string                            `json:"model,omitempty"`
	Authorization          []AgentAuthorizationEvidence      `json:"authorization,omitempty"`
	ToolInvocationRefs     []string                          `json:"tool_invocation_refs,omitempty"`
	ToolInvocations        []AgentTaskToolInvocationEvidence `json:"tool_invocations,omitempty"`
	AuditRefs              []string                          `json:"audit_refs,omitempty"`
	Usage                  map[string]any                    `json:"usage,omitempty"`
}

type AgentTaskToolInvocationEvidence struct {
	Ref                  string                     `json:"ref"`
	Tool                 string                     `json:"tool"`
	InputHash            string                     `json:"input_hash"`
	OutputHash           string                     `json:"output_hash,omitempty"`
	Status               string                     `json:"status"`
	ErrorCode            string                     `json:"error_code,omitempty"`
	Authorization        AgentAuthorizationEvidence `json:"authorization"`
	StartedAt            time.Time                  `json:"started_at"`
	FinishedAt           *time.Time                 `json:"finished_at,omitempty"`
	DurationMilliseconds int64                      `json:"duration_ms,omitempty"`
	CostUnits            int                        `json:"cost_units,omitempty"`
}

type AgentTaskReconciliation struct {
	Required      bool       `json:"required"`
	ExternalRunID string     `json:"external_run_id,omitempty"`
	State         string     `json:"state,omitempty"`
	LastCheckedAt *time.Time `json:"last_checked_at,omitempty"`
	Reason        string     `json:"reason,omitempty"`
}

type AgentTaskApproval struct {
	ProposalID  string         `json:"proposal_id"`
	Status      string         `json:"status"`
	Decision    string         `json:"decision,omitempty"`
	Actor       string         `json:"actor,omitempty"`
	Reason      string         `json:"reason,omitempty"`
	Execution   map[string]any `json:"execution,omitempty"`
	RequestedAt time.Time      `json:"requested_at"`
	ExpiresAt   time.Time      `json:"expires_at"`
	ResolvedAt  *time.Time     `json:"resolved_at,omitempty"`
}

type AgentTaskManualOverride struct {
	OriginalOutput map[string]any `json:"original_output,omitempty"`
	NewOutput      map[string]any `json:"new_output,omitempty"`
	Actor          string         `json:"actor"`
	Reason         string         `json:"reason"`
	AuditRef       string         `json:"audit_ref"`
	CreatedAt      time.Time      `json:"created_at"`
}

type AgentTaskOperationEvidence struct {
	Kind           string    `json:"kind"`
	IdempotencyKey string    `json:"idempotency_key"`
	Actor          string    `json:"actor"`
	Reason         string    `json:"reason"`
	AuditRef       string    `json:"audit_ref"`
	CreatedAt      time.Time `json:"created_at"`
}

type AgentTaskRun struct {
	ID                 string                       `json:"id"`
	WorkspaceID        string                       `json:"workspace_id"`
	ProcessID          string                       `json:"process_id,omitempty"`
	NodeInstanceID     string                       `json:"node_instance_id,omitempty"`
	InteractiveRunID   string                       `json:"interactive_run_id,omitempty"`
	TaskKey            string                       `json:"task_key"`
	TaskVersion        string                       `json:"task_version"`
	Status             AgentTaskRunStatus           `json:"status"`
	Outcome            string                       `json:"outcome,omitempty"`
	Identity           AgentExecutionIdentity       `json:"identity"`
	Input              map[string]any               `json:"input,omitempty"`
	Output             map[string]any               `json:"output,omitempty"`
	RawEvidenceRef     string                       `json:"raw_evidence_ref,omitempty"`
	Attempt            int                          `json:"attempt"`
	MaxAttempts        int                          `json:"max_attempts"`
	TimeoutSeconds     int                          `json:"timeout_seconds"`
	ToolCallCount      int                          `json:"tool_call_count"`
	NextAttemptAt      *time.Time                   `json:"next_attempt_at,omitempty"`
	Lease              AgentTaskLease               `json:"lease,omitempty"`
	CancelRequestedAt  *time.Time                   `json:"cancel_requested_at,omitempty"`
	CancellationReason string                       `json:"cancellation_reason,omitempty"`
	IdempotencyKey     string                       `json:"idempotency_key"`
	CorrelationID      string                       `json:"correlation_id,omitempty"`
	Attempts           []AgentTaskAttempt           `json:"attempts,omitempty"`
	Evidence           AgentTaskExecutionEvidence   `json:"evidence"`
	Approval           *AgentTaskApproval           `json:"approval,omitempty"`
	ManualOverrides    []AgentTaskManualOverride    `json:"manual_overrides,omitempty"`
	Operations         []AgentTaskOperationEvidence `json:"operations,omitempty"`
	Reconciliation     AgentTaskReconciliation      `json:"reconciliation"`
	LastErrorCode      string                       `json:"last_error_code,omitempty"`
	CreatedAt          time.Time                    `json:"created_at"`
	UpdatedAt          time.Time                    `json:"updated_at"`
	CompletedAt        *time.Time                   `json:"completed_at,omitempty"`
	Revision           int64                        `json:"revision"`
}

func (run AgentTaskRun) ValidForCreate() bool {
	return strings.TrimSpace(run.ID) != "" && strings.TrimSpace(run.WorkspaceID) != "" && strings.TrimSpace(run.TaskKey) != "" && strings.TrimSpace(run.TaskVersion) != "" && strings.TrimSpace(run.IdempotencyKey) != "" && run.Status == AgentTaskRunPending && run.MaxAttempts > 0 && run.Attempt == 0 && !run.CreatedAt.IsZero()
}
