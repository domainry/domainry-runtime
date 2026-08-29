// Business change-plan persistence.
package changeplan

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
	ormbuilder "github.com/domainry/domainry-orm/builder"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type BusinessChangePlanStore struct {
	store *database.RuntimeStore
	db    *sql.DB
}

func NewBusinessChangePlanStore(store *database.RuntimeStore) BusinessChangePlanStore {
	return BusinessChangePlanStore{store: store, db: store.DB()}
}

func transactionOptions() *sql.TxOptions {
	return &sql.TxOptions{Isolation: sql.LevelSerializable}
}

func changePlanWorkspaceID(value string) (string, error) {
	workspace, err := principalmodel.NewWorkspaceID(value)
	if err != nil {
		return "", err
	}
	return workspace.String(), nil
}

func (r BusinessChangePlanStore) GetDraft(ctx context.Context, workspaceID, id string) (changeplanmodel.BusinessChangePlanDraft, bool, error) {
	workspaceID, err := changePlanWorkspaceID(workspaceID)
	if err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, false, err
	}
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "business_change_plan_drafts", workspaceID).Columns("workspace_id", "plan_id", "revision", "status", "payload_json", "created_by", "updated_by", "created_at", "updated_at").Where(ormbuilder.Equal("plan_id", id)).Build()
	if buildErr != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, false, buildErr
	}
	var value changeplanmodel.BusinessChangePlanDraft
	var payload string
	err = r.db.QueryRowContext(ctx, query, args...).Scan(&value.WorkspaceID, &value.PlanID, &value.Revision, &value.Status, &payload, &value.CreatedBy, &value.UpdatedBy, &value.CreatedAt, &value.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return changeplanmodel.BusinessChangePlanDraft{}, false, nil
	}
	if err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, false, fmt.Errorf("get domain change plan draft: %w", err)
	}
	value.Payload = []byte(payload)
	return value, true, nil
}

func (r BusinessChangePlanStore) SaveDraft(ctx context.Context, workspaceID string, value changeplanmodel.BusinessChangePlanDraft, expected int) (changeplanmodel.BusinessChangePlanDraft, bool, error) {
	workspaceID, err := changePlanWorkspaceID(workspaceID)
	if err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, false, err
	}
	if strings.TrimSpace(value.WorkspaceID) != "" && strings.TrimSpace(value.WorkspaceID) != workspaceID {
		return changeplanmodel.BusinessChangePlanDraft{}, false, fmt.Errorf("change plan draft workspace does not match repository workspace")
	}
	value.WorkspaceID = workspaceID
	if expected == 0 {
		query, args, buildErr := ormbuilder.NewWorkspaceInsertBuilder(r.store.SQLRenderer, "business_change_plan_drafts", workspaceID).Columns("plan_id", "revision", "status", "payload_json", "created_by", "updated_by", "created_at", "updated_at").Values(value.PlanID, 1, "draft", string(value.Payload), value.CreatedBy, value.UpdatedBy, value.CreatedAt, value.UpdatedAt).Build()
		if buildErr != nil {
			return changeplanmodel.BusinessChangePlanDraft{}, false, buildErr
		}
		if _, err := r.db.ExecContext(ctx, query, args...); err != nil {
			return changeplanmodel.BusinessChangePlanDraft{}, false, fmt.Errorf("create domain change plan draft: %w", err)
		}
		return r.GetDraft(ctx, workspaceID, value.PlanID)
	}
	query, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "business_change_plan_drafts", workspaceID).Set("payload_json", string(value.Payload)).Set("updated_by", value.UpdatedBy).Set("updated_at", value.UpdatedAt).SetExpression("revision", ormbuilder.Add(ormbuilder.Column("revision"), ormbuilder.Value(1))).Where(ormbuilder.And(ormbuilder.Equal("plan_id", value.PlanID), ormbuilder.Equal("revision", expected), ormbuilder.Equal("status", "draft"))).Build()
	if buildErr != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, false, buildErr
	}
	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, false, fmt.Errorf("save domain change plan draft: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, false, fmt.Errorf("read saved domain change plan draft rows: %w", err)
	}
	if n != 1 {
		return changeplanmodel.BusinessChangePlanDraft{}, false, nil
	}
	return r.GetDraft(ctx, workspaceID, value.PlanID)
}

func (r BusinessChangePlanStore) TransitionDraft(ctx context.Context, workspaceID, id string, expected int, fromStatus, toStatus, by, at string) (changeplanmodel.BusinessChangePlanDraft, bool, error) {
	workspaceID, err := changePlanWorkspaceID(workspaceID)
	if err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, false, err
	}
	query, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "business_change_plan_drafts", workspaceID).Set("status", strings.TrimSpace(toStatus)).Set("updated_by", strings.TrimSpace(by)).Set("updated_at", strings.TrimSpace(at)).SetExpression("revision", ormbuilder.Add(ormbuilder.Column("revision"), ormbuilder.Value(1))).Where(ormbuilder.And(ormbuilder.Equal("plan_id", strings.TrimSpace(id)), ormbuilder.Equal("revision", expected), ormbuilder.Equal("status", strings.TrimSpace(fromStatus)))).Build()
	if buildErr != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, false, buildErr
	}
	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, false, fmt.Errorf("transition domain change plan draft: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, false, fmt.Errorf("read transitioned domain change plan draft rows: %w", err)
	}
	if rows != 1 {
		return changeplanmodel.BusinessChangePlanDraft{}, false, nil
	}
	return r.GetDraft(ctx, workspaceID, id)
}

func (r BusinessChangePlanStore) PublishDraft(ctx context.Context, workspaceID, id string, expected int, by, at string) (changeplanmodel.BusinessChangePlanDraft, bool, error) {
	workspaceID, err := changePlanWorkspaceID(workspaceID)
	if err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, false, err
	}
	query, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "business_change_plan_drafts", workspaceID).Set("status", "published").Set("updated_by", by).Set("updated_at", at).Where(ormbuilder.And(ormbuilder.Equal("plan_id", id), ormbuilder.Equal("revision", expected), ormbuilder.Equal("status", "applying"))).Build()
	if buildErr != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, false, buildErr
	}
	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, false, fmt.Errorf("publish domain change plan draft: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return changeplanmodel.BusinessChangePlanDraft{}, false, fmt.Errorf("read published domain change plan draft rows: %w", err)
	}
	if n != 1 {
		return changeplanmodel.BusinessChangePlanDraft{}, false, nil
	}
	return r.GetDraft(ctx, workspaceID, id)
}

func (r BusinessChangePlanStore) TryBeginOperation(ctx context.Context, workspaceID string, request changeplanmodel.ChangePlanOperationClaimRequest) (changeplanmodel.ChangePlanOperationClaimResult, error) {
	workspaceID, err := changePlanWorkspaceID(workspaceID)
	if err != nil {
		return changeplanmodel.ChangePlanOperationClaimResult{}, err
	}
	if strings.TrimSpace(request.Execution.WorkspaceID) != workspaceID {
		return changeplanmodel.ChangePlanOperationClaimResult{}, fmt.Errorf("change plan operation workspace does not match repository workspace")
	}
	now := request.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if request.LeaseTTL <= 0 {
		request.LeaseTTL = 30 * time.Second
	}
	value := request.Execution
	value.WorkspaceID = workspaceID
	value.PlanID, value.Operation, value.IdempotencyKey = strings.TrimSpace(value.PlanID), strings.TrimSpace(value.Operation), strings.TrimSpace(value.IdempotencyKey)
	value.ID = changePlanOperationID(value)
	value.RequestFingerprint, value.Status = strings.TrimSpace(request.RequestFingerprint), string(idempotency.StatusProcessing)
	value.LeaseOwner, value.LeaseExpiresAt, value.FencingToken = strings.TrimSpace(request.LeaseOwner), now.Add(request.LeaseTTL).Format(time.RFC3339Nano), 1
	value.CreatedAt, value.UpdatedAt = now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)
	columns := changePlanOperationColumns()
	values := changePlanOperationValues(value, "{}")
	insertColumns := append(append([]string{}, columns[:1]...), columns[2:]...)
	insertValues := append(append([]any{}, values[:1]...), values[2:]...)
	insert, insertArgs, buildErr := ormbuilder.NewWorkspaceInsertBuilder(r.store.SQLRenderer, "business_change_plan_operations", workspaceID).Columns(insertColumns...).Values(insertValues...).Build()
	if buildErr != nil {
		return changeplanmodel.ChangePlanOperationClaimResult{}, buildErr
	}
	_, insertErr := r.db.ExecContext(ctx, insert, insertArgs...)
	if insertErr == nil {
		r.store.ObserveIdempotency(ctx, value.WorkspaceID, "change_plan."+value.Operation, idempotency.OutcomeAcquired)
		return changeplanmodel.ChangePlanOperationClaimResult{Decision: idempotency.DecisionAcquired, Execution: value}, nil
	}
	current, found, err := r.findOperation(ctx, value)
	if err != nil {
		return changeplanmodel.ChangePlanOperationClaimResult{}, err
	}
	if !found {
		return changeplanmodel.ChangePlanOperationClaimResult{}, database.MutationConstraintError(insertErr, "business_change_plan_operation", value.ID, mutation.MutationConflictIdempotency)
	}
	decision := idempotency.Classify(idempotency.ReceiptState{Status: idempotency.Status(current.Status), Fingerprint: current.RequestFingerprint, Lease: changePlanOperationLease(current)}, value.RequestFingerprint, now)
	if decision != idempotency.DecisionAcquired {
		r.store.ObserveIdempotency(ctx, value.WorkspaceID, "change_plan."+value.Operation, idempotency.OutcomeForDecision(decision, false))
		return changeplanmodel.ChangePlanOperationClaimResult{Decision: decision, Execution: current}, nil
	}
	query, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "business_change_plan_operations", workspaceID).Set("status", string(idempotency.StatusProcessing)).Set("lease_owner", value.LeaseOwner).Set("lease_expires_at", value.LeaseExpiresAt).SetExpression("fencing_token", ormbuilder.Add(ormbuilder.Column("fencing_token"), ormbuilder.Value(1))).Set("updated_at", value.UpdatedAt).Where(ormbuilder.And(ormbuilder.Equal("id", value.ID), ormbuilder.Equal("request_fingerprint", value.RequestFingerprint), ormbuilder.Or(ormbuilder.And(ormbuilder.Equal("status", string(idempotency.StatusProcessing)), ormbuilder.LessThanOrEqual("lease_expires_at", now.Format(time.RFC3339Nano))), ormbuilder.Equal("status", string(idempotency.StatusFailedRetryable))))).Build()
	if buildErr != nil {
		return changeplanmodel.ChangePlanOperationClaimResult{}, buildErr
	}
	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return changeplanmodel.ChangePlanOperationClaimResult{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return changeplanmodel.ChangePlanOperationClaimResult{}, err
	}
	current, found, err = r.findOperation(ctx, value)
	if err != nil || !found {
		return changeplanmodel.ChangePlanOperationClaimResult{}, err
	}
	if rows == 1 {
		r.store.ObserveIdempotency(ctx, value.WorkspaceID, "change_plan."+value.Operation, idempotency.OutcomeReclaimed)
		return changeplanmodel.ChangePlanOperationClaimResult{Decision: idempotency.DecisionAcquired, Execution: current}, nil
	}
	r.store.ObserveIdempotency(ctx, value.WorkspaceID, "change_plan."+value.Operation, idempotency.OutcomeInProgress)
	return changeplanmodel.ChangePlanOperationClaimResult{Decision: idempotency.DecisionInProgress, Execution: current}, nil
}

func (r BusinessChangePlanStore) CompleteOperation(ctx context.Context, workspaceID string, completion changeplanmodel.ChangePlanOperationCompletion) (changeplanmodel.ChangePlanOperationExecution, error) {
	workspaceID, err := changePlanWorkspaceID(workspaceID)
	if err != nil {
		return changeplanmodel.ChangePlanOperationExecution{}, err
	}
	resultJSON, err := json.Marshal(completion.Result)
	if err != nil {
		return changeplanmodel.ChangePlanOperationExecution{}, err
	}
	now := completion.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	query, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "business_change_plan_operations", workspaceID).Set("status", string(idempotency.StatusSucceeded)).Set("result_json", string(resultJSON)).Set("expires_at", completion.ExpiresAt.UTC().Format(time.RFC3339Nano)).Set("updated_at", now.Format(time.RFC3339Nano)).Where(changePlanLeasePredicate(completion.ExecutionID, completion.LeaseOwner, completion.FencingToken)).Build()
	if buildErr != nil {
		return changeplanmodel.ChangePlanOperationExecution{}, buildErr
	}
	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return changeplanmodel.ChangePlanOperationExecution{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return changeplanmodel.ChangePlanOperationExecution{}, err
	}
	if rows != 1 {
		r.observeChangePlanLeaseLost(ctx, workspaceID, completion.ExecutionID)
		return changeplanmodel.ChangePlanOperationExecution{}, mutation.MutationConflict("change_plan_operation", completion.ExecutionID, mutation.MutationConflictLeaseLost, nil)
	}
	return r.findOperationByID(ctx, workspaceID, completion.ExecutionID)
}

func (r BusinessChangePlanStore) FailOperation(ctx context.Context, workspaceID string, failure changeplanmodel.ChangePlanOperationFailure) (changeplanmodel.ChangePlanOperationExecution, error) {
	workspaceID, err := changePlanWorkspaceID(workspaceID)
	if err != nil {
		return changeplanmodel.ChangePlanOperationExecution{}, err
	}
	resultJSON, err := json.Marshal(failure.Result)
	if err != nil {
		return changeplanmodel.ChangePlanOperationExecution{}, err
	}
	now := failure.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	status := idempotency.StatusFailedTerminal
	if failure.Retryable {
		status = idempotency.StatusFailedRetryable
	}
	query, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "business_change_plan_operations", workspaceID).Set("status", string(status)).Set("result_json", string(resultJSON)).Set("error_code", strings.TrimSpace(failure.ErrorCode)).Set("expires_at", failure.ExpiresAt.UTC().Format(time.RFC3339Nano)).Set("updated_at", now.Format(time.RFC3339Nano)).Where(changePlanLeasePredicate(failure.ExecutionID, failure.LeaseOwner, failure.FencingToken)).Build()
	if buildErr != nil {
		return changeplanmodel.ChangePlanOperationExecution{}, buildErr
	}
	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return changeplanmodel.ChangePlanOperationExecution{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return changeplanmodel.ChangePlanOperationExecution{}, err
	}
	if rows != 1 {
		r.observeChangePlanLeaseLost(ctx, workspaceID, failure.ExecutionID)
		return changeplanmodel.ChangePlanOperationExecution{}, mutation.MutationConflict("change_plan_operation", failure.ExecutionID, mutation.MutationConflictLeaseLost, nil)
	}
	return r.findOperationByID(ctx, workspaceID, failure.ExecutionID)
}

func (r BusinessChangePlanStore) observeChangePlanLeaseLost(ctx context.Context, workspaceID, executionID string) {
	if execution, err := r.findOperationByID(ctx, workspaceID, executionID); err == nil {
		r.store.ObserveIdempotency(ctx, execution.WorkspaceID, "change_plan."+execution.Operation, idempotency.OutcomeLeaseLost)
	}
}

func (r BusinessChangePlanStore) findOperation(ctx context.Context, scope changeplanmodel.ChangePlanOperationExecution) (changeplanmodel.ChangePlanOperationExecution, bool, error) {
	query, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "business_change_plan_operations", scope.WorkspaceID).Columns(changePlanOperationColumns()...).Where(ormbuilder.And(ormbuilder.Equal("plan_id", scope.PlanID), ormbuilder.Equal("plan_revision", scope.PlanRevision), ormbuilder.Equal("operation", scope.Operation), ormbuilder.Equal("idempotency_key", scope.IdempotencyKey))).Build()
	if buildErr != nil {
		return changeplanmodel.ChangePlanOperationExecution{}, false, buildErr
	}
	value, err := scanChangePlanOperation(r.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return changeplanmodel.ChangePlanOperationExecution{}, false, nil
	}
	return value, err == nil, err
}

func (r BusinessChangePlanStore) findOperationByID(ctx context.Context, workspaceID, id string) (changeplanmodel.ChangePlanOperationExecution, error) {
	query, args, err := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "business_change_plan_operations", workspaceID).Columns(changePlanOperationColumns()...).Where(ormbuilder.Equal("id", id)).Build()
	if err != nil {
		return changeplanmodel.ChangePlanOperationExecution{}, err
	}
	return scanChangePlanOperation(r.db.QueryRowContext(ctx, query, args...))
}

func changePlanLeasePredicate(id, owner string, token int64) ormbuilder.Predicate {
	return ormbuilder.And(ormbuilder.Equal("id", strings.TrimSpace(id)), ormbuilder.Equal("lease_owner", strings.TrimSpace(owner)), ormbuilder.Equal("fencing_token", token), ormbuilder.Equal("status", string(idempotency.StatusProcessing)))
}

func changePlanOperationColumns() []string {
	return []string{"id", "workspace_id", "plan_id", "plan_revision", "operation", "idempotency_key", "request_fingerprint", "status", "result_json", "lease_owner", "lease_expires_at", "fencing_token", "error_code", "expires_at", "actor_id", "created_at", "updated_at"}
}

func changePlanOperationValues(value changeplanmodel.ChangePlanOperationExecution, resultJSON string) []any {
	return []any{value.ID, value.WorkspaceID, value.PlanID, value.PlanRevision, value.Operation, value.IdempotencyKey, value.RequestFingerprint, value.Status, resultJSON, value.LeaseOwner, value.LeaseExpiresAt, value.FencingToken, value.ErrorCode, value.ExpiresAt, value.ActorID, value.CreatedAt, value.UpdatedAt}
}

type changePlanOperationScanner interface{ Scan(...any) error }

func scanChangePlanOperation(row changePlanOperationScanner) (changeplanmodel.ChangePlanOperationExecution, error) {
	var value changeplanmodel.ChangePlanOperationExecution
	var resultJSON string
	if err := row.Scan(&value.ID, &value.WorkspaceID, &value.PlanID, &value.PlanRevision, &value.Operation, &value.IdempotencyKey, &value.RequestFingerprint, &value.Status, &resultJSON, &value.LeaseOwner, &value.LeaseExpiresAt, &value.FencingToken, &value.ErrorCode, &value.ExpiresAt, &value.ActorID, &value.CreatedAt, &value.UpdatedAt); err != nil {
		return changeplanmodel.ChangePlanOperationExecution{}, err
	}
	value.Result = json.RawMessage(resultJSON)
	return value, nil
}

func changePlanOperationID(value changeplanmodel.ChangePlanOperationExecution) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{strings.TrimSpace(value.WorkspaceID), strings.TrimSpace(value.PlanID), fmt.Sprint(value.PlanRevision), strings.TrimSpace(value.Operation), strings.TrimSpace(value.IdempotencyKey)}, ":")))
	return "change_plan_operation:" + hex.EncodeToString(sum[:])[:20]
}

func changePlanOperationLease(value changeplanmodel.ChangePlanOperationExecution) idempotency.Lease {
	expiresAt, _ := time.Parse(time.RFC3339Nano, value.LeaseExpiresAt)
	return idempotency.Lease{Owner: value.LeaseOwner, Token: value.FencingToken, ExpiresAt: expiresAt}
}
