package transactionmodel

import (
	"context"
	"strings"
)

type BoundaryIntentStatus string

const (
	BoundaryIntentPending                BoundaryIntentStatus = "pending"
	BoundaryIntentExecuting              BoundaryIntentStatus = "executing"
	BoundaryIntentReconciliationRequired BoundaryIntentStatus = "reconciliation_required"
	BoundaryIntentCompensating           BoundaryIntentStatus = "compensating"
	BoundaryIntentSucceeded              BoundaryIntentStatus = "succeeded"
	BoundaryIntentCompensated            BoundaryIntentStatus = "compensated"
	BoundaryIntentManualReview           BoundaryIntentStatus = "manual_review"
)

type BoundaryIntent struct {
	ID                  string
	WorkspaceID         string
	Owner               string
	Operation           string
	ResourceID          string
	IdempotencyKey      string
	Status              BoundaryIntentStatus
	Payload             map[string]any
	CompensationPayload map[string]any
	AttemptCount        int
	NextAttemptAt       string
	LeaseOwner          string
	LeaseExpiresAt      string
	FencingToken        int64
	LastError           string
	CreatedAt           string
	UpdatedAt           string
}

type BoundaryIntentRepository interface {
	CreateBoundaryIntent(context.Context, BoundaryIntent) (BoundaryIntent, bool, error)
	ClaimBoundaryIntent(context.Context, string, string, string) (BoundaryIntent, bool, error)
	TransitionBoundaryIntent(context.Context, string, string, int64, BoundaryIntentStatus, string, string) (BoundaryIntent, error)
	GetBoundaryIntent(context.Context, string) (BoundaryIntent, bool, error)
}

func BoundaryIntentTransitionAllowed(from, to BoundaryIntentStatus) bool {
	allowed := map[BoundaryIntentStatus]map[BoundaryIntentStatus]bool{
		BoundaryIntentPending:                {BoundaryIntentExecuting: true},
		BoundaryIntentExecuting:              {BoundaryIntentSucceeded: true, BoundaryIntentReconciliationRequired: true},
		BoundaryIntentReconciliationRequired: {BoundaryIntentExecuting: true, BoundaryIntentCompensating: true, BoundaryIntentManualReview: true},
		BoundaryIntentCompensating:           {BoundaryIntentCompensated: true, BoundaryIntentManualReview: true},
	}
	return strings.TrimSpace(string(from)) != "" && allowed[from][to]
}
