package action

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

	ormdriver "github.com/domainry/domainry-orm/driver"
	"github.com/domainry/domainry-orm/query"
	actioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"

	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
)

const (
	actionExecutionOperationTable = "_operations"
	actionExecutionOwner          = "action"
	actionExecutionKind           = "action.execution"
)

type ActionBusinessExecutionStore struct {
	store         *database.RuntimeStore
	db            *sql.DB
	profile       ormdriver.Profile
	claimAttempts int
	waitAttempts  int
	claimDelay    func(int) time.Duration
	waitDelay     time.Duration
}

func NewActionBusinessExecutionStore(store *database.RuntimeStore) ActionBusinessExecutionStore {
	return ActionBusinessExecutionStore{
		store: store, db: store.DB(), profile: store.Engine, claimAttempts: 50, waitAttempts: 50,
		claimDelay: func(attempt int) time.Duration { return time.Duration(attempt+1) * time.Millisecond },
		waitDelay:  2 * time.Millisecond,
	}
}
func (r ActionBusinessExecutionStore) TryBeginExecution(ctx context.Context, request actionmodel.ActionExecutionClaimRequest) (actionmodel.ActionExecutionClaimResult, error) {
	for attempt := 0; attempt < r.claimAttempts; attempt++ {
		claim, err := r.tryBeginExecutionOnce(ctx, request)
		if err == nil || !r.store.IsTransientError(err) {
			return claim, err
		}
		timer := time.NewTimer(r.claimDelay(attempt))
		select {
		case <-ctx.Done():
			timer.Stop()
			return actionmodel.ActionExecutionClaimResult{}, ctx.Err()
		case <-timer.C:
		}
	}
	return actionmodel.ActionExecutionClaimResult{}, fmt.Errorf("claim business action execution: sqlite remained busy after retry")
}

func (r ActionBusinessExecutionStore) tryBeginExecutionOnce(ctx context.Context, request actionmodel.ActionExecutionClaimRequest) (actionmodel.ActionExecutionClaimResult, error) {
	now := request.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if request.LeaseTTL <= 0 {
		request.LeaseTTL = 30 * time.Second
	}
	value := request.Execution
	value.WorkspaceID = integrationWorkspaceID(value.WorkspaceID)
	value.ObjectKey, value.RecordID = strings.TrimSpace(value.ObjectKey), strings.TrimSpace(value.RecordID)
	value.ActionKey, value.IdempotencyKey = strings.TrimSpace(value.ActionKey), strings.TrimSpace(value.IdempotencyKey)
	value.RequestFingerprint, value.LeaseOwner = strings.TrimSpace(request.RequestFingerprint), strings.TrimSpace(request.LeaseOwner)
	value.ID = businessActionExecutionID(value.WorkspaceID, value.ObjectKey, value.RecordID, value.ActionKey, value.IdempotencyKey)
	value.Status, value.FencingToken = string(idempotency.StatusProcessing), 1
	value.LeaseExpiresAt = now.Add(request.LeaseTTL).Format(time.RFC3339Nano)
	value.CreatedAt, value.UpdatedAt = now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)
	columns := actionExecutionColumns()
	values := actionExecutionValues(value, "{}")
	insertColumns, insertValues := append(columns[:1], columns[2:]...), append(values[:1], values[2:]...)
	insertBuilder, buildErr := r.store.SubjectEvidenceInsertBuilder(value.WorkspaceID, actionExecutionOperationTable, insertColumns, insertValues)
	if buildErr != nil {
		return actionmodel.ActionExecutionClaimResult{}, fmt.Errorf("build business action execution insert: %w", buildErr)
	}
	insertQuery, insertArgs, buildErr := insertBuilder.Build()
	if buildErr != nil {
		return actionmodel.ActionExecutionClaimResult{}, fmt.Errorf("build business action execution insert: %w", buildErr)
	}
	inserted, insertErr := r.db.ExecContext(ctx, insertQuery, insertArgs...)
	if insertErr == nil {
		rows, rowsErr := inserted.RowsAffected()
		if rowsErr != nil {
			return actionmodel.ActionExecutionClaimResult{}, rowsErr
		}
		if rows != 1 {
			return actionmodel.ActionExecutionClaimResult{}, fmt.Errorf("runtime.subject_erased")
		}
		r.store.ObserveIdempotency(ctx, value.WorkspaceID, "action.execute", idempotency.OutcomeAcquired)
		return actionmodel.ActionExecutionClaimResult{Decision: idempotency.DecisionAcquired, Execution: value}, nil
	}
	current, found, err := r.waitForExecutionByScope(ctx, value.WorkspaceID, value.ObjectKey, value.RecordID, value.ActionKey, value.IdempotencyKey)
	if err != nil {
		return actionmodel.ActionExecutionClaimResult{}, err
	}
	if !found {
		return actionmodel.ActionExecutionClaimResult{}, database.MutationConstraintError(insertErr, "business_action_execution", value.ID, mutation.MutationConflictIdempotency)
	}
	decision := idempotency.Classify(idempotency.ReceiptState{Status: idempotency.Status(current.Status), Fingerprint: current.RequestFingerprint, Lease: actionExecutionLease(current)}, value.RequestFingerprint, now)
	if request.PreventReclaim && decision == idempotency.DecisionAcquired {
		decision = idempotency.DecisionInProgress
	}
	if decision != idempotency.DecisionAcquired {
		r.store.ObserveIdempotency(ctx, value.WorkspaceID, "action.execute", idempotency.OutcomeForDecision(decision, false))
		return actionmodel.ActionExecutionClaimResult{Decision: decision, Execution: current}, nil
	}
	queryValue, args, buildErr := query.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, actionExecutionOperationTable, value.WorkspaceID).
		Set("status", string(idempotency.StatusProcessing)).Set("lease_owner", value.LeaseOwner).
		Set("lease_expires_at", value.LeaseExpiresAt).
		SetExpression("fencing_token", query.Add(query.Column("fencing_token"), query.Value(1))).
		Set("result_json", "{}").Set("metadata_json", actionExecutionMetadataJSON(0, value.RoleKey)).
		Set("error_code", "").Set("failure_class", "").Set("finished_at", "").Set("expires_at", "").
		Set("updated_at", value.UpdatedAt).
		Where(query.And(
			actionExecutionOperationPredicate(value.ID), query.Equal("request_fingerprint", value.RequestFingerprint),
			query.Or(
				query.And(query.Equal("status", string(idempotency.StatusProcessing)), query.LessThanOrEqual("lease_expires_at", now.Format(time.RFC3339Nano))),
				query.Equal("status", string(idempotency.StatusFailedRetryable)),
			),
		)).Build()
	if buildErr != nil {
		return actionmodel.ActionExecutionClaimResult{}, fmt.Errorf("build business action reclaim: %w", buildErr)
	}
	result, err := r.db.ExecContext(ctx, queryValue, args...)
	if err != nil {
		return actionmodel.ActionExecutionClaimResult{}, err
	}
	rows, _ := result.RowsAffected()
	current, found, err = r.findExecutionByScope(ctx, value.WorkspaceID, value.ObjectKey, value.RecordID, value.ActionKey, value.IdempotencyKey)
	if err != nil || !found {
		return actionmodel.ActionExecutionClaimResult{}, err
	}
	if rows == 1 {
		r.store.ObserveIdempotency(ctx, value.WorkspaceID, "action.execute", idempotency.OutcomeReclaimed)
		return actionmodel.ActionExecutionClaimResult{Decision: idempotency.DecisionAcquired, Execution: current}, nil
	}
	r.store.ObserveIdempotency(ctx, value.WorkspaceID, "action.execute", idempotency.OutcomeInProgress)
	return actionmodel.ActionExecutionClaimResult{Decision: idempotency.DecisionInProgress, Execution: current}, nil
}

func (r ActionBusinessExecutionStore) FindExecution(ctx context.Context, scope actionmodel.ActionBusinessExecution) (actionmodel.ActionBusinessExecution, bool, error) {
	if strings.TrimSpace(scope.WorkspaceID) == "" || strings.TrimSpace(scope.ObjectKey) == "" || strings.TrimSpace(scope.ActionKey) == "" || strings.TrimSpace(scope.IdempotencyKey) == "" {
		return actionmodel.ActionBusinessExecution{}, false, fmt.Errorf("action receipt scope is incomplete")
	}
	return r.findExecutionByScope(ctx, scope.WorkspaceID, scope.ObjectKey, scope.RecordID, scope.ActionKey, scope.IdempotencyKey)
}

var _ actioncontract.ActionExecutionReader = ActionBusinessExecutionStore{}

func (r ActionBusinessExecutionStore) waitForExecutionByScope(ctx context.Context, workspaceID, objectKey, recordID, actionKey, idempotencyKey string) (actionmodel.ActionBusinessExecution, bool, error) {
	var lastErr error
	for attempt := 0; attempt < r.waitAttempts; attempt++ {
		value, found, err := r.findExecutionByScope(ctx, workspaceID, objectKey, recordID, actionKey, idempotencyKey)
		if err == nil && found {
			return value, true, nil
		}
		if err != nil {
			lastErr = err
		}
		timer := time.NewTimer(r.waitDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return actionmodel.ActionBusinessExecution{}, false, ctx.Err()
		case <-timer.C:
		}
	}
	return actionmodel.ActionBusinessExecution{}, false, lastErr
}

func (r ActionBusinessExecutionStore) HeartbeatExecution(ctx context.Context, executionID, leaseOwner string, fencingToken int64, expiresAt, now time.Time) error {
	queryValue, args, err := actionExecutionLeaseUpdate(r.store, executionID, leaseOwner, fencingToken).
		Set("lease_expires_at", expiresAt.UTC().Format(time.RFC3339Nano)).Set("updated_at", now.UTC().Format(time.RFC3339Nano)).Build()
	if err != nil {
		return fmt.Errorf("build business action heartbeat: %w", err)
	}
	result, err := r.db.ExecContext(ctx, queryValue, args...)
	return r.actionLeaseMutationResult(ctx, result, err, executionID)
}

func (r ActionBusinessExecutionStore) CompleteExecution(ctx context.Context, completion actionmodel.ActionExecutionCompletion) (actionmodel.ActionBusinessExecution, error) {
	now := completion.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	resultJSON, err := json.Marshal(nonNilMap(completion.Result))
	if err != nil {
		return actionmodel.ActionBusinessExecution{}, err
	}
	status := idempotency.StatusSucceeded
	if strings.TrimSpace(completion.ErrorCode) != "" {
		status = idempotency.StatusFailedTerminal
		if completion.Retryable {
			status = idempotency.StatusFailedRetryable
		}
	}
	queryValue, args, err := actionExecutionCompletionUpdate(r.store, completion, status, string(resultJSON), now)
	if err != nil {
		return actionmodel.ActionBusinessExecution{}, err
	}
	result, err := r.db.ExecContext(ctx, queryValue, args...)
	if err := r.actionLeaseMutationResult(ctx, result, err, completion.ExecutionID); err != nil {
		return actionmodel.ActionBusinessExecution{}, err
	}
	return r.findExecutionByID(ctx, completion.ExecutionID)
}

type actionExecutionTransaction struct {
	owner       ActionBusinessExecutionStore
	transaction ormdriver.Transaction
	executor    recordpersistence.TransactionExecutor
	done        bool
}

func (r ActionBusinessExecutionStore) BeginExecutionTransaction(ctx context.Context) (actioncontract.ActionExecutionTransaction, error) {
	if r.db == nil {
		return nil, fmt.Errorf("begin domain action execution transaction: database is required")
	}
	transaction, err := r.profile.BeginWrite(ctx, r.db)
	if err != nil {
		return nil, fmt.Errorf("begin domain action execution transaction: %w", err)
	}
	return &actionExecutionTransaction{owner: r, transaction: transaction, executor: transaction}, nil
}

func (t *actionExecutionTransaction) Context(ctx context.Context) context.Context {
	if t == nil || t.executor == nil {
		return ctx
	}
	return recordpersistence.WithActionExecutionTransaction(ctx, t.executor)
}

func (t *actionExecutionTransaction) RollBack(ctx context.Context) error {
	if t == nil || t.executor == nil || t.done {
		return nil
	}
	t.done = true
	err := t.transaction.Rollback(ctx)
	if errors.Is(err, sql.ErrTxDone) {
		return nil
	}
	return err
}

func (t *actionExecutionTransaction) Commit(ctx context.Context, commits []transactionmodel.RecordMutationCommit, completion actionmodel.ActionExecutionCompletion) (actionmodel.ActionBusinessExecution, error) {
	if t == nil || t.executor == nil || t.done {
		return actionmodel.ActionBusinessExecution{}, fmt.Errorf("action execution transaction is not active")
	}
	defer func() { _ = t.RollBack(ctx) }()

	recordStore := recordpersistence.NewRecordStore(t.owner.store)
	for _, commit := range commits {
		if err := recordStore.ApplyRecordMutationTx(ctx, t.executor, completion.Execution.WorkspaceID, commit); err != nil {
			return actionmodel.ActionBusinessExecution{}, database.MutationTransactionError(err, commit.Object.Key, commit.Record.ID)
		}
	}
	for _, evidence := range completion.AuditEvents {
		evidence.WorkspaceID = completion.Execution.WorkspaceID
		if err := recordStore.ApplyAuditTx(ctx, t.executor, evidence); err != nil {
			return actionmodel.ActionBusinessExecution{}, database.MutationTransactionError(err, "audit_event", evidence.ID)
		}
	}

	now := completion.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	resultJSON, err := json.Marshal(nonNilMap(completion.Result))
	if err != nil {
		return actionmodel.ActionBusinessExecution{}, err
	}
	status := idempotency.StatusSucceeded
	if strings.TrimSpace(completion.ErrorCode) != "" {
		status = idempotency.StatusFailedTerminal
		if completion.Retryable {
			status = idempotency.StatusFailedRetryable
		}
	}
	queryValue, args, err := actionExecutionCompletionUpdate(t.owner.store, completion, status, string(resultJSON), now)
	if err != nil {
		return actionmodel.ActionBusinessExecution{}, err
	}
	result, err := t.executor.ExecContext(ctx, queryValue, args...)
	if err != nil {
		return actionmodel.ActionBusinessExecution{}, database.MutationTransactionError(err, "business_action_execution", completion.ExecutionID)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return actionmodel.ActionBusinessExecution{}, err
	}
	if rows != 1 {
		return actionmodel.ActionBusinessExecution{}, mutation.MutationConflict("business_action_execution", completion.ExecutionID, mutation.MutationConflictLeaseLost, nil)
	}
	if err := ctx.Err(); err != nil {
		return actionmodel.ActionBusinessExecution{}, err
	}
	if err := t.commitSQL(ctx); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, sql.ErrTxDone) {
			return actionmodel.ActionBusinessExecution{}, err
		}
		mapped := database.MutationTransactionError(err, "business_action_execution", completion.ExecutionID)
		if mutation.IsTransactionTransient(mapped, "") || mutation.IsMutationConflict(mapped, "") {
			return actionmodel.ActionBusinessExecution{}, mapped
		}
		return actionmodel.ActionBusinessExecution{}, mutation.TransactionCommitUnknown("business_action_execution", completion.ExecutionID, err)
	}
	t.done = true
	recordStore.PublishCommittedOutboxWakeups(ctx, completion.Execution.WorkspaceID, commits)
	recordStore.PublishCommittedNotificationWakeups(ctx, completion.Execution.WorkspaceID, commits)
	return t.owner.findExecutionByID(ctx, completion.ExecutionID)
}

func (t *actionExecutionTransaction) commitSQL(ctx context.Context) error {
	return t.transaction.Commit(ctx)
}

func (r ActionBusinessExecutionStore) findExecutionByScope(ctx context.Context, workspaceID, objectKey, recordID, actionKey, idempotencyKey string) (actionmodel.ActionBusinessExecution, bool, error) {
	id := businessActionExecutionID(workspaceID, objectKey, recordID, actionKey, idempotencyKey)
	queryValue, args, err := query.NewWorkspaceSelectBuilder(r.store.SQLRenderer, actionExecutionOperationTable, workspaceID).
		Columns(actionExecutionColumns()...).
		Where(actionExecutionOperationPredicate(id)).
		Limit(1).
		Build()
	if err != nil {
		return actionmodel.ActionBusinessExecution{}, false, err
	}
	value, err := actionScanBusinessActionExecution(r.db.QueryRowContext(ctx, queryValue, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return actionmodel.ActionBusinessExecution{}, false, nil
	}
	return value, err == nil, err
}

func (r ActionBusinessExecutionStore) findExecutionByID(ctx context.Context, executionID string) (actionmodel.ActionBusinessExecution, error) {
	queryValue, args, err := query.NewSelectBuilder(r.store.SQLRenderer, actionExecutionOperationTable).
		Columns(actionExecutionColumns()...).
		Where(actionExecutionOperationPredicate(executionID)).
		Limit(1).
		Build()
	if err != nil {
		return actionmodel.ActionBusinessExecution{}, err
	}
	return actionScanBusinessActionExecution(r.db.QueryRowContext(ctx, queryValue, args...))
}

func actionExecutionLeaseUpdate(store *database.RuntimeStore, executionID, leaseOwner string, fencingToken int64) *query.UpdateBuilder {
	return query.NewUpdateBuilder(store.SQLRenderer, actionExecutionOperationTable).Where(query.And(
		actionExecutionOperationPredicate(executionID), query.Equal("lease_owner", strings.TrimSpace(leaseOwner)),
		query.Equal("fencing_token", fencingToken), query.Equal("status", string(idempotency.StatusProcessing)),
	))
}

func actionExecutionCompletionUpdate(store *database.RuntimeStore, completion actionmodel.ActionExecutionCompletion, status idempotency.Status, resultJSON string, now time.Time) (string, []any, error) {
	failureClass := ""
	if status == idempotency.StatusFailedRetryable {
		failureClass = "retryable"
	} else if status == idempotency.StatusFailedTerminal {
		failureClass = "terminal"
	}
	return query.NewWorkspaceUpdateBuilder(store.SQLRenderer, actionExecutionOperationTable, completion.Execution.WorkspaceID).Where(query.And(
		actionExecutionOperationPredicate(completion.ExecutionID), query.Equal("lease_owner", strings.TrimSpace(completion.LeaseOwner)),
		query.Equal("fencing_token", completion.FencingToken), query.Equal("status", string(idempotency.StatusProcessing)),
	)).
		Set("status", string(status)).Set("result_json", resultJSON).Set("metadata_json", actionExecutionMetadataJSON(completion.ResponseStatus, completion.Execution.RoleKey)).
		Set("error_code", strings.TrimSpace(completion.ErrorCode)).Set("failure_class", failureClass).Set("expires_at", completion.ExpiresAt.UTC().Format(time.RFC3339Nano)).
		Set("finished_at", now.Format(time.RFC3339Nano)).Set("updated_at", now.Format(time.RFC3339Nano)).Build()
}

func actionExecutionColumns() []string {
	return []string{"id", "workspace_id", "owner", "kind", "action_key", "resource_type", "resource_id", "idempotency_key", "request_fingerprint", "requested_by", "reason", "reference", "status", "status_url", "result_json", "metadata_json", "error_code", "failure_class", "next_action", "related_ids_json", "correlation", "evidence_json", "lease_owner", "lease_expires_at", "fencing_token", "expires_at", "created_at", "started_at", "finished_at", "updated_at"}
}

func actionExecutionValues(value actionmodel.ActionBusinessExecution, resultJSON string) []any {
	failureClass := ""
	if value.Status == string(idempotency.StatusFailedRetryable) {
		failureClass = "retryable"
	} else if value.Status == string(idempotency.StatusFailedTerminal) {
		failureClass = "terminal"
	}
	return []any{value.ID, value.WorkspaceID, actionExecutionOwner, actionExecutionKind, value.ActionKey, value.ObjectKey, value.RecordID, value.ID, value.RequestFingerprint, value.ActorID, "", value.IdempotencyKey, value.Status, "/operations/" + value.ID, resultJSON, actionExecutionMetadataJSON(value.ResponseStatus, value.RoleKey), value.ErrorCode, failureClass, "", "[]", value.ID, "[]", value.LeaseOwner, value.LeaseExpiresAt, value.FencingToken, value.ExpiresAt, value.CreatedAt, value.CreatedAt, "", value.UpdatedAt}
}

type actionExecutionOperationMetadata struct {
	ResponseStatus int    `json:"response_status"`
	RoleKey        string `json:"role_key,omitempty"`
}

func actionExecutionMetadataJSON(responseStatus int, roleKey string) string {
	encoded, _ := json.Marshal(actionExecutionOperationMetadata{ResponseStatus: responseStatus, RoleKey: strings.TrimSpace(roleKey)})
	return string(encoded)
}

func actionExecutionLease(value actionmodel.ActionBusinessExecution) idempotency.Lease {
	expiresAt, _ := time.Parse(time.RFC3339Nano, value.LeaseExpiresAt)
	return idempotency.Lease{Owner: value.LeaseOwner, Token: value.FencingToken, ExpiresAt: expiresAt}
}

func (r ActionBusinessExecutionStore) actionLeaseMutationResult(ctx context.Context, result sql.Result, err error, executionID string) error {
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		if execution, loadErr := r.findExecutionByID(ctx, executionID); loadErr == nil {
			r.store.ObserveIdempotency(ctx, execution.WorkspaceID, "action.execute", idempotency.OutcomeLeaseLost)
		}
		return mutation.MutationConflict("business_action_execution", executionID, mutation.MutationConflictLeaseLost, nil)
	}
	return nil
}

type rowScanner interface{ Scan(...any) error }

func actionScanBusinessActionExecution(row rowScanner) (actionmodel.ActionBusinessExecution, error) {
	var execution actionmodel.ActionBusinessExecution
	var owner, kind, operationKey, reason, statusURL, resultJSON, metadataJSON, failureClass, nextAction, relatedIDs, correlation, evidence, startedAt, finishedAt string
	if err := row.Scan(&execution.ID, &execution.WorkspaceID, &owner, &kind, &execution.ActionKey, &execution.ObjectKey, &execution.RecordID, &operationKey, &execution.RequestFingerprint, &execution.ActorID, &reason, &execution.IdempotencyKey, &execution.Status, &statusURL, &resultJSON, &metadataJSON, &execution.ErrorCode, &failureClass, &nextAction, &relatedIDs, &correlation, &evidence, &execution.LeaseOwner, &execution.LeaseExpiresAt, &execution.FencingToken, &execution.ExpiresAt, &execution.CreatedAt, &startedAt, &finishedAt, &execution.UpdatedAt); err != nil {
		return actionmodel.ActionBusinessExecution{}, err
	}
	if owner != actionExecutionOwner || kind != actionExecutionKind || operationKey != execution.ID || correlation != execution.ID {
		return actionmodel.ActionBusinessExecution{}, fmt.Errorf("action execution operation identity is invalid")
	}
	var metadata actionExecutionOperationMetadata
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		return actionmodel.ActionBusinessExecution{}, fmt.Errorf("decode action execution metadata: %w", err)
	}
	execution.ResponseStatus, execution.RoleKey = metadata.ResponseStatus, metadata.RoleKey
	_ = json.Unmarshal([]byte(resultJSON), &execution.Result)
	execution.Result = nonNilMap(execution.Result)
	return execution, nil
}

func actionExecutionOperationPredicate(executionID string) query.Predicate {
	return query.And(
		query.Equal("id", strings.TrimSpace(executionID)),
		query.Equal("owner", actionExecutionOwner),
		query.Equal("kind", actionExecutionKind),
	)
}

func businessActionExecutionID(workspaceID, objectKey, recordID, actionKey, idempotencyKey string) string {
	return "business_action:" + integrationWorkspaceID(workspaceID) + ":" + businessActionShortHash(strings.Join([]string{strings.TrimSpace(objectKey), strings.TrimSpace(recordID), strings.TrimSpace(actionKey), strings.TrimSpace(idempotencyKey)}, ":"))
}

func businessActionShortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:16]
}

func integrationWorkspaceID(value string) string {
	return strings.TrimSpace(value)
}

func nonNilMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}
