package runtime

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/requestcontext"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

const recordMutationLeaseTTL = 30 * time.Second

type RecordMutationExecutionRuntime struct {
	repository recordcontract.RecordMutationExecutionStore
}

type recordMutationExecutionLookup interface {
	FindRecordMutationExecution(context.Context, recordmodel.RecordMutationExecution) (recordmodel.RecordMutationExecution, bool, error)
}

func NewRecordMutationExecutionRuntime(repository recordcontract.RecordMutationExecutionStore) *RecordMutationExecutionRuntime {
	return &RecordMutationExecutionRuntime{repository: repository}
}

func (s *RecordMutationExecutionRuntime) BeginCreate(ctx context.Context, objectKey, key string, payload map[string]any, principal principalmodel.Principal) (recordmodel.Record, recordmodel.RecordMutationClaimResult, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return recordmodel.Record{}, recordmodel.RecordMutationClaimResult{}, false, apperror.New(apperror.KindBadRequest, idempotency.ErrorCodeMissingKey, nil, map[string]string{"use_case": "record.create"})
	}
	if s == nil || s.repository == nil {
		return recordmodel.Record{}, recordmodel.RecordMutationClaimResult{}, false, apperror.New(apperror.KindInternal, idempotency.ErrorCodeReceiptUnavailable, nil, nil)
	}
	fingerprint, err := idempotency.Fingerprint(idempotency.FingerprintInput{UseCase: "record.create", ResourceType: "record_collection", TargetID: objectKey, Payload: payload})
	if err != nil {
		return recordmodel.Record{}, recordmodel.RecordMutationClaimResult{}, false, recordMutationRuntimeError("fingerprint record create", err)
	}
	owner := strings.TrimSpace(principal.RequestID)
	if owner == "" {
		owner = requestcontext.RequestID(ctx)
	}
	if owner == "" {
		owner = requestcontext.NewRequestID()
	}
	workspaceID := strings.TrimSpace(principal.WorkspaceID)
	if _, err := principalmodel.NewWorkspaceID(workspaceID); err != nil {
		return recordmodel.Record{}, recordmodel.RecordMutationClaimResult{}, false, apperror.New(apperror.KindForbidden, "backend.workspace_scope_required", err, nil)
	}
	claim, err := s.repository.TryBeginRecordMutation(ctx, recordmodel.RecordMutationClaimRequest{
		Execution:          recordmodel.RecordMutationExecution{WorkspaceID: workspaceID, Operation: "create", ObjectKey: strings.TrimSpace(objectKey), IdempotencyKey: key, ActorID: principal.UserID},
		RequestFingerprint: fingerprint, LeaseOwner: owner, LeaseTTL: recordMutationLeaseTTL, Now: time.Now().UTC(),
	})
	if err != nil {
		return recordmodel.Record{}, recordmodel.RecordMutationClaimResult{}, false, recordMutationRuntimeError("claim record create", err)
	}
	switch claim.Decision {
	case idempotency.DecisionAcquired:
		return recordmodel.Record{}, claim, false, nil
	case idempotency.DecisionReplay:
		return claim.Execution.Result, claim, true, nil
	case idempotency.DecisionFingerprintConflict:
		return recordmodel.Record{}, claim, false, apperror.New(apperror.KindConflict, idempotency.ErrorCodeKeyReused, nil, map[string]string{"object_key": objectKey})
	case idempotency.DecisionInProgress:
		return recordmodel.Record{}, claim, false, apperror.New(apperror.KindConflict, idempotency.ErrorCodeInProgress, nil, map[string]string{"retry_after": recordMutationRetryAfter(claim.Execution.LeaseExpiresAt)})
	default:
		return recordmodel.Record{}, claim, false, apperror.New(apperror.KindInternal, idempotency.ErrorCodeReceiptUnavailable, nil, nil)
	}
}

func (s *RecordMutationExecutionRuntime) BeginUpdate(ctx context.Context, objectKey, recordID, key string, payload map[string]any, principal principalmodel.Principal) (recordmodel.Record, recordmodel.RecordMutationClaimResult, bool, error) {
	claim, replay, err := s.beginOperationTarget(ctx, "update", objectKey, recordID, key, idempotency.FingerprintInput{
		UseCase: "record.update", ResourceType: "record", TargetID: strings.TrimSpace(objectKey) + "/" + strings.TrimSpace(recordID), Payload: payload,
	}, principal)
	if err != nil || !replay {
		return recordmodel.Record{}, claim, replay, err
	}
	return claim.Execution.Result, claim, true, nil
}

func (s *RecordMutationExecutionRuntime) Commit(ctx context.Context, claim recordmodel.RecordMutationClaimResult, commit transactionmodel.RecordMutationCommit) error {
	if s == nil || s.repository == nil {
		return apperror.New(apperror.KindInternal, idempotency.ErrorCodeReceiptUnavailable, nil, nil)
	}
	_, err := s.repository.CommitRecordMutationExecution(ctx, commit, recordmodel.RecordMutationCompletion{
		WorkspaceID: claim.Execution.WorkspaceID,
		ExecutionID: claim.Execution.ID, LeaseOwner: claim.Execution.LeaseOwner, FencingToken: claim.Execution.FencingToken,
		Result: commit.Record, ExpiresAt: time.Now().UTC().Add(30 * 24 * time.Hour), Now: time.Now().UTC(),
	})
	if err != nil {
		return recordMutationRuntimeError("commit record mutation execution", err)
	}
	return nil
}

func (s *RecordMutationExecutionRuntime) BeginImport(ctx context.Context, objectKey, key string, rawCSV []byte, principal principalmodel.Principal) (recordmodel.RecordImportApplyResult, recordmodel.RecordMutationClaimResult, bool, error) {
	claim, replay, err := s.beginOperation(ctx, "import", objectKey, key, idempotency.FingerprintInput{UseCase: "record.import", ResourceType: "record_collection", TargetID: objectKey, Payload: rawCSV}, principal)
	if err != nil || !replay {
		return recordmodel.RecordImportApplyResult{}, claim, replay, err
	}
	var result recordmodel.RecordImportApplyResult
	raw, err := json.Marshal(claim.Execution.OperationResult)
	if err != nil {
		return recordmodel.RecordImportApplyResult{}, claim, false, recordMutationRuntimeError("decode record import replay", err)
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return recordmodel.RecordImportApplyResult{}, claim, false, recordMutationRuntimeError("decode record import replay", err)
	}
	return result, claim, true, nil
}

func (s *RecordMutationExecutionRuntime) ReplayImport(ctx context.Context, objectKey, key string, rawCSV []byte, principal principalmodel.Principal) (recordmodel.RecordImportApplyResult, bool, error) {
	if s == nil || s.repository == nil {
		return recordmodel.RecordImportApplyResult{}, false, nil
	}
	lookup, ok := s.repository.(recordMutationExecutionLookup)
	if !ok {
		return recordmodel.RecordImportApplyResult{}, false, nil
	}
	workspaceID := strings.TrimSpace(principal.WorkspaceID)
	if _, err := principalmodel.NewWorkspaceID(workspaceID); err != nil {
		return recordmodel.RecordImportApplyResult{}, false, apperror.New(apperror.KindForbidden, "backend.workspace_scope_required", err, nil)
	}
	execution, found, err := lookup.FindRecordMutationExecution(ctx, recordmodel.RecordMutationExecution{
		WorkspaceID: workspaceID, Operation: "import", ObjectKey: strings.TrimSpace(objectKey), IdempotencyKey: strings.TrimSpace(key),
	})
	if err != nil {
		return recordmodel.RecordImportApplyResult{}, false, recordMutationRuntimeError("lookup record import replay", err)
	}
	if !found || execution.Status != string(idempotency.StatusSucceeded) {
		return recordmodel.RecordImportApplyResult{}, false, nil
	}
	fingerprint, _ := idempotency.Fingerprint(idempotency.FingerprintInput{UseCase: "record.import", ResourceType: "record_collection", TargetID: objectKey, Payload: rawCSV})
	if execution.RequestFingerprint != fingerprint {
		return recordmodel.RecordImportApplyResult{}, false, apperror.New(apperror.KindConflict, idempotency.ErrorCodeKeyReused, nil, map[string]string{"object_key": objectKey})
	}
	var result recordmodel.RecordImportApplyResult
	raw, err := json.Marshal(execution.OperationResult)
	if err != nil {
		return recordmodel.RecordImportApplyResult{}, false, recordMutationRuntimeError("decode record import replay", err)
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return recordmodel.RecordImportApplyResult{}, false, recordMutationRuntimeError("decode record import replay", err)
	}
	return result, true, nil
}

func (s *RecordMutationExecutionRuntime) CompleteOperation(ctx context.Context, claim recordmodel.RecordMutationClaimResult, result any) error {
	if s == nil || s.repository == nil {
		return apperror.New(apperror.KindInternal, idempotency.ErrorCodeReceiptUnavailable, nil, nil)
	}
	_, err := s.repository.CompleteRecordMutationExecution(ctx, recordmodel.RecordMutationCompletion{WorkspaceID: claim.Execution.WorkspaceID, ExecutionID: claim.Execution.ID, LeaseOwner: claim.Execution.LeaseOwner, FencingToken: claim.Execution.FencingToken, Result: result, ExpiresAt: time.Now().UTC().Add(30 * 24 * time.Hour), Now: time.Now().UTC()})
	if err != nil {
		return recordMutationRuntimeError("complete record operation", err)
	}
	return nil
}

func (s *RecordMutationExecutionRuntime) beginOperation(ctx context.Context, operation, objectKey, key string, input idempotency.FingerprintInput, principal principalmodel.Principal) (recordmodel.RecordMutationClaimResult, bool, error) {
	return s.beginOperationTarget(ctx, operation, objectKey, "", key, input, principal)
}

func (s *RecordMutationExecutionRuntime) beginOperationTarget(ctx context.Context, operation, objectKey, targetID, key string, input idempotency.FingerprintInput, principal principalmodel.Principal) (recordmodel.RecordMutationClaimResult, bool, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return recordmodel.RecordMutationClaimResult{}, false, apperror.New(apperror.KindBadRequest, idempotency.ErrorCodeMissingKey, nil, map[string]string{"use_case": "record." + operation})
	}
	if s == nil || s.repository == nil {
		return recordmodel.RecordMutationClaimResult{}, false, apperror.New(apperror.KindInternal, idempotency.ErrorCodeReceiptUnavailable, nil, nil)
	}
	fingerprint, err := idempotency.Fingerprint(input)
	if err != nil {
		return recordmodel.RecordMutationClaimResult{}, false, recordMutationRuntimeError("fingerprint record operation", err)
	}
	owner := strings.TrimSpace(principal.RequestID)
	if owner == "" {
		owner = requestcontext.RequestID(ctx)
	}
	if owner == "" {
		owner = requestcontext.NewRequestID()
	}
	workspaceID := strings.TrimSpace(principal.WorkspaceID)
	if _, err := principalmodel.NewWorkspaceID(workspaceID); err != nil {
		return recordmodel.RecordMutationClaimResult{}, false, apperror.New(apperror.KindForbidden, "backend.workspace_scope_required", err, nil)
	}
	claim, err := s.repository.TryBeginRecordMutation(ctx, recordmodel.RecordMutationClaimRequest{Execution: recordmodel.RecordMutationExecution{WorkspaceID: workspaceID, Operation: operation, ObjectKey: strings.TrimSpace(objectKey), TargetID: strings.TrimSpace(targetID), IdempotencyKey: key, ActorID: principal.UserID}, RequestFingerprint: fingerprint, LeaseOwner: owner, LeaseTTL: recordMutationLeaseTTL, Now: time.Now().UTC()})
	if err != nil {
		return recordmodel.RecordMutationClaimResult{}, false, recordMutationRuntimeError("claim record operation", err)
	}
	switch claim.Decision {
	case idempotency.DecisionAcquired:
		return claim, false, nil
	case idempotency.DecisionReplay:
		return claim, true, nil
	case idempotency.DecisionFingerprintConflict:
		return claim, false, apperror.New(apperror.KindConflict, idempotency.ErrorCodeKeyReused, nil, map[string]string{"object_key": objectKey})
	case idempotency.DecisionInProgress:
		return claim, false, apperror.New(apperror.KindConflict, idempotency.ErrorCodeInProgress, nil, map[string]string{"retry_after": recordMutationRetryAfter(claim.Execution.LeaseExpiresAt)})
	default:
		return claim, false, apperror.New(apperror.KindInternal, idempotency.ErrorCodeReceiptUnavailable, nil, nil)
	}
}

func recordMutationRetryAfter(value string) string {
	expiresAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return "1"
	}
	seconds := int(math.Ceil(time.Until(expiresAt).Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}

func recordMutationRuntimeError(operation string, err error) error {
	return &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.internal", Params: map[string]string{"operation": operation}, Err: err}
}
