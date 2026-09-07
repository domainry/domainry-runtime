package contract

import (
	"context"

	dispatchmodel "github.com/domainry/domainry-runtime/runtime/domain/dispatch/model"
)

// CallbackReceiptStore owns the receiver-side replay claim. Its scope is the
// authenticated Runtime callback identity, not a Scheduler run or definition.
type CallbackReceiptStore interface {
	TryBeginCallback(context.Context, dispatchmodel.CallbackClaimRequest) (dispatchmodel.CallbackClaimResult, error)
	HeartbeatCallback(context.Context, dispatchmodel.CallbackHeartbeat) (bool, error)
	CompleteCallback(context.Context, dispatchmodel.CallbackCompletion) error
	FailCallbackRetryable(context.Context, dispatchmodel.CallbackFailure) error
}
