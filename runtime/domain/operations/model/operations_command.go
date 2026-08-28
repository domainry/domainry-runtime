package operationsmodel

import "time"

// OperationsStatus is the durable lifecycle of an operator-requested command.
type OperationsStatus string

const (
	OperationsStatusCreated   OperationsStatus = "created"
	OperationsStatusStarted   OperationsStatus = "started"
	OperationsStatusSucceeded OperationsStatus = "succeeded"
	OperationsStatusFailed    OperationsStatus = "failed"
)

// OperationsScope makes tenant and runtime-global operation boundaries
// explicit. Exactly one of WorkspaceID and SystemPurpose must be set.
type OperationsScope struct {
	WorkspaceID   string `json:"workspace_id,omitempty"`
	SystemPurpose string `json:"system_purpose,omitempty"`
	ResourceType  string `json:"resource_type"`
	ResourceID    string `json:"resource_id,omitempty"`
}

// OperationsCommand contains the identity, authorization intent and audit
// evidence shared by every Runtime operation owner.
type OperationsCommand struct {
	ID                 string           `json:"id"`
	Kind               string           `json:"kind"`
	Permission         string           `json:"permission"`
	Scope              OperationsScope  `json:"scope"`
	IdempotencyKey     string           `json:"idempotency_key"`
	RequestFingerprint string           `json:"request_fingerprint"`
	RequestedBy        string           `json:"requested_by"`
	Reason             string           `json:"reason"`
	Reference          string           `json:"reference,omitempty"`
	Status             OperationsStatus `json:"status"`
	CreatedAt          time.Time        `json:"created_at"`
	StartedAt          *time.Time       `json:"started_at,omitempty"`
	FinishedAt         *time.Time       `json:"finished_at,omitempty"`
	UpdatedAt          time.Time        `json:"updated_at"`
}
