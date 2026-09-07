package model

import (
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
)

const CallbackExecutionUseCase = "dispatch.callback.execute"

// CallbackReceipt is Runtime-owned protocol coordination state. Downstream
// business state remains owned by the target module or Integration delivery.
type CallbackReceipt struct {
	ID               string
	WorkspaceID      string
	RuntimeID        string
	Method           string
	Path             string
	IdempotencyKey   string
	BodySHA256       string
	Status           string
	ExecutionID      string
	DownstreamID     string
	DownstreamOwner  string
	DownstreamStatus string
	LeaseOwner       string
	LeaseExpiresAt   string
	FencingToken     int64
	CreatedAt        string
	UpdatedAt        string
	ExpiresAt        string
}

type CallbackClaimRequest struct {
	Receipt    CallbackReceipt
	LeaseOwner string
	LeaseTTL   time.Duration
	Now        time.Time
}

type CallbackClaimResult struct {
	Decision idempotency.Decision
	Receipt  CallbackReceipt
}

type CallbackHeartbeat struct {
	WorkspaceID  string
	ReceiptID    string
	LeaseOwner   string
	FencingToken int64
	LeaseTTL     time.Duration
	Now          time.Time
}

type CallbackCompletion struct {
	WorkspaceID      string
	ReceiptID        string
	DownstreamID     string
	DownstreamOwner  string
	DownstreamStatus string
	LeaseOwner       string
	FencingToken     int64
	Now              time.Time
	ExpiresAt        time.Time
}

type CallbackFailure struct {
	WorkspaceID  string
	ReceiptID    string
	LeaseOwner   string
	FencingToken int64
	Now          time.Time
	ExpiresAt    time.Time
}
