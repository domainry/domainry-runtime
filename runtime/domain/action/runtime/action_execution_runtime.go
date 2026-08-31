package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
	"github.com/domainry/domainry-foundation/requestcontext"
	actioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

const actionExecutionLeaseTTL = 30 * time.Second

// ActionExecutionRuntime owns the generic Action receipt lifecycle while the
// Application owner supplies the semantic fingerprint inputs.
type ActionExecutionRuntime struct {
	repository actioncontract.ActionExecutionStore
}

func (s *ActionExecutionRuntime) BeginTransaction(ctx context.Context) (actioncontract.ActionExecutionTransaction, error) {
	if s == nil || s.repository == nil {
		return nil, apperror.New(apperror.KindInternal, idempotency.ErrorCodeReceiptUnavailable, nil, nil)
	}
	store, ok := s.repository.(actioncontract.ActionExecutionTransactionStore)
	if !ok {
		return nil, apperror.New(apperror.KindInternal, "backend.action.transaction_unavailable", nil, nil)
	}
	transaction, err := store.BeginExecutionTransaction(ctx)
	if err != nil {
		return nil, executionServiceError("begin domain action transaction", err)
	}
	return transaction, nil
}

func (s *ActionExecutionRuntime) CommitTransaction(ctx context.Context, transaction actioncontract.ActionExecutionTransaction, claim actionmodel.ActionExecutionClaimResult, result any, commits []transactionmodel.RecordMutationCommit, audits []auditmodel.AuditEvent) error {
	if transaction == nil || strings.TrimSpace(claim.Execution.ID) == "" {
		return apperror.New(apperror.KindInternal, idempotency.ErrorCodeReceiptUnavailable, nil, nil)
	}
	resultMap, err := encodeExecutionResult(result)
	if err != nil {
		return err
	}
	if _, err := transaction.Commit(ctx, commits, actionExecutionCompletion(claim, resultMap, audits)); err != nil {
		return executionServiceError("commit domain action transaction", err)
	}
	return nil
}

func NewActionExecutionRuntime(repository actioncontract.ActionExecutionStore) *ActionExecutionRuntime {
	return &ActionExecutionRuntime{repository: repository}
}

func (s *ActionExecutionRuntime) Available() bool {
	return s != nil && s.repository != nil
}

func (s *ActionExecutionRuntime) TransactionAvailable() bool {
	if s == nil || s.repository == nil {
		return false
	}
	_, ok := s.repository.(actioncontract.ActionExecutionTransactionStore)
	return ok
}

func (s *ActionExecutionRuntime) BeginObject(ctx context.Context, objectKey, actionKey, idempotencyKey string, input idempotency.FingerprintInput, principal principalmodel.Principal) (actionmodel.ActionObjectResult, actionmodel.ActionExecutionClaimResult, bool, error) {
	claim, replay, err := s.begin(ctx, objectKey, "", actionKey, idempotencyKey, input, principal)
	if err != nil || !replay {
		return actionmodel.ActionObjectResult{}, claim, replay, err
	}
	var result actionmodel.ActionObjectResult
	if err := decodeExecutionResult(claim.Execution.Result, &result); err != nil {
		return actionmodel.ActionObjectResult{}, claim, false, err
	}
	result.Message = "backend.action.idempotent_replay"
	return result, claim, true, nil
}

func (s *ActionExecutionRuntime) BeginRecord(ctx context.Context, objectKey, recordID, actionKey, idempotencyKey string, input idempotency.FingerprintInput, principal principalmodel.Principal) (actionmodel.ActionResult, actionmodel.ActionExecutionClaimResult, bool, error) {
	claim, replay, err := s.begin(ctx, objectKey, recordID, actionKey, idempotencyKey, input, principal)
	if err != nil || !replay {
		return actionmodel.ActionResult{}, claim, replay, err
	}
	var result actionmodel.ActionResult
	if err := decodeExecutionResult(claim.Execution.Result, &result); err != nil {
		return actionmodel.ActionResult{}, claim, false, err
	}
	result.Message = "backend.action.idempotent_replay"
	return result, claim, true, nil
}

func (s *ActionExecutionRuntime) BeginBulk(ctx context.Context, objectKey, actionKey, idempotencyKey string, request actionmodel.ActionBulkRequest, principal principalmodel.Principal) (actionmodel.ActionBulkResult, actionmodel.ActionExecutionClaimResult, bool, error) {
	claim, replay, err := s.begin(ctx, objectKey, "", actionKey+"#bulk", idempotencyKey, idempotency.FingerprintInput{
		UseCase: "action.execute_bulk", ResourceType: "record_collection", TargetID: objectKey + "/" + actionKey,
		Payload: map[string]any{"record_ids": request.RecordIDs, "data": request.Data, "expected_versions": request.ExpectedVersions},
	}, principal)
	if err != nil || !replay {
		return actionmodel.ActionBulkResult{}, claim, replay, err
	}
	var result actionmodel.ActionBulkResult
	if err := decodeExecutionResult(claim.Execution.Result, &result); err != nil {
		return actionmodel.ActionBulkResult{}, claim, false, err
	}
	result.Message = "backend.action.idempotent_replay"
	return result, claim, true, nil
}

func (s *ActionExecutionRuntime) Complete(ctx context.Context, claim actionmodel.ActionExecutionClaimResult, result any) error {
	if s == nil || s.repository == nil || strings.TrimSpace(claim.Execution.ID) == "" {
		return nil
	}
	resultMap, err := encodeExecutionResult(result)
	if err != nil {
		return err
	}
	_, err = s.repository.CompleteExecution(ctx, actionmodel.ActionExecutionCompletion{
		Execution: claim.Execution, ExecutionID: claim.Execution.ID, LeaseOwner: claim.Execution.LeaseOwner, FencingToken: claim.Execution.FencingToken,
		Result: resultMap, ResponseStatus: 200, ExpiresAt: time.Now().UTC().Add(30 * 24 * time.Hour), Now: time.Now().UTC(),
	})
	if err != nil {
		return executionServiceError("complete domain action execution", err)
	}
	return nil
}

func (s *ActionExecutionRuntime) Fail(ctx context.Context, claim actionmodel.ActionExecutionClaimResult, result actionmodel.ActionInvocationResult, failure error, audits []auditmodel.AuditEvent) error {
	if s == nil || s.repository == nil || strings.TrimSpace(claim.Execution.ID) == "" {
		return nil
	}
	resultMap, err := encodeExecutionResult(actionExecutionFailure{
		Result: result,
		Kind:   apperror.KindOf(failure),
		Params: apperror.ParamsOf(failure),
	})
	if err != nil {
		return err
	}
	completion := actionmodel.ActionExecutionCompletion{
		Execution: claim.Execution, ExecutionID: claim.Execution.ID, LeaseOwner: claim.Execution.LeaseOwner, FencingToken: claim.Execution.FencingToken,
		Result: resultMap, ErrorCode: result.ErrorCode, Retryable: result.Retryable,
		ResponseStatus: actionFailureResponseStatus(apperror.KindOf(failure)),
		AuditEvents:    append([]auditmodel.AuditEvent(nil), audits...),
		ExpiresAt:      time.Now().UTC().Add(30 * 24 * time.Hour), Now: time.Now().UTC(),
	}
	if store, ok := s.repository.(actioncontract.ActionExecutionTransactionStore); ok && len(completion.AuditEvents) > 0 {
		transaction, beginErr := store.BeginExecutionTransaction(ctx)
		if beginErr != nil {
			return executionServiceError("begin failed domain action transaction", beginErr)
		}
		if _, commitErr := transaction.Commit(ctx, nil, completion); commitErr != nil {
			_ = transaction.RollBack(context.WithoutCancel(ctx))
			return executionServiceError("commit failed domain action transaction", commitErr)
		}
		return nil
	}
	_, err = s.repository.CompleteExecution(ctx, completion)
	if err != nil {
		return executionServiceError("complete failed domain action execution", err)
	}
	return nil
}

func actionFailureResponseStatus(kind apperror.ErrorKind) int {
	switch kind {
	case apperror.KindBadRequest:
		return 400
	case apperror.KindForbidden:
		return 403
	case apperror.KindNotFound:
		return 404
	case apperror.KindConflict:
		return 409
	case apperror.KindRateLimited:
		return 429
	case apperror.KindUnavailable:
		return 503
	default:
		return 500
	}
}

func (s *ActionExecutionRuntime) begin(ctx context.Context, objectKey, recordID, actionKey, idempotencyKey string, input idempotency.FingerprintInput, principal principalmodel.Principal) (actionmodel.ActionExecutionClaimResult, bool, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" || s == nil || s.repository == nil {
		return actionmodel.ActionExecutionClaimResult{Decision: idempotency.DecisionAcquired}, false, nil
	}
	fingerprint, err := idempotency.Fingerprint(input)
	if err != nil {
		return actionmodel.ActionExecutionClaimResult{}, false, executionServiceError("fingerprint domain action request", err)
	}
	owner := strings.TrimSpace(principal.RequestID)
	if owner == "" {
		owner = requestcontext.RequestID(ctx)
	}
	if owner == "" {
		owner = requestcontext.NewRequestID()
	}
	claim, err := s.repository.TryBeginExecution(ctx, actionmodel.ActionExecutionClaimRequest{
		Execution: actionmodel.ActionBusinessExecution{
			WorkspaceID: actionWorkspaceID(principal), ObjectKey: strings.TrimSpace(objectKey), RecordID: strings.TrimSpace(recordID), ActionKey: strings.TrimSpace(actionKey),
			IdempotencyKey: idempotencyKey, ActorID: principal.UserID, RoleKey: principal.RoleKey,
		},
		RequestFingerprint: fingerprint, LeaseOwner: owner, LeaseTTL: actionExecutionLeaseTTL, Now: time.Now().UTC(),
	})
	if err != nil {
		return actionmodel.ActionExecutionClaimResult{}, false, executionServiceError("claim domain action execution", err)
	}
	switch claim.Decision {
	case idempotency.DecisionAcquired:
		return claim, false, nil
	case idempotency.DecisionReplay:
		if idempotency.Status(claim.Execution.Status) == idempotency.StatusFailedTerminal {
			return claim, false, replayActionExecutionFailure(claim.Execution)
		}
		return claim, true, nil
	case idempotency.DecisionFingerprintConflict:
		return claim, false, apperror.New(apperror.KindConflict, idempotency.ErrorCodeKeyReused, nil, map[string]string{"action": actionKey})
	case idempotency.DecisionInProgress:
		return claim, false, apperror.New(apperror.KindConflict, idempotency.ErrorCodeInProgress, nil, map[string]string{"retry_after": actionRetryAfter(claim.Execution.LeaseExpiresAt)})
	default:
		return claim, false, apperror.New(apperror.KindInternal, idempotency.ErrorCodeReceiptUnavailable, nil, map[string]string{"action": actionKey})
	}
}

type actionExecutionFailure struct {
	Result actionmodel.ActionInvocationResult `json:"result"`
	Kind   apperror.ErrorKind                 `json:"kind"`
	Params map[string]string                  `json:"params,omitempty"`
}

func replayActionExecutionFailure(execution actionmodel.ActionBusinessExecution) error {
	var failure actionExecutionFailure
	if err := decodeExecutionResult(execution.Result, &failure); err != nil {
		return apperror.New(apperror.KindInternal, idempotency.ErrorCodeReceiptUnavailable, err, nil)
	}
	code := strings.TrimSpace(execution.ErrorCode)
	if code == "" || !actionFailureKindValid(failure.Kind) ||
		failure.Result.Status != "failed" ||
		strings.TrimSpace(failure.Result.ErrorCode) != code ||
		failure.Result.Retryable {
		return apperror.New(apperror.KindInternal, idempotency.ErrorCodeReceiptUnavailable, nil, nil)
	}
	return apperror.New(failure.Kind, code, nil, failure.Params)
}

func actionFailureKindValid(kind apperror.ErrorKind) bool {
	switch kind {
	case apperror.KindBadRequest,
		apperror.KindForbidden,
		apperror.KindNotFound,
		apperror.KindConflict,
		apperror.KindRateLimited,
		apperror.KindUnavailable,
		apperror.KindInternal:
		return true
	default:
		return false
	}
}

func actionRetryAfter(expiresAt string) string {
	expiry, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(expiresAt))
	if err != nil {
		return "1"
	}
	seconds := int(math.Ceil(time.Until(expiry).Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}

func actionExecutionCompletion(claim actionmodel.ActionExecutionClaimResult, result map[string]any, audits []auditmodel.AuditEvent) actionmodel.ActionExecutionCompletion {
	now := time.Now().UTC()
	return actionmodel.ActionExecutionCompletion{
		Execution: claim.Execution, ExecutionID: claim.Execution.ID, LeaseOwner: claim.Execution.LeaseOwner, FencingToken: claim.Execution.FencingToken,
		Result: result, ResponseStatus: 200, AuditEvents: append([]auditmodel.AuditEvent(nil), audits...), ExpiresAt: now.Add(30 * 24 * time.Hour), Now: now,
	}
}

func encodeExecutionResult(result any) (map[string]any, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, executionServiceError("encode domain action result", err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, executionServiceError("decode domain action result", err)
	}
	return out, nil
}

func decodeExecutionResult(values map[string]any, target any) error {
	raw, err := json.Marshal(values)
	if err != nil {
		return executionServiceError("encode cached domain action result", err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return executionServiceError("decode cached domain action result", err)
	}
	return nil
}

func actionWorkspaceID(principal principalmodel.Principal) string {
	return strings.TrimSpace(principal.WorkspaceID)
}

func executionServiceError(operation string, err error) error {
	if errors.Is(err, context.Canceled) {
		return apperror.New(apperror.KindUnavailable, "backend.action.cancelled", err, nil)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return apperror.New(apperror.KindUnavailable, "backend.action.timeout", err, nil)
	}
	var businessConflict *mutation.PolicyConflictError
	if errors.As(err, &businessConflict) {
		return apperror.New(apperror.KindConflict, businessConflict.Code, err, map[string]string{"resource": businessConflict.Resource, "identifier": businessConflict.Identifier, "field": businessConflict.Field})
	}
	var conflict *mutation.MutationConflictError
	if errors.As(err, &conflict) {
		// Optimistic record mutations are part of the public record contract,
		// regardless of whether they were invoked through generic CRUD or a
		// source-owned Action. Keep the persistence-layer mutation codes for
		// other conflict classes, but never leak the internal optimistic code at
		// the Action boundary.
		if conflict.Kind == mutation.MutationConflictOptimistic {
			return apperror.New(apperror.KindConflict, "backend.record.version_conflict", err, map[string]string{"resource": conflict.Resource, "identifier": conflict.Identifier})
		}
		return apperror.New(apperror.KindConflict, mutation.StableConflictCode(conflict.Kind), err, map[string]string{"resource": conflict.Resource, "identifier": conflict.Identifier})
	}
	var transient *mutation.TransactionTransientError
	if errors.As(err, &transient) {
		return apperror.New(apperror.KindUnavailable, mutation.StableTransientCode(transient.Kind), err, map[string]string{"resource": transient.Resource, "identifier": transient.Identifier})
	}
	if mutation.IsTransactionCommitUnknown(err) {
		return apperror.New(apperror.KindUnavailable, mutation.TransactionCommitUnknownCode, err, nil)
	}
	return &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.internal", Params: map[string]string{"operation": operation}, Err: err}
}
