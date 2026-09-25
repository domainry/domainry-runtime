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
	actioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"

	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
	sharedoperation "github.com/domainry/domainry-foundation/operation"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
)

const (
	actionExecutionOwner = "action"
	actionExecutionKind  = "action.execution"
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
	operationStore := sharedoperation.NewSQLStore(r.db, r.store.SQLRenderer)
	tx, beginErr := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if beginErr != nil {
		return actionmodel.ActionExecutionClaimResult{}, beginErr
	}
	if guardErr := r.store.GuardSubjectEvidenceWrite(ctx, tx, value.WorkspaceID, sharedoperation.TableName,
		[]string{"id", "owner", "resource_type", "resource_id", "requested_by"},
		[]any{value.ID, actionExecutionOwner, value.ObjectKey, value.RecordID, value.ActorID}); guardErr != nil {
		_ = tx.Rollback()
		return actionmodel.ActionExecutionClaimResult{}, guardErr
	}
	inserted, insertErr := operationStore.InsertRecord(sharedoperation.WithExecutor(ctx, tx), actionExecutionRecord(value, json.RawMessage(`{}`)))
	if insertErr == nil && inserted {
		if commitErr := tx.Commit(); commitErr != nil {
			return actionmodel.ActionExecutionClaimResult{}, commitErr
		}
		r.store.ObserveIdempotency(ctx, value.WorkspaceID, "action.execute", idempotency.OutcomeAcquired)
		return actionmodel.ActionExecutionClaimResult{Decision: idempotency.DecisionAcquired, Execution: value}, nil
	}
	_ = tx.Rollback()
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
	status, empty := string(idempotency.StatusProcessing), ""
	resultJSON, metadataJSON := json.RawMessage(`{}`), json.RawMessage(actionExecutionMetadataJSON(0, value.RoleKey))
	changes := sharedoperation.RecordChanges{
		Status: &status, LeaseOwner: &value.LeaseOwner, LeaseExpiresAt: &value.LeaseExpiresAt, IncrementFencingToken: true,
		ResultJSON: &resultJSON, MetadataJSON: &metadataJSON, ErrorCode: &empty, FailureClass: &empty,
		FinishedAt: &empty, ExpiresAt: &empty, UpdatedAt: &value.UpdatedAt,
	}
	filter := sharedoperation.RecordFilter{
		WorkspaceID: value.WorkspaceID, ID: value.ID, Owner: actionExecutionOwner, Kind: actionExecutionKind,
		RequestFingerprint: value.RequestFingerprint, LeaseExpiresAtOrBefore: now.Format(time.RFC3339Nano),
		ReclaimableStatus: string(idempotency.StatusFailedRetryable), ExpiredLeaseStatus: string(idempotency.StatusProcessing),
	}
	changed, err := operationStore.PatchRecord(ctx, filter, changes)
	if err != nil {
		return actionmodel.ActionExecutionClaimResult{}, err
	}
	current, found, err = r.findExecutionByScope(ctx, value.WorkspaceID, value.ObjectKey, value.RecordID, value.ActionKey, value.IdempotencyKey)
	if err != nil || !found {
		return actionmodel.ActionExecutionClaimResult{}, err
	}
	if changed {
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
	leaseExpiresAt, updatedAt, token := expiresAt.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano), fencingToken
	changed, err := sharedoperation.NewSQLStore(r.db, r.store.SQLRenderer).PatchRecord(ctx, sharedoperation.RecordFilter{
		AllScopes: true, ID: strings.TrimSpace(executionID), Owner: actionExecutionOwner, Kind: actionExecutionKind,
		LeaseOwner: strings.TrimSpace(leaseOwner), FencingToken: &token, Status: string(idempotency.StatusProcessing),
	}, sharedoperation.RecordChanges{LeaseExpiresAt: &leaseExpiresAt, UpdatedAt: &updatedAt})
	return r.actionLeaseMutationResult(ctx, changed, err, executionID)
}

func (r ActionBusinessExecutionStore) CompleteExecution(ctx context.Context, completion actionmodel.ActionExecutionCompletion) (actionmodel.ActionBusinessExecution, error) {
	now := completion.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	resultJSON, err := database.MarshalTimeJSON(nonNilMap(completion.Result))
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
	filter, changes := actionExecutionCompletionPatch(completion, status, resultJSON, now)
	changed, err := sharedoperation.NewSQLStore(r.db, r.store.SQLRenderer).PatchRecord(ctx, filter, changes)
	if err := r.actionLeaseMutationResult(ctx, changed, err, completion.ExecutionID); err != nil {
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
	resultJSON, err := database.MarshalTimeJSON(nonNilMap(completion.Result))
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
	filter, changes := actionExecutionCompletionPatch(completion, status, resultJSON, now)
	changed, err := sharedoperation.NewSQLStore(t.owner.db, t.owner.store.SQLRenderer).PatchRecord(sharedoperation.WithExecutor(ctx, t.executor), filter, changes)
	if err != nil {
		return actionmodel.ActionBusinessExecution{}, database.MutationTransactionError(err, "business_action_execution", completion.ExecutionID)
	}
	if !changed {
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
	record, found, err := sharedoperation.NewSQLStore(r.db, r.store.SQLRenderer).GetRecord(ctx, sharedoperation.RecordFilter{
		WorkspaceID: workspaceID, ID: id, Owner: actionExecutionOwner, Kind: actionExecutionKind,
	})
	if err != nil || !found {
		return actionmodel.ActionBusinessExecution{}, found, err
	}
	value, err := actionBusinessExecution(record)
	return value, err == nil, err
}

func (r ActionBusinessExecutionStore) findExecutionByID(ctx context.Context, executionID string) (actionmodel.ActionBusinessExecution, error) {
	record, found, err := sharedoperation.NewSQLStore(r.db, r.store.SQLRenderer).GetRecord(ctx, sharedoperation.RecordFilter{
		AllScopes: true, ID: strings.TrimSpace(executionID), Owner: actionExecutionOwner, Kind: actionExecutionKind,
	})
	if err != nil {
		return actionmodel.ActionBusinessExecution{}, err
	}
	if !found {
		return actionmodel.ActionBusinessExecution{}, sql.ErrNoRows
	}
	return actionBusinessExecution(record)
}

func actionExecutionCompletionPatch(completion actionmodel.ActionExecutionCompletion, status idempotency.Status, resultJSON json.RawMessage, now time.Time) (sharedoperation.RecordFilter, sharedoperation.RecordChanges) {
	failureClass := ""
	if status == idempotency.StatusFailedRetryable {
		failureClass = "retryable"
	} else if status == idempotency.StatusFailedTerminal {
		failureClass = "terminal"
	}
	statusValue, metadataJSON := string(status), json.RawMessage(actionExecutionMetadataJSON(completion.ResponseStatus, completion.Execution.RoleKey))
	errorCode, expiresAt, finishedAt := strings.TrimSpace(completion.ErrorCode), completion.ExpiresAt.UTC().Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)
	token := completion.FencingToken
	return sharedoperation.RecordFilter{
			WorkspaceID: completion.Execution.WorkspaceID, ID: completion.ExecutionID, Owner: actionExecutionOwner, Kind: actionExecutionKind,
			LeaseOwner: strings.TrimSpace(completion.LeaseOwner), FencingToken: &token, Status: string(idempotency.StatusProcessing),
		}, sharedoperation.RecordChanges{
			Status: &statusValue, ResultJSON: &resultJSON, MetadataJSON: &metadataJSON, ErrorCode: &errorCode,
			FailureClass: &failureClass, ExpiresAt: &expiresAt, FinishedAt: &finishedAt, UpdatedAt: &finishedAt,
		}
}

func actionExecutionRecord(value actionmodel.ActionBusinessExecution, resultJSON json.RawMessage) sharedoperation.Record {
	failureClass := ""
	if value.Status == string(idempotency.StatusFailedRetryable) {
		failureClass = "retryable"
	} else if value.Status == string(idempotency.StatusFailedTerminal) {
		failureClass = "terminal"
	}
	return sharedoperation.Record{
		ID: value.ID, WorkspaceID: value.WorkspaceID, Owner: actionExecutionOwner, Kind: actionExecutionKind,
		ActionKey: value.ActionKey, ResourceType: value.ObjectKey, ResourceID: value.RecordID,
		IdempotencyKey: value.ID, RequestFingerprint: value.RequestFingerprint, RequestedBy: value.ActorID, Reference: value.IdempotencyKey,
		Status: value.Status, StatusURL: "/operations/" + value.ID, ResultJSON: resultJSON,
		MetadataJSON: json.RawMessage(actionExecutionMetadataJSON(value.ResponseStatus, value.RoleKey)), ErrorCode: value.ErrorCode,
		FailureClass: failureClass, RelatedIDsJSON: json.RawMessage(`[]`), Correlation: value.ID, EvidenceJSON: json.RawMessage(`[]`),
		LeaseOwner: value.LeaseOwner, LeaseExpiresAt: value.LeaseExpiresAt, FencingToken: value.FencingToken, ExpiresAt: value.ExpiresAt,
		CreatedAt: value.CreatedAt, StartedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

type actionExecutionOperationMetadata struct {
	ResponseStatus int    `json:"response_status"`
	RoleKey        string `json:"role_key,omitempty"`
}

func actionExecutionMetadataJSON(responseStatus int, roleKey string) string {
	encoded, _ := database.MarshalTimeJSON(actionExecutionOperationMetadata{ResponseStatus: responseStatus, RoleKey: strings.TrimSpace(roleKey)})
	return string(encoded)
}

func actionExecutionLease(value actionmodel.ActionBusinessExecution) idempotency.Lease {
	expiresAt, _ := time.Parse(time.RFC3339Nano, value.LeaseExpiresAt)
	return idempotency.Lease{Owner: value.LeaseOwner, Token: value.FencingToken, ExpiresAt: expiresAt}
}

func (r ActionBusinessExecutionStore) actionLeaseMutationResult(ctx context.Context, changed bool, err error, executionID string) error {
	if err != nil {
		return err
	}
	if !changed {
		if execution, loadErr := r.findExecutionByID(ctx, executionID); loadErr == nil {
			r.store.ObserveIdempotency(ctx, execution.WorkspaceID, "action.execute", idempotency.OutcomeLeaseLost)
		}
		return mutation.MutationConflict("business_action_execution", executionID, mutation.MutationConflictLeaseLost, nil)
	}
	return nil
}

func actionBusinessExecution(record sharedoperation.Record) (actionmodel.ActionBusinessExecution, error) {
	execution := actionmodel.ActionBusinessExecution{
		ID: record.ID, WorkspaceID: record.WorkspaceID, ActionKey: record.ActionKey, ObjectKey: record.ResourceType, RecordID: record.ResourceID,
		RequestFingerprint: record.RequestFingerprint, ActorID: record.RequestedBy, IdempotencyKey: record.Reference, Status: record.Status,
		ErrorCode: record.ErrorCode, LeaseOwner: record.LeaseOwner, LeaseExpiresAt: record.LeaseExpiresAt, FencingToken: record.FencingToken,
		ExpiresAt: record.ExpiresAt, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
	if record.Owner != actionExecutionOwner || record.Kind != actionExecutionKind || record.IdempotencyKey != execution.ID || record.Correlation != execution.ID {
		return actionmodel.ActionBusinessExecution{}, fmt.Errorf("action execution operation identity is invalid")
	}
	var metadata actionExecutionOperationMetadata
	if err := database.UnmarshalTimeJSON(record.MetadataJSON, &metadata); err != nil {
		return actionmodel.ActionBusinessExecution{}, fmt.Errorf("decode action execution metadata: %w", err)
	}
	execution.ResponseStatus, execution.RoleKey = metadata.ResponseStatus, metadata.RoleKey
	_ = database.UnmarshalTimeJSON(record.ResultJSON, &execution.Result)
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
	return strings.TrimSpace(value)
}

func nonNilMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}
