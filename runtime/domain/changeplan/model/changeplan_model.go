package changeplanmodel

import (
	"encoding/json"
	"time"

	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
)

type BusinessChangePlanDraft struct {
	WorkspaceID string          `json:"workspace_id"`
	PlanID      string          `json:"plan_id"`
	Revision    int             `json:"revision"`
	Status      string          `json:"status"`
	Payload     json.RawMessage `json:"payload"`
	CreatedBy   string          `json:"created_by"`
	UpdatedBy   string          `json:"updated_by"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
}

type BusinessChangePlanPublication struct {
	WorkspaceID      string
	PlanID           string
	ExpectedRevision int
	UpdatedBy        string
	UpdatedAt        string
}

type BusinessChangePlanDraftConflictError struct{ PlanID string }

func (err *BusinessChangePlanDraftConflictError) Error() string {
	return "domain change plan draft conflict: " + err.PlanID
}

type ChangePlanOperationExecution struct {
	ID                 string          `json:"id"`
	WorkspaceID        string          `json:"workspace_id"`
	PlanID             string          `json:"plan_id"`
	PlanRevision       int             `json:"plan_revision"`
	Operation          string          `json:"operation"`
	IdempotencyKey     string          `json:"idempotency_key"`
	RequestFingerprint string          `json:"request_fingerprint"`
	Status             string          `json:"status"`
	Result             json.RawMessage `json:"result,omitempty"`
	LeaseOwner         string          `json:"lease_owner"`
	LeaseExpiresAt     string          `json:"lease_expires_at"`
	FencingToken       int64           `json:"fencing_token"`
	ErrorCode          string          `json:"error_code,omitempty"`
	ExpiresAt          string          `json:"expires_at,omitempty"`
	ActorID            string          `json:"actor_id,omitempty"`
	CreatedAt          string          `json:"created_at"`
	UpdatedAt          string          `json:"updated_at"`
}

type ChangePlanOperationClaimRequest struct {
	Execution          ChangePlanOperationExecution
	RequestFingerprint string
	LeaseOwner         string
	LeaseTTL           time.Duration
	Now                time.Time
}

type ChangePlanOperationClaimResult struct {
	Decision  idempotency.Decision
	Execution ChangePlanOperationExecution
}

type ChangePlanOperationCompletion struct {
	ExecutionID  string
	LeaseOwner   string
	FencingToken int64
	Result       any
	ExpiresAt    time.Time
	Now          time.Time
}

type ChangePlanOperationFailure struct {
	ExecutionID  string
	LeaseOwner   string
	FencingToken int64
	ErrorCode    string
	Result       any
	Retryable    bool
	ExpiresAt    time.Time
	Now          time.Time
}
