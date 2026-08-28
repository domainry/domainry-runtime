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

func joinIdentifiers(store *database.RuntimeStore, columns ...string) string {
	values := make([]string, 0, len(columns))
	for _, column := range columns {
		values = append(values, store.Identifier(column))
	}
	return strings.Join(values, ", ")
}

func joinPlaceholders(store *database.RuntimeStore, count int) string {
	values := make([]string, 0, count)
	for index := 0; index < count; index++ {
		values = append(values, store.Placeholder(index+1))
	}
	return strings.Join(values, ", ")
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
	query := "SELECT " + joinIdentifiers(r.store, "workspace_id", "plan_id", "revision", "status", "payload_json", "created_by", "updated_by", "created_at", "updated_at") + " FROM " + r.store.TableIdentifier("business_change_plan_drafts") + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1) + " AND " + r.store.Identifier("plan_id") + " = " + r.store.Placeholder(2)
	var value changeplanmodel.BusinessChangePlanDraft
	var payload string
	err = r.db.QueryRowContext(ctx, query, workspaceID, id).Scan(&value.WorkspaceID, &value.PlanID, &value.Revision, &value.Status, &payload, &value.CreatedBy, &value.UpdatedBy, &value.CreatedAt, &value.UpdatedAt)
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
		columns := []string{"workspace_id", "plan_id", "revision", "status", "payload_json", "created_by", "updated_by", "created_at", "updated_at"}
		args := []any{workspaceID, value.PlanID, 1, "draft", string(value.Payload), value.CreatedBy, value.UpdatedBy, value.CreatedAt, value.UpdatedAt}
		if _, err := r.db.ExecContext(ctx, "INSERT INTO "+r.store.TableIdentifier("business_change_plan_drafts")+" ("+joinIdentifiers(r.store, columns...)+") VALUES ("+joinPlaceholders(r.store, len(args))+")", args...); err != nil {
			return changeplanmodel.BusinessChangePlanDraft{}, false, fmt.Errorf("create domain change plan draft: %w", err)
		}
		return r.GetDraft(ctx, workspaceID, value.PlanID)
	}
	query := "UPDATE " + r.store.TableIdentifier("business_change_plan_drafts") + " SET " + r.store.Identifier("payload_json") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("updated_by") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(3) + ", " + r.store.Identifier("revision") + " = " + r.store.Identifier("revision") + " + 1 WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(4) + " AND " + r.store.Identifier("plan_id") + " = " + r.store.Placeholder(5) + " AND " + r.store.Identifier("revision") + " = " + r.store.Placeholder(6) + " AND " + r.store.Identifier("status") + " = 'draft'"
	result, err := r.db.ExecContext(ctx, query, string(value.Payload), value.UpdatedBy, value.UpdatedAt, workspaceID, value.PlanID, expected)
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
	query := "UPDATE " + r.store.TableIdentifier("business_change_plan_drafts") + " SET " + r.store.Identifier("status") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("updated_by") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(3) + ", " + r.store.Identifier("revision") + " = " + r.store.Identifier("revision") + " + 1 WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(4) + " AND " + r.store.Identifier("plan_id") + " = " + r.store.Placeholder(5) + " AND " + r.store.Identifier("revision") + " = " + r.store.Placeholder(6) + " AND " + r.store.Identifier("status") + " = " + r.store.Placeholder(7)
	result, err := r.db.ExecContext(ctx, query, strings.TrimSpace(toStatus), strings.TrimSpace(by), strings.TrimSpace(at), workspaceID, strings.TrimSpace(id), expected, strings.TrimSpace(fromStatus))
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
	query := "UPDATE " + r.store.TableIdentifier("business_change_plan_drafts") + " SET " + r.store.Identifier("status") + " = 'published', " + r.store.Identifier("updated_by") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(2) + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(3) + " AND " + r.store.Identifier("plan_id") + " = " + r.store.Placeholder(4) + " AND " + r.store.Identifier("revision") + " = " + r.store.Placeholder(5) + " AND " + r.store.Identifier("status") + " = 'applying'"
	result, err := r.db.ExecContext(ctx, query, by, at, workspaceID, id, expected)
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
	_, insertErr := r.db.ExecContext(ctx, "INSERT INTO "+r.store.TableIdentifier("business_change_plan_operations")+" ("+joinIdentifiers(r.store, columns...)+") VALUES ("+joinPlaceholders(r.store, len(columns))+")", changePlanOperationValues(value, "{}")...)
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
	query := "UPDATE " + r.store.TableIdentifier("business_change_plan_operations") + " SET " + r.store.Identifier("status") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("lease_owner") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("lease_expires_at") + " = " + r.store.Placeholder(3) + ", " + r.store.Identifier("fencing_token") + " = " + r.store.Identifier("fencing_token") + " + 1, " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(4) + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(5) + " AND " + r.store.Identifier("id") + " = " + r.store.Placeholder(6) + " AND " + r.store.Identifier("request_fingerprint") + " = " + r.store.Placeholder(7) + " AND ((" + r.store.Identifier("status") + " = " + r.store.Placeholder(8) + " AND " + r.store.Identifier("lease_expires_at") + " <= " + r.store.Placeholder(9) + ") OR " + r.store.Identifier("status") + " = " + r.store.Placeholder(10) + ")"
	result, err := r.db.ExecContext(ctx, query, string(idempotency.StatusProcessing), value.LeaseOwner, value.LeaseExpiresAt, value.UpdatedAt, workspaceID, value.ID, value.RequestFingerprint, string(idempotency.StatusProcessing), now.Format(time.RFC3339Nano), string(idempotency.StatusFailedRetryable))
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
	query := "UPDATE " + r.store.TableIdentifier("business_change_plan_operations") + " SET " + r.store.Identifier("status") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("result_json") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("expires_at") + " = " + r.store.Placeholder(3) + ", " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(4) + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(5) + " AND " + r.store.Identifier("id") + " = " + r.store.Placeholder(6) + " AND " + r.store.Identifier("lease_owner") + " = " + r.store.Placeholder(7) + " AND " + r.store.Identifier("fencing_token") + " = " + r.store.Placeholder(8) + " AND " + r.store.Identifier("status") + " = " + r.store.Placeholder(9)
	result, err := r.db.ExecContext(ctx, query, string(idempotency.StatusSucceeded), string(resultJSON), completion.ExpiresAt.UTC().Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), workspaceID, completion.ExecutionID, strings.TrimSpace(completion.LeaseOwner), completion.FencingToken, string(idempotency.StatusProcessing))
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
	query := "UPDATE " + r.store.TableIdentifier("business_change_plan_operations") + " SET " + r.store.Identifier("status") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("result_json") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("error_code") + " = " + r.store.Placeholder(3) + ", " + r.store.Identifier("expires_at") + " = " + r.store.Placeholder(4) + ", " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(5) + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(6) + " AND " + r.store.Identifier("id") + " = " + r.store.Placeholder(7) + " AND " + r.store.Identifier("lease_owner") + " = " + r.store.Placeholder(8) + " AND " + r.store.Identifier("fencing_token") + " = " + r.store.Placeholder(9) + " AND " + r.store.Identifier("status") + " = " + r.store.Placeholder(10)
	result, err := r.db.ExecContext(ctx, query, string(status), string(resultJSON), strings.TrimSpace(failure.ErrorCode), failure.ExpiresAt.UTC().Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), workspaceID, failure.ExecutionID, strings.TrimSpace(failure.LeaseOwner), failure.FencingToken, string(idempotency.StatusProcessing))
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
	query := "SELECT " + joinIdentifiers(r.store, changePlanOperationColumns()...) + " FROM " + r.store.TableIdentifier("business_change_plan_operations") + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1) + " AND " + r.store.Identifier("plan_id") + " = " + r.store.Placeholder(2) + " AND " + r.store.Identifier("plan_revision") + " = " + r.store.Placeholder(3) + " AND " + r.store.Identifier("operation") + " = " + r.store.Placeholder(4) + " AND " + r.store.Identifier("idempotency_key") + " = " + r.store.Placeholder(5)
	value, err := scanChangePlanOperation(r.db.QueryRowContext(ctx, query, scope.WorkspaceID, scope.PlanID, scope.PlanRevision, scope.Operation, scope.IdempotencyKey))
	if errors.Is(err, sql.ErrNoRows) {
		return changeplanmodel.ChangePlanOperationExecution{}, false, nil
	}
	return value, err == nil, err
}

func (r BusinessChangePlanStore) findOperationByID(ctx context.Context, workspaceID, id string) (changeplanmodel.ChangePlanOperationExecution, error) {
	query := "SELECT " + joinIdentifiers(r.store, changePlanOperationColumns()...) + " FROM " + r.store.TableIdentifier("business_change_plan_operations") + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1) + " AND " + r.store.Identifier("id") + " = " + r.store.Placeholder(2)
	return scanChangePlanOperation(r.db.QueryRowContext(ctx, query, workspaceID, id))
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
