package record

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
	"github.com/domainry/domainry-orm/query"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

const (
	recordMutationOperationTable = "_operations"
	recordMutationOwner          = "record"
	recordMutationKind           = "record.mutation"
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
	columns := recordMutationExecutionColumns()
	values := recordMutationExecutionValues(value, "{}")
	insertColumns, insertValues := append(columns[:1], columns[2:]...), append(values[:1], values[2:]...)
	insertBuilder, buildErr := r.store.SubjectEvidenceInsertBuilder(workspaceID, recordMutationOperationTable, insertColumns, insertValues)
	if buildErr != nil {
		return recordmodel.RecordMutationClaimResult{}, buildErr
	}
	queryValue, args, buildErr := insertBuilder.Build()
	if buildErr != nil {
		return recordmodel.RecordMutationClaimResult{}, buildErr
	}
	inserted, insertErr := r.database().ExecContext(ctx, queryValue, args...)
	if insertErr == nil {
		rows, rowsErr := inserted.RowsAffected()
		if rowsErr != nil {
			return recordmodel.RecordMutationClaimResult{}, rowsErr
		}
		if rows != 1 {
			return recordmodel.RecordMutationClaimResult{}, fmt.Errorf("runtime.subject_erased")
		}
		r.store.ObserveIdempotency(ctx, value.WorkspaceID, "record."+value.Operation, idempotency.OutcomeAcquired)
		return recordmodel.RecordMutationClaimResult{Decision: idempotency.DecisionAcquired, Execution: value}, nil
	}
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
	queryValue, args, err = query.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, recordMutationOperationTable, workspaceID).
		Set("status", string(idempotency.StatusProcessing)).Set("lease_owner", value.LeaseOwner).
		Set("lease_expires_at", value.LeaseExpiresAt).
		SetExpression("fencing_token", query.Add(query.Column("fencing_token"), query.Value(1))).
		Set("metadata_json", recordMutationMetadataJSON(0)).Set("error_code", "").Set("failure_class", "").Set("result_json", "{}").
		Set("finished_at", "").Set("expires_at", "").
		Set("updated_at", value.UpdatedAt).
		Where(query.And(
			recordMutationOperationPredicate(value.ID),
			query.Equal("request_fingerprint", value.RequestFingerprint),
			query.Or(
				query.Equal("status", string(idempotency.StatusFailedRetryable)),
				query.And(query.Equal("status", string(idempotency.StatusProcessing)), query.LessThanOrEqual("lease_expires_at", now.Format(time.RFC3339Nano))),
			),
		)).Build()
	if err != nil {
		return recordmodel.RecordMutationClaimResult{}, err
	}
	result, err := r.database().ExecContext(ctx, queryValue, args...)
	if err != nil {
		return recordmodel.RecordMutationClaimResult{}, err
	}
	rows, err := result.RowsAffected()
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
	if rows == 1 {
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
	queryValue, args, err := recordMutationCompletionUpdate(r.store, workspaceID, completion, string(resultJSON), 201, now)
	if err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	result, err := tx.ExecContext(ctx, queryValue, args...)
	if err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	if rows != 1 {
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
	queryValue, args, err := recordMutationCompletionUpdate(r.store, workspaceID, completion, string(resultJSON), 204, now)
	if err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	result, err := tx.ExecContext(ctx, queryValue, args...)
	if err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	if rows != 1 {
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
	queryValue, args, err := recordMutationCompletionUpdate(r.store, workspaceID, completion, string(resultJSON), responseStatus, now)
	if err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	result, err := r.database().ExecContext(ctx, queryValue, args...)
	if err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	if rows != 1 {
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
	queryValue, args, err := query.NewWorkspaceSelectBuilder(r.store.SQLRenderer, recordMutationOperationTable, scope.WorkspaceID).
		Columns(recordMutationExecutionColumns()...).
		Where(recordMutationOperationPredicate(id)).
		Limit(1).
		Build()
	if err != nil {
		return recordmodel.RecordMutationExecution{}, false, err
	}
	value, err := scanRecordMutationExecution(r.database().QueryRowContext(ctx, queryValue, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return recordmodel.RecordMutationExecution{}, false, nil
	}
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
	queryValue, args, err := query.NewWorkspaceSelectBuilder(r.store.SQLRenderer, recordMutationOperationTable, workspaceID).
		Columns(recordMutationExecutionColumns()...).
		Where(recordMutationOperationPredicate(id)).
		Build()
	if err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	return scanRecordMutationExecution(r.database().QueryRowContext(ctx, queryValue, args...))
}

func recordMutationCompletionUpdate(store *database.RuntimeStore, workspaceID string, completion recordmodel.RecordMutationCompletion, resultJSON string, responseStatus int, now time.Time) (string, []any, error) {
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
	return query.NewWorkspaceUpdateBuilder(store.SQLRenderer, recordMutationOperationTable, workspaceID).
		Set("status", string(status)).Set("result_json", resultJSON).Set("metadata_json", recordMutationMetadataJSON(responseStatus)).Set("error_code", errorCode).Set("failure_class", failureClass).
		Set("expires_at", completion.ExpiresAt.UTC().Format(time.RFC3339Nano)).Set("finished_at", now.Format(time.RFC3339Nano)).Set("updated_at", now.Format(time.RFC3339Nano)).
		Where(query.And(recordMutationOperationPredicate(completion.ExecutionID), query.Equal("lease_owner", strings.TrimSpace(completion.LeaseOwner)), query.Equal("fencing_token", completion.FencingToken), query.Equal("status", string(idempotency.StatusProcessing)))).Build()
}

func recordMutationExecutionColumns() []string {
	return []string{"id", "workspace_id", "owner", "kind", "action_key", "resource_type", "resource_id", "idempotency_key", "request_fingerprint", "requested_by", "reason", "reference", "status", "status_url", "result_json", "metadata_json", "error_code", "failure_class", "next_action", "related_ids_json", "correlation", "evidence_json", "lease_owner", "lease_expires_at", "fencing_token", "expires_at", "created_at", "started_at", "finished_at", "updated_at"}
}

func recordMutationExecutionValues(value recordmodel.RecordMutationExecution, resultJSON string) []any {
	failureClass := ""
	if value.Status == string(idempotency.StatusFailedRetryable) {
		failureClass = "retryable"
	} else if value.Status == string(idempotency.StatusFailedTerminal) {
		failureClass = "terminal"
	}
	return []any{value.ID, value.WorkspaceID, recordMutationOwner, recordMutationKind, value.Operation, value.ObjectKey, value.TargetID, value.ID, value.RequestFingerprint, value.ActorID, "", value.IdempotencyKey, value.Status, "/operations/" + value.ID, resultJSON, recordMutationMetadataJSON(value.ResponseStatus), value.ErrorCode, failureClass, "", "[]", value.ID, "[]", value.LeaseOwner, value.LeaseExpiresAt, value.FencingToken, value.ExpiresAt, value.CreatedAt, value.CreatedAt, "", value.UpdatedAt}
}

type recordMutationOperationMetadata struct {
	ResponseStatus int `json:"response_status"`
}

func recordMutationMetadataJSON(responseStatus int) string {
	encoded, _ := json.Marshal(recordMutationOperationMetadata{ResponseStatus: responseStatus})
	return string(encoded)
}

type recordMutationExecutionScanner interface{ Scan(...any) error }

func scanRecordMutationExecution(row recordMutationExecutionScanner) (recordmodel.RecordMutationExecution, error) {
	var value recordmodel.RecordMutationExecution
	var owner, kind, operationKey, reason, statusURL, resultJSON, metadataJSON, failureClass, nextAction, relatedIDs, correlation, evidence, startedAt, finishedAt string
	if err := row.Scan(&value.ID, &value.WorkspaceID, &owner, &kind, &value.Operation, &value.ObjectKey, &value.TargetID, &operationKey, &value.RequestFingerprint, &value.ActorID, &reason, &value.IdempotencyKey, &value.Status, &statusURL, &resultJSON, &metadataJSON, &value.ErrorCode, &failureClass, &nextAction, &relatedIDs, &correlation, &evidence, &value.LeaseOwner, &value.LeaseExpiresAt, &value.FencingToken, &value.ExpiresAt, &value.CreatedAt, &startedAt, &finishedAt, &value.UpdatedAt); err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	if owner != recordMutationOwner || kind != recordMutationKind || operationKey != value.ID || correlation != value.ID {
		return recordmodel.RecordMutationExecution{}, fmt.Errorf("record mutation operation identity is invalid")
	}
	var metadata recordMutationOperationMetadata
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		return recordmodel.RecordMutationExecution{}, fmt.Errorf("decode record mutation metadata: %w", err)
	}
	value.ResponseStatus = metadata.ResponseStatus
	if err := json.Unmarshal([]byte(resultJSON), &value.Result); err != nil {
		return recordmodel.RecordMutationExecution{}, fmt.Errorf("decode record mutation result: %w", err)
	}
	_ = json.Unmarshal([]byte(resultJSON), &value.OperationResult)
	return value, nil
}

func recordMutationOperationPredicate(id string) query.Predicate {
	return query.And(
		query.Equal("id", strings.TrimSpace(id)),
		query.Equal("owner", recordMutationOwner),
		query.Equal("kind", recordMutationKind),
	)
}

func recordMutationExecutionID(value recordmodel.RecordMutationExecution) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{strings.TrimSpace(value.WorkspaceID), strings.TrimSpace(value.Operation), strings.TrimSpace(value.ObjectKey), strings.TrimSpace(value.TargetID), strings.TrimSpace(value.IdempotencyKey)}, ":")))
	return "record_mutation:" + hex.EncodeToString(sum[:])[:20]
}

func recordMutationLease(value recordmodel.RecordMutationExecution) idempotency.Lease {
	expiresAt, _ := time.Parse(time.RFC3339Nano, value.LeaseExpiresAt)
	return idempotency.Lease{Owner: value.LeaseOwner, Token: value.FencingToken, ExpiresAt: expiresAt}
}
