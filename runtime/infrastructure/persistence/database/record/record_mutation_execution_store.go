package record

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
	sharedoperation "github.com/domainry/domainry-foundation/operation"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

const (
	recordMutationOwner = "record"
	recordMutationKind  = "record.mutation"
)

func (r RecordStore) TryBeginRecordMutation(ctx context.Context, request recordmodel.RecordMutationClaimRequest) (recordmodel.RecordMutationClaimResult, error) {
	for attempt := 0; attempt < 50; attempt++ {
		claim, err := r.tryBeginRecordMutationOnce(ctx, request)
		if err == nil || !r.store.IsTransientError(err) {
			return claim, err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return recordmodel.RecordMutationClaimResult{}, ctx.Err()
		case <-timer.C:
		}
	}
	return recordmodel.RecordMutationClaimResult{}, fmt.Errorf("claim record mutation: sqlite remained busy after retry")
}

func (r RecordStore) tryBeginRecordMutationOnce(ctx context.Context, request recordmodel.RecordMutationClaimRequest) (recordmodel.RecordMutationClaimResult, error) {
	now := request.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if request.LeaseTTL <= 0 {
		request.LeaseTTL = 30 * time.Second
	}
	value := request.Execution
	workspaceID, err := requireRecordWorkspaceID(value.WorkspaceID)
	if err != nil {
		return recordmodel.RecordMutationClaimResult{}, err
	}
	value.WorkspaceID = workspaceID
	value.Operation, value.ObjectKey, value.TargetID, value.IdempotencyKey = strings.TrimSpace(value.Operation), strings.TrimSpace(value.ObjectKey), strings.TrimSpace(value.TargetID), strings.TrimSpace(value.IdempotencyKey)
	value.ID = recordMutationExecutionID(value)
	value.RequestFingerprint, value.Status = strings.TrimSpace(request.RequestFingerprint), string(idempotency.StatusProcessing)
	value.LeaseOwner, value.LeaseExpiresAt, value.FencingToken = strings.TrimSpace(request.LeaseOwner), now.Add(request.LeaseTTL).Format(time.RFC3339Nano), 1
	value.CreatedAt, value.UpdatedAt = now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)
	operationStore := sharedoperation.NewSQLStore(r.database(), r.store.SQLRenderer)
	tx, beginErr := r.database().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if beginErr != nil {
		return recordmodel.RecordMutationClaimResult{}, beginErr
	}
	if guardErr := r.store.GuardSubjectEvidenceWrite(ctx, tx, workspaceID, sharedoperation.TableName,
		[]string{"id", "owner", "resource_type", "resource_id", "requested_by"},
		[]any{value.ID, recordMutationOwner, value.ObjectKey, value.TargetID, value.ActorID}); guardErr != nil {
		_ = tx.Rollback()
		return recordmodel.RecordMutationClaimResult{}, guardErr
	}
	inserted, insertErr := operationStore.InsertRecord(sharedoperation.WithExecutor(ctx, tx), recordMutationRecord(value, json.RawMessage(`{}`)))
	if insertErr == nil && inserted {
		if commitErr := tx.Commit(); commitErr != nil {
			return recordmodel.RecordMutationClaimResult{}, commitErr
		}
		r.store.ObserveIdempotency(ctx, value.WorkspaceID, "record."+value.Operation, idempotency.OutcomeAcquired)
		return recordmodel.RecordMutationClaimResult{Decision: idempotency.DecisionAcquired, Execution: value}, nil
	}
	_ = tx.Rollback()
	current, found, err := r.findRecordMutationExecution(ctx, value)
	if err != nil {
		return recordmodel.RecordMutationClaimResult{}, err
	}
	if !found {
		return recordmodel.RecordMutationClaimResult{}, database.MutationConstraintError(insertErr, "record_mutation_execution", value.ID, mutation.MutationConflictIdempotency)
	}
	decision := idempotency.Classify(idempotency.ReceiptState{Status: idempotency.Status(current.Status), Fingerprint: current.RequestFingerprint, Lease: recordMutationLease(current)}, value.RequestFingerprint, now)
	if decision != idempotency.DecisionAcquired {
		r.store.ObserveIdempotency(ctx, value.WorkspaceID, "record."+value.Operation, idempotency.OutcomeForDecision(decision, false))
		return recordmodel.RecordMutationClaimResult{Decision: decision, Execution: current}, nil
	}
	status, empty := string(idempotency.StatusProcessing), ""
	resultJSON, metadataJSON := json.RawMessage(`{}`), json.RawMessage(recordMutationMetadataJSON(0))
	changed, err := operationStore.PatchRecord(ctx, sharedoperation.RecordFilter{
		WorkspaceID: workspaceID, ID: value.ID, Owner: recordMutationOwner, Kind: recordMutationKind,
		RequestFingerprint: value.RequestFingerprint, LeaseExpiresAtOrBefore: now.Format(time.RFC3339Nano),
		ReclaimableStatus: string(idempotency.StatusFailedRetryable), ExpiredLeaseStatus: string(idempotency.StatusProcessing),
	}, sharedoperation.RecordChanges{
		Status: &status, LeaseOwner: &value.LeaseOwner, LeaseExpiresAt: &value.LeaseExpiresAt, IncrementFencingToken: true,
		MetadataJSON: &metadataJSON, ErrorCode: &empty, FailureClass: &empty, ResultJSON: &resultJSON,
		FinishedAt: &empty, ExpiresAt: &empty, UpdatedAt: &value.UpdatedAt,
	})
	if err != nil {
		return recordmodel.RecordMutationClaimResult{}, err
	}
	current, found, err = r.findRecordMutationExecution(ctx, value)
	if err != nil {
		return recordmodel.RecordMutationClaimResult{}, err
	}
	if !found {
		return recordmodel.RecordMutationClaimResult{}, fmt.Errorf("reclaimed record mutation receipt disappeared")
	}
	if changed {
		r.store.ObserveIdempotency(ctx, value.WorkspaceID, "record."+value.Operation, idempotency.OutcomeReclaimed)
		return recordmodel.RecordMutationClaimResult{Decision: idempotency.DecisionAcquired, Execution: current}, nil
	}
	r.store.ObserveIdempotency(ctx, value.WorkspaceID, "record."+value.Operation, idempotency.OutcomeInProgress)
	return recordmodel.RecordMutationClaimResult{Decision: idempotency.DecisionInProgress, Execution: current}, nil
}

func (r RecordStore) CommitRecordMutationExecution(ctx context.Context, commit transactionmodel.RecordMutationCommit, completion recordmodel.RecordMutationCompletion) (recordmodel.RecordMutationExecution, error) {
	workspaceID, err := requireRecordWorkspaceID(completion.WorkspaceID)
	if err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	tx, err := r.database().BeginTx(ctx, recordMutationTxOptions())
	if err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := r.applyRecordMutationTx(ctx, tx, workspaceID, commit); err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	resultJSON, err := json.Marshal(completion.Result)
	if err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	now := completion.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	filter, changes := recordMutationCompletionPatch(workspaceID, completion, resultJSON, 201, now)
	changed, err := sharedoperation.NewSQLStore(r.database(), r.store.SQLRenderer).PatchRecord(sharedoperation.WithExecutor(ctx, tx), filter, changes)
	if err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	if !changed {
		r.observeRecordMutationLeaseLost(ctx, workspaceID, completion.ExecutionID)
		return recordmodel.RecordMutationExecution{}, mutation.MutationConflict("record_mutation_execution", completion.ExecutionID, mutation.MutationConflictLeaseLost, nil)
	}
	if err := tx.Commit(); err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	return r.findRecordMutationExecutionByID(ctx, workspaceID, completion.ExecutionID)
}

func (r RecordStore) CommitRecordMutationBatchExecution(ctx context.Context, commits []transactionmodel.RecordMutationCommit, completion recordmodel.RecordMutationCompletion) (recordmodel.RecordMutationExecution, error) {
	workspaceID, err := requireRecordWorkspaceID(completion.WorkspaceID)
	if err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	if len(commits) == 0 {
		return recordmodel.RecordMutationExecution{}, fmt.Errorf("commit record mutation batch execution: commits are required")
	}
	tx, err := r.database().BeginTx(ctx, recordMutationTxOptions())
	if err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, commit := range commits {
		if err := r.applyRecordMutationTx(ctx, tx, workspaceID, commit); err != nil {
			return recordmodel.RecordMutationExecution{}, err
		}
	}
	resultJSON, err := json.Marshal(completion.Result)
	if err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	now := completion.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	filter, changes := recordMutationCompletionPatch(workspaceID, completion, resultJSON, 204, now)
	changed, err := sharedoperation.NewSQLStore(r.database(), r.store.SQLRenderer).PatchRecord(sharedoperation.WithExecutor(ctx, tx), filter, changes)
	if err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	if !changed {
		r.observeRecordMutationLeaseLost(ctx, workspaceID, completion.ExecutionID)
		return recordmodel.RecordMutationExecution{}, mutation.MutationConflict("record_mutation_execution", completion.ExecutionID, mutation.MutationConflictLeaseLost, nil)
	}
	if err := tx.Commit(); err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	return r.findRecordMutationExecutionByID(ctx, workspaceID, completion.ExecutionID)
}

func (r RecordStore) CompleteRecordMutationExecution(ctx context.Context, completion recordmodel.RecordMutationCompletion) (recordmodel.RecordMutationExecution, error) {
	workspaceID, err := requireRecordWorkspaceID(completion.WorkspaceID)
	if err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	resultJSON, err := json.Marshal(completion.Result)
	if err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	now := completion.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	responseStatus := completion.ResponseStatus
	if responseStatus == 0 {
		responseStatus = 200
	}
	filter, changes := recordMutationCompletionPatch(workspaceID, completion, resultJSON, responseStatus, now)
	changed, err := sharedoperation.NewSQLStore(r.database(), r.store.SQLRenderer).PatchRecord(ctx, filter, changes)
	if err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	if !changed {
		r.observeRecordMutationLeaseLost(ctx, workspaceID, completion.ExecutionID)
		return recordmodel.RecordMutationExecution{}, mutation.MutationConflict("record_mutation_execution", completion.ExecutionID, mutation.MutationConflictLeaseLost, nil)
	}
	return r.findRecordMutationExecutionByID(ctx, workspaceID, completion.ExecutionID)
}

func (r RecordStore) observeRecordMutationLeaseLost(ctx context.Context, workspaceID, executionID string) {
	if execution, err := r.findRecordMutationExecutionByID(ctx, workspaceID, executionID); err == nil {
		r.store.ObserveIdempotency(ctx, execution.WorkspaceID, "record."+execution.Operation, idempotency.OutcomeLeaseLost)
	}
}

func (r RecordStore) findRecordMutationExecution(ctx context.Context, scope recordmodel.RecordMutationExecution) (recordmodel.RecordMutationExecution, bool, error) {
	id := recordMutationExecutionID(scope)
	record, found, err := sharedoperation.NewSQLStore(r.database(), r.store.SQLRenderer).GetRecord(ctx, sharedoperation.RecordFilter{
		WorkspaceID: scope.WorkspaceID, ID: id, Owner: recordMutationOwner, Kind: recordMutationKind,
	})
	if err != nil || !found {
		return recordmodel.RecordMutationExecution{}, found, err
	}
	value, err := recordMutationExecution(record)
	return value, err == nil, err
}

func (r RecordStore) FindRecordMutationExecution(ctx context.Context, scope recordmodel.RecordMutationExecution) (recordmodel.RecordMutationExecution, bool, error) {
	return r.findRecordMutationExecution(ctx, scope)
}

func (r RecordStore) findRecordMutationExecutionByID(ctx context.Context, workspaceID, id string) (recordmodel.RecordMutationExecution, error) {
	workspaceID, err := requireRecordWorkspaceID(workspaceID)
	if err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	record, found, err := sharedoperation.NewSQLStore(r.database(), r.store.SQLRenderer).GetRecord(ctx, sharedoperation.RecordFilter{
		WorkspaceID: workspaceID, ID: strings.TrimSpace(id), Owner: recordMutationOwner, Kind: recordMutationKind,
	})
	if err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	if !found {
		return recordmodel.RecordMutationExecution{}, sql.ErrNoRows
	}
	return recordMutationExecution(record)
}

func recordMutationCompletionPatch(workspaceID string, completion recordmodel.RecordMutationCompletion, resultJSON json.RawMessage, responseStatus int, now time.Time) (sharedoperation.RecordFilter, sharedoperation.RecordChanges) {
	status := idempotency.StatusSucceeded
	errorCode := strings.TrimSpace(completion.ErrorCode)
	if errorCode != "" {
		status = idempotency.StatusFailedTerminal
		if completion.Retryable {
			status = idempotency.StatusFailedRetryable
		}
	}
	failureClass := ""
	if status == idempotency.StatusFailedRetryable {
		failureClass = "retryable"
	} else if status == idempotency.StatusFailedTerminal {
		failureClass = "terminal"
	}
	statusValue, metadataJSON := string(status), json.RawMessage(recordMutationMetadataJSON(responseStatus))
	expiresAt, finishedAt, token := completion.ExpiresAt.UTC().Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), completion.FencingToken
	return sharedoperation.RecordFilter{
			WorkspaceID: workspaceID, ID: completion.ExecutionID, Owner: recordMutationOwner, Kind: recordMutationKind,
			LeaseOwner: strings.TrimSpace(completion.LeaseOwner), FencingToken: &token, Status: string(idempotency.StatusProcessing),
		}, sharedoperation.RecordChanges{
			Status: &statusValue, ResultJSON: &resultJSON, MetadataJSON: &metadataJSON, ErrorCode: &errorCode,
			FailureClass: &failureClass, ExpiresAt: &expiresAt, FinishedAt: &finishedAt, UpdatedAt: &finishedAt,
		}
}

func recordMutationRecord(value recordmodel.RecordMutationExecution, resultJSON json.RawMessage) sharedoperation.Record {
	failureClass := ""
	if value.Status == string(idempotency.StatusFailedRetryable) {
		failureClass = "retryable"
	} else if value.Status == string(idempotency.StatusFailedTerminal) {
		failureClass = "terminal"
	}
	return sharedoperation.Record{
		ID: value.ID, WorkspaceID: value.WorkspaceID, Owner: recordMutationOwner, Kind: recordMutationKind,
		ActionKey: value.Operation, ResourceType: value.ObjectKey, ResourceID: value.TargetID,
		IdempotencyKey: value.ID, RequestFingerprint: value.RequestFingerprint, RequestedBy: value.ActorID, Reference: value.IdempotencyKey,
		Status: value.Status, StatusURL: "/operations/" + value.ID, ResultJSON: resultJSON,
		MetadataJSON: json.RawMessage(recordMutationMetadataJSON(value.ResponseStatus)), ErrorCode: value.ErrorCode,
		FailureClass: failureClass, RelatedIDsJSON: json.RawMessage(`[]`), Correlation: value.ID, EvidenceJSON: json.RawMessage(`[]`),
		LeaseOwner: value.LeaseOwner, LeaseExpiresAt: value.LeaseExpiresAt, FencingToken: value.FencingToken, ExpiresAt: value.ExpiresAt,
		CreatedAt: value.CreatedAt, StartedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

type recordMutationOperationMetadata struct {
	ResponseStatus int `json:"response_status"`
}

func recordMutationMetadataJSON(responseStatus int) string {
	encoded, _ := json.Marshal(recordMutationOperationMetadata{ResponseStatus: responseStatus})
	return string(encoded)
}

func recordMutationExecution(record sharedoperation.Record) (recordmodel.RecordMutationExecution, error) {
	value := recordmodel.RecordMutationExecution{
		ID: record.ID, WorkspaceID: record.WorkspaceID, Operation: record.ActionKey, ObjectKey: record.ResourceType, TargetID: record.ResourceID,
		RequestFingerprint: record.RequestFingerprint, ActorID: record.RequestedBy, IdempotencyKey: record.Reference, Status: record.Status,
		ErrorCode: record.ErrorCode, LeaseOwner: record.LeaseOwner, LeaseExpiresAt: record.LeaseExpiresAt, FencingToken: record.FencingToken,
		ExpiresAt: record.ExpiresAt, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
	if record.Owner != recordMutationOwner || record.Kind != recordMutationKind || record.IdempotencyKey != value.ID || record.Correlation != value.ID {
		return recordmodel.RecordMutationExecution{}, fmt.Errorf("record mutation operation identity is invalid")
	}
	var metadata recordMutationOperationMetadata
	if err := json.Unmarshal(record.MetadataJSON, &metadata); err != nil {
		return recordmodel.RecordMutationExecution{}, fmt.Errorf("decode record mutation metadata: %w", err)
	}
	value.ResponseStatus = metadata.ResponseStatus
	if err := json.Unmarshal(record.ResultJSON, &value.Result); err != nil {
		return recordmodel.RecordMutationExecution{}, fmt.Errorf("decode record mutation result: %w", err)
	}
	_ = json.Unmarshal(record.ResultJSON, &value.OperationResult)
	return value, nil
}

func recordMutationExecutionID(value recordmodel.RecordMutationExecution) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{strings.TrimSpace(value.WorkspaceID), strings.TrimSpace(value.Operation), strings.TrimSpace(value.ObjectKey), strings.TrimSpace(value.TargetID), strings.TrimSpace(value.IdempotencyKey)}, ":")))
	return "record_mutation:" + hex.EncodeToString(sum[:])[:20]
}

func recordMutationLease(value recordmodel.RecordMutationExecution) idempotency.Lease {
	expiresAt, _ := time.Parse(time.RFC3339Nano, value.LeaseExpiresAt)
	return idempotency.Lease{Owner: value.LeaseOwner, Token: value.FencingToken, ExpiresAt: expiresAt}
}
