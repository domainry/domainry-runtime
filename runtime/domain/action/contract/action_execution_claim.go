package contract

import (
	"context"
	"time"

	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
)

// ActionExecutionClaimStore owns the atomic Action receipt lifecycle. Every
// completion and heartbeat is fenced by owner and token.
type ActionExecutionClaimStore interface {
	TryBeginExecution(context.Context, actionmodel.ActionExecutionClaimRequest) (actionmodel.ActionExecutionClaimResult, error)
	HeartbeatExecution(ctx context.Context, executionID, expectedLeaseOwner string, expectedFencingToken int64, leaseExpiresAt, now time.Time) error
	CompleteExecution(context.Context, actionmodel.ActionExecutionCompletion) (actionmodel.ActionBusinessExecution, error)
}
