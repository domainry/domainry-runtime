package workflowmodel

import (
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
)

type WorkflowExecutionReceipt struct {
	ID                 string
	WorkspaceID        string
	WorkflowKey        string
	IdempotencyKey     string
	RequestFingerprint string
	Status             string
	ExecutionID        string
	LeaseOwner         string
	LeaseExpiresAt     string
	FencingToken       int64
	CreatedAt          string
	UpdatedAt          string
	ExpiresAt          string
}

type WorkflowExecutionClaimRequest struct {
	Receipt            WorkflowExecutionReceipt
	RequestFingerprint string
	LeaseOwner         string
	LeaseTTL           time.Duration
	Now                time.Time
}

type WorkflowExecutionClaimResult struct {
	Decision idempotency.Decision
	Receipt  WorkflowExecutionReceipt
}

type WorkflowExecutionReceiptCompletion struct {
	WorkspaceID  string
	ReceiptID    string
	ExecutionID  string
	LeaseOwner   string
	FencingToken int64
	ExpiresAt    time.Time
	Now          time.Time
}
