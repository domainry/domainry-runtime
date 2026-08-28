package action

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/mutation"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
)

const (
	ActionSourceHTTP        = actionmodel.ActionSourceHTTP
	ActionSourceWorkflow    = actionmodel.ActionSourceWorkflow
	ActionSourceAutomation  = actionmodel.ActionSourceAutomation
	ActionSourceIntegration = actionmodel.ActionSourceIntegration
	ActionSourceAgent       = actionmodel.ActionSourceAgent
	ActionSourceNested      = actionmodel.ActionSourceNested
	ActionSourceBulk        = actionmodel.ActionSourceBulk
)

func actionSourceValid(source actionmodel.ActionSource) bool {
	switch source {
	case actionmodel.ActionSourceHTTP,
		actionmodel.ActionSourceWorkflow,
		actionmodel.ActionSourceAutomation,
		actionmodel.ActionSourceScheduler,
		actionmodel.ActionSourceIntegration,
		actionmodel.ActionSourceAgent,
		actionmodel.ActionSourceNested,
		actionmodel.ActionSourceBulk:
		return true
	default:
		return false
	}
}

func NewActionInvocationID(ctx context.Context) string {
	_ = ctx
	return fmt.Sprintf("action_%d", time.Now().UnixNano())
}

func ActionNormalizeInvocation(invocation actionmodel.ActionInvocation) actionmodel.ActionInvocation {
	invocation.ActionKey = strings.TrimSpace(invocation.ActionKey)
	invocation.ObjectKey = strings.TrimSpace(invocation.ObjectKey)
	invocation.RecordID = strings.TrimSpace(invocation.RecordID)
	invocation.ProcessID = strings.TrimSpace(invocation.ProcessID)
	invocation.NodeID = strings.TrimSpace(invocation.NodeID)
	invocation.RequestID = strings.TrimSpace(invocation.RequestID)
	invocation.IdempotencyKey = strings.TrimSpace(invocation.IdempotencyKey)
	invocation.AssuranceToken = strings.TrimSpace(invocation.AssuranceToken)
	if !invocation.Actor.Known {
		invocation.Actor = invocation.Principal
	}
	if !invocation.Principal.Known {
		invocation.Principal = invocation.Actor
	}
	if invocation.RunAs.Known {
		invocation.Principal = invocation.RunAs
	}
	if invocation.Input == nil {
		invocation.Input = map[string]any{}
	}
	if invocation.RequestID == "" {
		invocation.RequestID = invocation.Principal.RequestID
	}
	return invocation
}

func ActionRecordInvocationOutput(result actionmodel.ActionResult) map[string]any {
	return map[string]any{"action_key": result.ActionKey, "object_key": result.ObjectKey, "record_id": result.RecordID, "record": result.Record, "data": result.Output}
}

func ActionObjectInvocationOutput(result actionmodel.ActionObjectResult) map[string]any {
	return map[string]any{"action_key": result.ActionKey, "object_key": result.ObjectKey, "data": result.Output, "created_records": result.CreatedRecords, "updated_records": result.UpdatedRecords}
}

func failInvocation(result actionmodel.ActionInvocationResult, err error) (actionmodel.ActionInvocationResult, error) {
	err = normalizeActionInvocationError(err)
	result.Status = "failed"
	result.ErrorCode = invocationErrorCode(err)
	var appErr *apperror.AppError
	result.Retryable = !errors.As(err, &appErr) || appErr.Kind == apperror.KindInternal || appErr.Kind == apperror.KindUnavailable
	return result, err
}

func normalizeActionInvocationError(err error) error {
	if err == nil {
		return nil
	}
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return apperror.New(apperror.KindUnavailable, "backend.action.cancelled", err, nil)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return apperror.New(apperror.KindUnavailable, "backend.action.timeout", err, nil)
	}
	var businessConflict *mutation.PolicyConflictError
	if errors.As(err, &businessConflict) {
		return apperror.New(apperror.KindConflict, businessConflict.Code, err, nil)
	}
	var conflict *mutation.MutationConflictError
	if errors.As(err, &conflict) {
		if conflict.Kind == mutation.MutationConflictOptimistic {
			return apperror.New(apperror.KindConflict, "backend.record.version_conflict", err, nil)
		}
		return apperror.New(apperror.KindConflict, mutation.StableConflictCode(conflict.Kind), err, nil)
	}
	var transient *mutation.TransactionTransientError
	if errors.As(err, &transient) {
		return apperror.New(apperror.KindUnavailable, mutation.StableTransientCode(transient.Kind), err, nil)
	}
	if mutation.IsTransactionCommitUnknown(err) {
		return apperror.New(apperror.KindUnavailable, mutation.TransactionCommitUnknownCode, err, nil)
	}
	return err
}

func invocationErrorCode(err error) string {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return appErr.ErrorCode()
	}
	return "backend.internal"
}
