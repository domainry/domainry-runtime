package dispatch

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	dispatchcontract "github.com/domainry/domainry-runtime/runtime/domain/dispatch/contract"
	dispatchmodel "github.com/domainry/domainry-runtime/runtime/domain/dispatch/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

const (
	callbackLeaseTTL   = 30 * time.Second
	callbackReceiptTTL = 90 * 24 * time.Hour
)

type CallbackIdentity struct {
	Method         string
	Path           string
	RuntimeID      string
	IdempotencyKey string
	BodySHA256     string
}

type CallbackExecutionRequest struct {
	Identity  CallbackIdentity
	Execution ExecutionRequest
}

type CallbackExecutionResult struct {
	ExecutionID string
	Receipt     ExecutionReceipt
	Replay      bool
}

type CallbackTargetExecutor interface {
	Execute(context.Context, ExecutionRequest) (ExecutionReceipt, error)
}

type CallbackExecutionDependencies struct {
	Executor   CallbackTargetExecutor
	Receipts   dispatchcontract.CallbackReceiptStore
	LeaseOwner string
	Now        func() time.Time
	LeaseTTL   time.Duration
}

// CallbackExecutionApplicationService owns only receiver replay coordination;
// it delegates the actual target execution to the existing owner router.
type CallbackExecutionApplicationService struct {
	executor   CallbackTargetExecutor
	receipts   dispatchcontract.CallbackReceiptStore
	leaseOwner string
	now        func() time.Time
	leaseTTL   time.Duration
}

func NewCallbackExecutionApplicationService(deps CallbackExecutionDependencies) *CallbackExecutionApplicationService {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	leaseTTL := deps.LeaseTTL
	if leaseTTL <= 0 {
		leaseTTL = callbackLeaseTTL
	}
	return &CallbackExecutionApplicationService{
		executor: deps.Executor, receipts: deps.Receipts, leaseOwner: strings.TrimSpace(deps.LeaseOwner), now: now, leaseTTL: leaseTTL,
	}
}

func (s *CallbackExecutionApplicationService) Execute(ctx context.Context, request CallbackExecutionRequest) (CallbackExecutionResult, error) {
	if s == nil || s.executor == nil || s.receipts == nil || s.leaseOwner == "" {
		return CallbackExecutionResult{}, callbackExecutionError(apperror.KindUnavailable, "backend.dispatch.callback_receipt_unavailable", nil)
	}
	now := s.now().UTC()
	claim, err := s.receipts.TryBeginCallback(ctx, dispatchmodel.CallbackClaimRequest{
		Receipt: dispatchmodel.CallbackReceipt{
			WorkspaceID: principalmodel.InstallationWorkspaceID, RuntimeID: request.Identity.RuntimeID,
			Method: request.Identity.Method, Path: request.Identity.Path, IdempotencyKey: request.Identity.IdempotencyKey,
			BodySHA256: request.Identity.BodySHA256, ExecutionID: strings.TrimSpace(request.Execution.ExecutionID),
		},
		LeaseOwner: s.leaseOwner, LeaseTTL: s.leaseTTL, Now: now,
	})
	if err != nil {
		return CallbackExecutionResult{}, callbackExecutionError(apperror.KindUnavailable, "backend.dispatch.callback_claim_failed", err)
	}
	switch claim.Decision {
	case idempotency.DecisionReplay:
		if strings.TrimSpace(claim.Receipt.DownstreamID) == "" || strings.TrimSpace(claim.Receipt.DownstreamStatus) == "" {
			return CallbackExecutionResult{}, callbackExecutionError(apperror.KindUnavailable, idempotency.ErrorCodeReceiptUnavailable, nil)
		}
		return CallbackExecutionResult{
			ExecutionID: claim.Receipt.ExecutionID, Replay: true,
			Receipt: ExecutionReceipt{ID: claim.Receipt.DownstreamID, Owner: claim.Receipt.DownstreamOwner, Status: claim.Receipt.DownstreamStatus},
		}, nil
	case idempotency.DecisionFingerprintConflict:
		return CallbackExecutionResult{}, callbackExecutionError(apperror.KindConflict, idempotency.ErrorCodeKeyReused, nil)
	case idempotency.DecisionInProgress:
		return CallbackExecutionResult{}, callbackExecutionError(apperror.KindUnavailable, idempotency.ErrorCodeInProgress, nil)
	case idempotency.DecisionAcquired:
	default:
		return CallbackExecutionResult{}, callbackExecutionError(apperror.KindUnavailable, idempotency.ErrorCodeReceiptUnavailable, nil)
	}

	workCtx, stopHeartbeat := workerplatform.WithHeartbeat(ctx, s.leaseTTL/3, func(heartbeatCtx context.Context) error {
		alive, heartbeatErr := s.receipts.HeartbeatCallback(heartbeatCtx, dispatchmodel.CallbackHeartbeat{
			WorkspaceID: claim.Receipt.WorkspaceID, ReceiptID: claim.Receipt.ID, LeaseOwner: claim.Receipt.LeaseOwner,
			FencingToken: claim.Receipt.FencingToken, LeaseTTL: s.leaseTTL, Now: s.now().UTC(),
		})
		if heartbeatErr != nil {
			return heartbeatErr
		}
		if !alive {
			return fmt.Errorf("callback receipt lease lost")
		}
		return nil
	})
	receipt, executeErr := s.executor.Execute(workCtx, request.Execution)
	heartbeatErr := stopHeartbeat()
	if executeErr == nil && (strings.TrimSpace(receipt.ID) == "" || strings.TrimSpace(receipt.Status) == "") {
		executeErr = fmt.Errorf("callback downstream receipt is incomplete")
	}
	if executeErr != nil || heartbeatErr != nil {
		failureAt := s.now().UTC()
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		failureErr := s.receipts.FailCallbackRetryable(cleanupCtx, dispatchmodel.CallbackFailure{
			WorkspaceID: claim.Receipt.WorkspaceID, ReceiptID: claim.Receipt.ID, LeaseOwner: claim.Receipt.LeaseOwner,
			FencingToken: claim.Receipt.FencingToken, Now: failureAt, ExpiresAt: failureAt.Add(callbackReceiptTTL),
		})
		if heartbeatErr != nil {
			return CallbackExecutionResult{}, callbackExecutionError(apperror.KindUnavailable, idempotency.ErrorCodeLeaseLost, heartbeatErr)
		}
		if failureErr != nil {
			return CallbackExecutionResult{}, callbackExecutionError(apperror.KindUnavailable, idempotency.ErrorCodeLeaseLost, failureErr)
		}
		return CallbackExecutionResult{}, executeErr
	}
	completedAt := s.now().UTC()
	completionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.receipts.CompleteCallback(completionCtx, dispatchmodel.CallbackCompletion{
		WorkspaceID: claim.Receipt.WorkspaceID, ReceiptID: claim.Receipt.ID,
		DownstreamID: receipt.ID, DownstreamOwner: receipt.Owner, DownstreamStatus: receipt.Status,
		LeaseOwner: claim.Receipt.LeaseOwner, FencingToken: claim.Receipt.FencingToken,
		Now: completedAt, ExpiresAt: completedAt.Add(callbackReceiptTTL),
	}); err != nil {
		return CallbackExecutionResult{}, callbackExecutionError(apperror.KindUnavailable, idempotency.ErrorCodeLeaseLost, err)
	}
	return CallbackExecutionResult{ExecutionID: claim.Receipt.ExecutionID, Receipt: receipt}, nil
}

func callbackExecutionError(kind apperror.ErrorKind, code string, cause error) error {
	return &apperror.AppError{Kind: kind, Code: code, Err: cause}
}
