package runtimeext

import "context"

const WorkflowReadAgentTaskEvidenceOperation = "read_agent_task_evidence"
const WorkflowAgentTaskEvidenceUnavailableErrorCode = "backend.action.agent_task_evidence_unavailable"
const WorkflowAgentTaskEvidenceDeniedErrorCode = "backend.action.agent_task_evidence_denied"

// No caller-selected Workspace, Process or Task run ID is accepted. Runtime
// binds the read to the current Workflow Action and its exact target record.
type WorkflowAgentTaskEvidenceRequest struct {
	WorkflowKey string
	NodeID      string
	TaskKey     string
	TaskVersion string
}

type WorkflowAgentToolEvidence struct {
	Ref            string `json:"ref"`
	Tool           string `json:"tool"`
	Status         string `json:"status"`
	InputSHA256    string `json:"input_sha256,omitempty"`
	OutputSHA256   string `json:"output_sha256,omitempty"`
	ErrorCode      string `json:"error_code,omitempty"`
	StartedAt      int64  `json:"started_at,omitempty"`
	FinishedAt     int64  `json:"finished_at,omitempty"`
	DurationMillis int64  `json:"duration_ms,omitempty"`
}

// This is owner-proven execution metadata, never model output. It deliberately
// excludes task input/output, provider content, credentials and grant bundles.
type WorkflowAgentTaskEvidence struct {
	Available        bool                        `json:"available"`
	TaskRunID        string                      `json:"task_run_id,omitempty"`
	ProcessID        string                      `json:"process_id,omitempty"`
	NodeID           string                      `json:"node_id,omitempty"`
	NodeInstanceID   string                      `json:"node_instance_id,omitempty"`
	TaskKey          string                      `json:"task_key,omitempty"`
	TaskVersion      string                      `json:"task_version,omitempty"`
	AgentKey         string                      `json:"agent_key,omitempty"`
	Model            string                      `json:"model,omitempty"`
	Status           string                      `json:"status,omitempty"`
	Outcome          string                      `json:"outcome,omitempty"`
	ErrorCode        string                      `json:"error_code,omitempty"`
	ExecutionUserID  string                      `json:"execution_user_id,omitempty"`
	ExecutionRoleKey string                      `json:"execution_role_key,omitempty"`
	CompletedAt      int64                       `json:"completed_at,omitempty"`
	Tools            []WorkflowAgentToolEvidence `json:"tools,omitempty"`
}

type WorkflowAgentTaskEvidenceExecution interface {
	ReadWorkflowAgentTaskEvidence(context.Context, WorkflowAgentTaskEvidenceRequest) (WorkflowAgentTaskEvidence, error)
}

func ReadWorkflowAgentTaskEvidence(ctx context.Context, execution ActionExecution, request WorkflowAgentTaskEvidenceRequest) (WorkflowAgentTaskEvidence, error) {
	capability, ok := execution.(WorkflowAgentTaskEvidenceExecution)
	if !ok {
		return WorkflowAgentTaskEvidence{}, &BusinessError{Class: BusinessErrorUnavailable, Code: WorkflowAgentTaskEvidenceUnavailableErrorCode, Message: "Runtime Workflow Agent evidence is unavailable"}
	}
	return capability.ReadWorkflowAgentTaskEvidence(ctx, request)
}
