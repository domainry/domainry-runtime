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
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func (r RecordStore) TryBeginRecordMutation(ctx context.Context, request recordmodel.RecordMutationClaimRequest) (recordmodel.RecordMutationClaimResult, error) {
	for attempt := 0; attempt < 50; attempt++ {
		claim, err := r.tryBeginRecordMutationOnce(ctx, request)
		if err == nil || !r.recordMutationSQLiteBusy(err) {
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
	_, insertErr := r.database().ExecContext(ctx, "INSERT INTO "+r.store.TableIdentifier("record_mutation_executions")+" ("+stringsJoinIdentifiers(r.store, columns...)+") VALUES ("+stringsJoinPlaceholders(r.store, len(columns))+")", recordMutationExecutionValues(value, "{}")...)
	if insertErr == nil {
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
	query := "UPDATE " + r.store.TableIdentifier("record_mutation_executions") + " SET " + r.store.Identifier("status") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("lease_owner") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("lease_expires_at") + " = " + r.store.Placeholder(3) + ", " + r.store.Identifier("fencing_token") + " = " + r.store.Identifier("fencing_token") + " + 1, " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(4) + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(5) + " AND " + r.store.Identifier("id") + " = " + r.store.Placeholder(6) + " AND " + r.store.Identifier("request_fingerprint") + " = " + r.store.Placeholder(7) + " AND " + r.store.Identifier("status") + " = " + r.store.Placeholder(8) + " AND " + r.store.Identifier("lease_expires_at") + " <= " + r.store.Placeholder(9)
	result, err := r.database().ExecContext(ctx, query, string(idempotency.StatusProcessing), value.LeaseOwner, value.LeaseExpiresAt, value.UpdatedAt, value.WorkspaceID, value.ID, value.RequestFingerprint, string(idempotency.StatusProcessing), now.Format(time.RFC3339Nano))
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

func (r RecordStore) recordMutationSQLiteBusy(err error) bool {
	if err == nil || r.store.Driver() != "sqlite" {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") || strings.Contains(message, "database is locked") || strings.Contains(message, "database table is locked")
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
	query := "UPDATE " + r.store.TableIdentifier("record_mutation_executions") + " SET " + r.store.Identifier("status") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("result_json") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("response_status") + " = " + r.store.Placeholder(3) + ", " + r.store.Identifier("expires_at") + " = " + r.store.Placeholder(4) + ", " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(5) + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(6) + " AND " + r.store.Identifier("id") + " = " + r.store.Placeholder(7) + " AND " + r.store.Identifier("lease_owner") + " = " + r.store.Placeholder(8) + " AND " + r.store.Identifier("fencing_token") + " = " + r.store.Placeholder(9) + " AND " + r.store.Identifier("status") + " = " + r.store.Placeholder(10)
	result, err := tx.ExecContext(ctx, query, string(idempotency.StatusSucceeded), string(resultJSON), 201, completion.ExpiresAt.UTC().Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), workspaceID, completion.ExecutionID, strings.TrimSpace(completion.LeaseOwner), completion.FencingToken, string(idempotency.StatusProcessing))
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
	query := "UPDATE " + r.store.TableIdentifier("record_mutation_executions") + " SET " + r.store.Identifier("status") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("result_json") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("response_status") + " = " + r.store.Placeholder(3) + ", " + r.store.Identifier("expires_at") + " = " + r.store.Placeholder(4) + ", " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(5) + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(6) + " AND " + r.store.Identifier("id") + " = " + r.store.Placeholder(7) + " AND " + r.store.Identifier("lease_owner") + " = " + r.store.Placeholder(8) + " AND " + r.store.Identifier("fencing_token") + " = " + r.store.Placeholder(9) + " AND " + r.store.Identifier("status") + " = " + r.store.Placeholder(10)
	result, err := r.database().ExecContext(ctx, query, string(idempotency.StatusSucceeded), string(resultJSON), 200, completion.ExpiresAt.UTC().Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), workspaceID, completion.ExecutionID, strings.TrimSpace(completion.LeaseOwner), completion.FencingToken, string(idempotency.StatusProcessing))
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
	query := "SELECT " + stringsJoinIdentifiers(r.store, recordMutationExecutionColumns()...) + " FROM " + r.store.TableIdentifier("record_mutation_executions") + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1) + " AND " + r.store.Identifier("operation") + " = " + r.store.Placeholder(2) + " AND " + r.store.Identifier("object_key") + " = " + r.store.Placeholder(3) + " AND " + r.store.Identifier("target_id") + " = " + r.store.Placeholder(4) + " AND " + r.store.Identifier("idempotency_key") + " = " + r.store.Placeholder(5) + " LIMIT 1"
	value, err := scanRecordMutationExecution(r.database().QueryRowContext(ctx, query, scope.WorkspaceID, scope.Operation, scope.ObjectKey, scope.TargetID, scope.IdempotencyKey))
	if errors.Is(err, sql.ErrNoRows) {
		return recordmodel.RecordMutationExecution{}, false, nil
	}
	return value, err == nil, err
}

// FindRecordMutationExecution exposes a read-only receipt lookup so callers can
// return a completed idempotent replay before state-dependent validation runs.
func (r RecordStore) FindRecordMutationExecution(ctx context.Context, scope recordmodel.RecordMutationExecution) (recordmodel.RecordMutationExecution, bool, error) {
	return r.findRecordMutationExecution(ctx, scope)
}

func (r RecordStore) findRecordMutationExecutionByID(ctx context.Context, workspaceID, id string) (recordmodel.RecordMutationExecution, error) {
	workspaceID, err := requireRecordWorkspaceID(workspaceID)
	if err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	query := "SELECT " + stringsJoinIdentifiers(r.store, recordMutationExecutionColumns()...) + " FROM " + r.store.TableIdentifier("record_mutation_executions") + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1) + " AND " + r.store.Identifier("id") + " = " + r.store.Placeholder(2)
	return scanRecordMutationExecution(r.database().QueryRowContext(ctx, query, workspaceID, id))
}

func recordMutationExecutionColumns() []string {
	return []string{"id", "workspace_id", "operation", "object_key", "target_id", "idempotency_key", "request_fingerprint", "status", "result_json", "lease_owner", "lease_expires_at", "fencing_token", "response_status", "error_code", "expires_at", "actor_id", "created_at", "updated_at"}
}

func recordMutationExecutionValues(value recordmodel.RecordMutationExecution, resultJSON string) []any {
	return []any{value.ID, value.WorkspaceID, value.Operation, value.ObjectKey, value.TargetID, value.IdempotencyKey, value.RequestFingerprint, value.Status, resultJSON, value.LeaseOwner, value.LeaseExpiresAt, value.FencingToken, value.ResponseStatus, value.ErrorCode, value.ExpiresAt, value.ActorID, value.CreatedAt, value.UpdatedAt}
}

type recordMutationExecutionScanner interface{ Scan(...any) error }

func scanRecordMutationExecution(row recordMutationExecutionScanner) (recordmodel.RecordMutationExecution, error) {
	var value recordmodel.RecordMutationExecution
	var resultJSON string
	if err := row.Scan(&value.ID, &value.WorkspaceID, &value.Operation, &value.ObjectKey, &value.TargetID, &value.IdempotencyKey, &value.RequestFingerprint, &value.Status, &resultJSON, &value.LeaseOwner, &value.LeaseExpiresAt, &value.FencingToken, &value.ResponseStatus, &value.ErrorCode, &value.ExpiresAt, &value.ActorID, &value.CreatedAt, &value.UpdatedAt); err != nil {
		return recordmodel.RecordMutationExecution{}, err
	}
	if err := json.Unmarshal([]byte(resultJSON), &value.Result); err != nil {
		return recordmodel.RecordMutationExecution{}, fmt.Errorf("decode record mutation result: %w", err)
	}
	_ = json.Unmarshal([]byte(resultJSON), &value.OperationResult)
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
