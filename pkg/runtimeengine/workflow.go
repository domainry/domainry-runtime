package runtimeengine

import "context"

// RecordWorkflowReader exposes execution metadata for exactly one readable
// business record. It grants neither Workflow operations nor access to Task
// inputs, variables, outputs, definitions, credentials or recovery commands.
type RecordWorkflowReader interface {
	ReadRecordWorkflow(context.Context, string, string, string) (RecordWorkflowStatus, error)
}

type RecordWorkflowStatus struct {
	Found       bool   `json:"found"`
	ProcessID   string `json:"process_id,omitempty"`
	WorkflowKey string `json:"workflow_key,omitempty"`
	Status      string `json:"status,omitempty"`
	ErrorCode   string `json:"error_code,omitempty"`
	StartedAt   int64  `json:"started_at,omitempty"`
	UpdatedAt   int64  `json:"updated_at,omitempty"`
	CompletedAt int64  `json:"completed_at,omitempty"`
}
