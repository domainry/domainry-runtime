// Business action execution persistence.
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

	actioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
	"github.com/domainry/domainry-runtime/runtime/platform/mutation"
)

type ActionBusinessExecutionStore struct {
	store         *database.RuntimeStore
	db            *sql.DB
	driver        string
	claimAttempts int
	waitAttempts  int
	claimDelay    func(int) time.Duration
	waitDelay     time.Duration
}

func NewActionBusinessExecutionStore(store *database.RuntimeStore) ActionBusinessExecutionStore {
	return ActionBusinessExecutionStore{
		store: store, db: store.DB(), driver: store.Driver(), claimAttempts: 50, waitAttempts: 50,
		claimDelay: func(attempt int) time.Duration { return time.Duration(attempt+1) * time.Millisecond },
		waitDelay:  2 * time.Millisecond,
	}
}
func (r ActionBusinessExecutionStore) TryBeginExecution(ctx context.Context, request actionmodel.ActionExecutionClaimRequest) (actionmodel.ActionExecutionClaimResult, error) {
	for attempt := 0; attempt < r.claimAttempts; attempt++ {
		claim, err := r.tryBeginExecutionOnce(ctx, request)
		if err == nil || !r.isSQLiteBusyError(err) {
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
	_, insertErr := r.db.ExecContext(ctx, "INSERT INTO "+r.store.TableIdentifier("business_action_executions")+" ("+stringsJoinIdentifiers(r.store, columns...)+") VALUES ("+stringsJoinPlaceholders(r.store, len(columns))+")", actionExecutionValues(value, "{}")...)
	if insertErr == nil {
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
	if decision != idempotency.DecisionAcquired {
		r.store.ObserveIdempotency(ctx, value.WorkspaceID, "action.execute", idempotency.OutcomeForDecision(decision, false))
		return actionmodel.ActionExecutionClaimResult{Decision: decision, Execution: current}, nil
	}
	query := "UPDATE " + r.store.TableIdentifier("business_action_executions") + " SET " + r.store.Identifier("status") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("lease_owner") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("lease_expires_at") + " = " + r.store.Placeholder(3) + ", " + r.store.Identifier("fencing_token") + " = " + r.store.Identifier("fencing_token") + " + 1, " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(4) + " WHERE " + r.store.Identifier("id") + " = " + r.store.Placeholder(5) + " AND " + r.store.Identifier("request_fingerprint") + " = " + r.store.Placeholder(6) + " AND ((" + r.store.Identifier("status") + " = " + r.store.Placeholder(7) + " AND " + r.store.Identifier("lease_expires_at") + " <= " + r.store.Placeholder(8) + ") OR " + r.store.Identifier("status") + " = " + r.store.Placeholder(9) + ")"
	result, err := r.db.ExecContext(ctx, query, string(idempotency.StatusProcessing), value.LeaseOwner, value.LeaseExpiresAt, value.UpdatedAt, value.ID, value.RequestFingerprint, string(idempotency.StatusProcessing), now.Format(time.RFC3339Nano), string(idempotency.StatusFailedRetryable))
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

func (r ActionBusinessExecutionStore) isSQLiteBusyError(err error) bool {
	if err == nil || r.driver != "sqlite" {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") || strings.Contains(message, "database is locked") || strings.Contains(message, "database table is locked")
}

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
	query := "UPDATE " + r.store.TableIdentifier("business_action_executions") + " SET " + r.store.Identifier("lease_expires_at") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(2) + " WHERE " + r.store.Identifier("id") + " = " + r.store.Placeholder(3) + " AND " + r.store.Identifier("lease_owner") + " = " + r.store.Placeholder(4) + " AND " + r.store.Identifier("fencing_token") + " = " + r.store.Placeholder(5) + " AND " + r.store.Identifier("status") + " = " + r.store.Placeholder(6)
	result, err := r.db.ExecContext(ctx, query, expiresAt.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano), executionID, strings.TrimSpace(leaseOwner), fencingToken, string(idempotency.StatusProcessing))
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
	query := "UPDATE " + r.store.TableIdentifier("business_action_executions") + " SET " + r.store.Identifier("status") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("result_json") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("response_status") + " = " + r.store.Placeholder(3) + ", " + r.store.Identifier("error_code") + " = " + r.store.Placeholder(4) + ", " + r.store.Identifier("expires_at") + " = " + r.store.Placeholder(5) + ", " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(6) + " WHERE " + r.store.Identifier("id") + " = " + r.store.Placeholder(7) + " AND " + r.store.Identifier("lease_owner") + " = " + r.store.Placeholder(8) + " AND " + r.store.Identifier("fencing_token") + " = " + r.store.Placeholder(9) + " AND " + r.store.Identifier("status") + " = " + r.store.Placeholder(10)
	result, err := r.db.ExecContext(ctx, query, string(status), string(resultJSON), completion.ResponseStatus, strings.TrimSpace(completion.ErrorCode), completion.ExpiresAt.UTC().Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), completion.ExecutionID, strings.TrimSpace(completion.LeaseOwner), completion.FencingToken, string(idempotency.StatusProcessing))
	if err := r.actionLeaseMutationResult(ctx, result, err, completion.ExecutionID); err != nil {
		return actionmodel.ActionBusinessExecution{}, err
	}
	return r.findExecutionByID(ctx, completion.ExecutionID)
}

type actionExecutionTransaction struct {
	owner    ActionBusinessExecutionStore
	executor recordpersistence.TransactionExecutor
	tx       *sql.Tx
	conn     *sql.Conn
	done     bool
}

func (r ActionBusinessExecutionStore) BeginExecutionTransaction(ctx context.Context) (actioncontract.ActionExecutionTransaction, error) {
	if r.db == nil {
		return nil, fmt.Errorf("begin domain action execution transaction: database is required")
	}
	if r.driver == "sqlite" {
		conn, err := r.db.Conn(ctx)
		if err != nil {
			return nil, fmt.Errorf("acquire sqlite domain action connection: %w", err)
		}
		if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("begin sqlite domain action execution transaction: %w", err)
		}
		return &actionExecutionTransaction{owner: r, executor: conn, conn: conn}, nil
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, fmt.Errorf("begin domain action execution transaction: %w", err)
	}
	return &actionExecutionTransaction{owner: r, executor: tx, tx: tx}, nil
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
	var err error
	if t.tx != nil {
		err = t.tx.Rollback()
	} else {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, err = t.conn.ExecContext(cleanupCtx, "ROLLBACK")
		closeErr := t.conn.Close()
		if err == nil {
			err = closeErr
		}
	}
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
	query := "UPDATE " + t.owner.store.TableIdentifier("business_action_executions") + " SET " + t.owner.store.Identifier("status") + " = " + t.owner.store.Placeholder(1) + ", " + t.owner.store.Identifier("result_json") + " = " + t.owner.store.Placeholder(2) + ", " + t.owner.store.Identifier("response_status") + " = " + t.owner.store.Placeholder(3) + ", " + t.owner.store.Identifier("error_code") + " = " + t.owner.store.Placeholder(4) + ", " + t.owner.store.Identifier("expires_at") + " = " + t.owner.store.Placeholder(5) + ", " + t.owner.store.Identifier("updated_at") + " = " + t.owner.store.Placeholder(6) + " WHERE " + t.owner.store.Identifier("id") + " = " + t.owner.store.Placeholder(7) + " AND " + t.owner.store.Identifier("lease_owner") + " = " + t.owner.store.Placeholder(8) + " AND " + t.owner.store.Identifier("fencing_token") + " = " + t.owner.store.Placeholder(9) + " AND " + t.owner.store.Identifier("status") + " = " + t.owner.store.Placeholder(10)
	result, err := t.executor.ExecContext(ctx, query, string(status), string(resultJSON), completion.ResponseStatus, strings.TrimSpace(completion.ErrorCode), completion.ExpiresAt.UTC().Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), completion.ExecutionID, strings.TrimSpace(completion.LeaseOwner), completion.FencingToken, string(idempotency.StatusProcessing))
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
	if t.tx != nil {
		return t.tx.Commit()
	}
	_, err := t.conn.ExecContext(ctx, "COMMIT")
	if err != nil {
		return err
	}
	// COMMIT succeeded even if returning the dedicated connection fails. Mark
	// the transaction terminal so the deferred cleanup cannot issue ROLLBACK
	// against an already committed connection.
	t.done = true
	_ = t.conn.Close()
	return nil
}

func (r ActionBusinessExecutionStore) findExecutionByScope(ctx context.Context, workspaceID, objectKey, recordID, actionKey, idempotencyKey string) (actionmodel.ActionBusinessExecution, bool, error) {
	query := "SELECT " + stringsJoinIdentifiers(r.store, actionExecutionColumns()...) + " FROM " + r.store.TableIdentifier("business_action_executions") + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1) + " AND " + r.store.Identifier("object_key") + " = " + r.store.Placeholder(2) + " AND " + r.store.Identifier("record_id") + " = " + r.store.Placeholder(3) + " AND " + r.store.Identifier("action_key") + " = " + r.store.Placeholder(4) + " AND " + r.store.Identifier("idempotency_key") + " = " + r.store.Placeholder(5) + " LIMIT 1"
	value, err := actionScanBusinessActionExecution(r.db.QueryRowContext(ctx, query, workspaceID, objectKey, recordID, actionKey, idempotencyKey))
	if errors.Is(err, sql.ErrNoRows) {
		return actionmodel.ActionBusinessExecution{}, false, nil
	}
	return value, err == nil, err
}

func (r ActionBusinessExecutionStore) findExecutionByID(ctx context.Context, executionID string) (actionmodel.ActionBusinessExecution, error) {
	query := "SELECT " + stringsJoinIdentifiers(r.store, actionExecutionColumns()...) + " FROM " + r.store.TableIdentifier("business_action_executions") + " WHERE " + r.store.Identifier("id") + " = " + r.store.Placeholder(1) + " LIMIT 1"
	return actionScanBusinessActionExecution(r.db.QueryRowContext(ctx, query, executionID))
}

func actionExecutionColumns() []string {
	return []string{"id", "workspace_id", "object_key", "record_id", "action_key", "idempotency_key", "request_fingerprint", "status", "result_json", "lease_owner", "lease_expires_at", "fencing_token", "response_status", "error_code", "expires_at", "actor_id", "role_key", "created_at", "updated_at"}
}

func actionExecutionValues(value actionmodel.ActionBusinessExecution, resultJSON string) []any {
	return []any{value.ID, value.WorkspaceID, value.ObjectKey, value.RecordID, value.ActionKey, value.IdempotencyKey, value.RequestFingerprint, value.Status, resultJSON, value.LeaseOwner, value.LeaseExpiresAt, value.FencingToken, value.ResponseStatus, value.ErrorCode, value.ExpiresAt, value.ActorID, value.RoleKey, value.CreatedAt, value.UpdatedAt}
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
	var resultJSON string
	if err := row.Scan(&execution.ID, &execution.WorkspaceID, &execution.ObjectKey, &execution.RecordID, &execution.ActionKey, &execution.IdempotencyKey, &execution.RequestFingerprint, &execution.Status, &resultJSON, &execution.LeaseOwner, &execution.LeaseExpiresAt, &execution.FencingToken, &execution.ResponseStatus, &execution.ErrorCode, &execution.ExpiresAt, &execution.ActorID, &execution.RoleKey, &execution.CreatedAt, &execution.UpdatedAt); err != nil {
		return actionmodel.ActionBusinessExecution{}, err
	}
	_ = json.Unmarshal([]byte(resultJSON), &execution.Result)
	execution.Result = nonNilMap(execution.Result)
	return execution, nil
}

func businessActionExecutionID(workspaceID, objectKey, recordID, actionKey, idempotencyKey string) string {
	return "business_action:" + integrationWorkspaceID(workspaceID) + ":" + businessActionShortHash(strings.Join([]string{strings.TrimSpace(objectKey), strings.TrimSpace(recordID), strings.TrimSpace(actionKey), strings.TrimSpace(idempotencyKey)}, ":"))
}

func businessActionShortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:16]
}

func integrationWorkspaceID(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return "default"
}

func stringsJoinIdentifiers(store *database.RuntimeStore, columns ...string) string {
	values := make([]string, 0, len(columns))
	for _, column := range columns {
		values = append(values, store.Identifier(column))
	}
	return strings.Join(values, ", ")
}

func stringsJoinPlaceholders(store *database.RuntimeStore, count int) string {
	values := make([]string, 0, count)
	for position := 1; position <= count; position++ {
		values = append(values, store.Placeholder(position))
	}
	return strings.Join(values, ", ")
}

func nonNilMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}
