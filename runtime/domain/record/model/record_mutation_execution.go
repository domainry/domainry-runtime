package recordmodel

import (
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
)

type RecordMutationExecution struct {
	ID                 string         `json:"id"`
	WorkspaceID        string         `json:"workspace_id"`
	Operation          string         `json:"operation"`
	ObjectKey          string         `json:"object_key"`
	TargetID           string         `json:"target_id,omitempty"`
	IdempotencyKey     string         `json:"idempotency_key"`
	RequestFingerprint string         `json:"request_fingerprint"`
	Status             string         `json:"status"`
	Result             Record         `json:"result"`
	OperationResult    map[string]any `json:"operation_result,omitempty"`
	LeaseOwner         string         `json:"lease_owner"`
	LeaseExpiresAt     string         `json:"lease_expires_at"`
	FencingToken       int64          `json:"fencing_token"`
	ResponseStatus     int            `json:"response_status"`
	ErrorCode          string         `json:"error_code,omitempty"`
	ExpiresAt          string         `json:"expires_at,omitempty"`
	ActorID            string         `json:"actor_id,omitempty"`
	CreatedAt          string         `json:"created_at"`
	UpdatedAt          string         `json:"updated_at"`
}

type RecordMutationClaimRequest struct {
	Execution          RecordMutationExecution
	RequestFingerprint string
	LeaseOwner         string
	LeaseTTL           time.Duration
	Now                time.Time
}

type RecordMutationClaimResult struct {
	Decision  idempotency.Decision
	Execution RecordMutationExecution
}

type RecordMutationCompletion struct {
	WorkspaceID  string
	ExecutionID  string
	LeaseOwner   string
	FencingToken int64
	Result       any
	ExpiresAt    time.Time
	Now          time.Time
}
