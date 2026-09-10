package actionmodel

import (
	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
)

type ActionBusinessExecution struct {
	ID                 string         `json:"id"`
	WorkspaceID        string         `json:"workspace_id,omitempty"`
	ObjectKey          string         `json:"object_key"`
	RecordID           string         `json:"record_id,omitempty"`
	ActionKey          string         `json:"action_key"`
	IdempotencyKey     string         `json:"idempotency_key"`
	RequestFingerprint string         `json:"request_fingerprint,omitempty"`
	Status             string         `json:"status"`
	Result             map[string]any `json:"result,omitempty"`
	LeaseOwner         string         `json:"lease_owner,omitempty"`
	LeaseExpiresAt     string         `json:"lease_expires_at,omitempty"`
	FencingToken       int64          `json:"fencing_token,omitempty"`
	ResponseStatus     int            `json:"response_status,omitempty"`
	ErrorCode          string         `json:"error_code,omitempty"`
	ExpiresAt          string         `json:"expires_at,omitempty"`
	ActorID            string         `json:"actor_id,omitempty"`
	RoleKey            string         `json:"role_key,omitempty"`
	CreatedAt          string         `json:"created_at,omitempty"`
	UpdatedAt          string         `json:"updated_at,omitempty"`
}

type ActionExecutionClaimRequest struct {
	PreventReclaim     bool
	Execution          ActionBusinessExecution
	RequestFingerprint string
	LeaseOwner         string
	LeaseTTL           time.Duration
	Now                time.Time
}

type ActionExecutionClaimResult struct {
	Decision  idempotency.Decision
	Execution ActionBusinessExecution
}

type ActionExecutionCompletion struct {
	Execution      ActionBusinessExecution
	ExecutionID    string
	LeaseOwner     string
	FencingToken   int64
	Result         map[string]any
	ResponseStatus int
	ErrorCode      string
	Retryable      bool
	// AuditEvents are durable Action evidence committed with local Record
	// facts, DurableIntents and the fenced execution receipt.
	AuditEvents []auditmodel.AuditEvent
	ExpiresAt   time.Time
	Now         time.Time
}
