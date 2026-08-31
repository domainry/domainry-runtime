package workflow

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
	"github.com/domainry/domainry-orm/query"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func (r WorkflowWorkerStore) TryBeginExecution(ctx context.Context, request workflowmodel.WorkflowExecutionClaimRequest) (workflowmodel.WorkflowExecutionClaimResult, error) {
	for attempt := 0; attempt < 50; attempt++ {
		claim, err := r.tryBeginExecutionOnce(ctx, request)
		if err == nil || !r.store.IsTransientError(err) {
			return claim, err
		}
		if err := r.waitForClaimRetry(ctx, attempt); err != nil {
			return workflowmodel.WorkflowExecutionClaimResult{}, err
		}
	}
	return workflowmodel.WorkflowExecutionClaimResult{}, fmt.Errorf("claim workflow execution: sqlite remained busy after retry")
}

func (r WorkflowWorkerStore) tryBeginExecutionOnce(ctx context.Context, request workflowmodel.WorkflowExecutionClaimRequest) (workflowmodel.WorkflowExecutionClaimResult, error) {
	now := request.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if request.LeaseTTL <= 0 {
		request.LeaseTTL = 5 * time.Minute
	}
	receipt := request.Receipt
	workspaceID, err := requireWorkflowWorkspaceID(receipt.WorkspaceID)
	if err != nil {
		return workflowmodel.WorkflowExecutionClaimResult{}, err
	}
	receipt.WorkspaceID = workspaceID
	receipt.WorkflowKey, receipt.IdempotencyKey = strings.TrimSpace(receipt.WorkflowKey), strings.TrimSpace(receipt.IdempotencyKey)
	receipt.RequestFingerprint, receipt.LeaseOwner = strings.TrimSpace(request.RequestFingerprint), strings.TrimSpace(request.LeaseOwner)
	receipt.ID = workflowReceiptID(receipt.WorkspaceID, receipt.WorkflowKey, receipt.IdempotencyKey)
	receipt.Status, receipt.FencingToken = string(idempotency.StatusProcessing), 1
	receipt.LeaseExpiresAt = now.Add(request.LeaseTTL).Format(time.RFC3339Nano)
	receipt.CreatedAt, receipt.UpdatedAt = now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)
	columns, values := workflowReceiptColumns(), workflowReceiptValues(receipt)
	statement, args, buildErr := query.NewWorkspaceInsertBuilder(r.store.SQLRenderer, "_workflow_execution_receipts", workspaceID).
		Columns(append(columns[:1], columns[2:]...)...).Values(append(values[:1], values[2:]...)...).Build()
	if buildErr != nil {
		return workflowmodel.WorkflowExecutionClaimResult{}, fmt.Errorf("build workflow execution receipt insert: %w", buildErr)
	}
	_, insertErr := r.database().ExecContext(ctx, statement, args...)
	if insertErr == nil {
		r.store.ObserveIdempotency(ctx, receipt.WorkspaceID, "workflow.execute", idempotency.OutcomeAcquired)
		return workflowmodel.WorkflowExecutionClaimResult{Decision: idempotency.DecisionAcquired, Receipt: receipt}, nil
	}
	current, found, err := r.findExecutionReceipt(ctx, receipt.WorkspaceID, receipt.WorkflowKey, receipt.IdempotencyKey)
	if err != nil {
		return workflowmodel.WorkflowExecutionClaimResult{}, err
	}
	if !found {
		return workflowmodel.WorkflowExecutionClaimResult{}, database.MutationConstraintError(insertErr, "workflow_execution_receipt", receipt.ID, mutation.MutationConflictIdempotency)
	}
	decision := idempotency.Classify(idempotency.ReceiptState{Status: idempotency.Status(current.Status), Fingerprint: current.RequestFingerprint, Lease: workflowReceiptLease(current)}, receipt.RequestFingerprint, now)
	if decision != idempotency.DecisionAcquired {
		r.store.ObserveIdempotency(ctx, receipt.WorkspaceID, "workflow.execute", idempotency.OutcomeForDecision(decision, false))
		return workflowmodel.WorkflowExecutionClaimResult{Decision: decision, Receipt: current}, nil
	}
	statement, args, buildErr = query.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "_workflow_execution_receipts", receipt.WorkspaceID).
		Set("status", string(idempotency.StatusProcessing)).
		Set("lease_owner", receipt.LeaseOwner).
		Set("lease_expires_at", receipt.LeaseExpiresAt).
		SetExpression("fencing_token", query.Add(query.Column("fencing_token"), query.Value(1))).
		Set("updated_at", receipt.UpdatedAt).
		Where(query.And(
			query.Equal("id", receipt.ID),
			query.Equal("request_fingerprint", receipt.RequestFingerprint),
			query.Equal("status", string(idempotency.StatusProcessing)),
			query.LessThanOrEqual("lease_expires_at", now.Format(time.RFC3339Nano)),
		)).Build()
	if buildErr != nil {
		return workflowmodel.WorkflowExecutionClaimResult{}, fmt.Errorf("build workflow execution receipt reclaim: %w", buildErr)
	}
	result, err := r.database().ExecContext(ctx, statement, args...)
	if err != nil {
		return workflowmodel.WorkflowExecutionClaimResult{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return workflowmodel.WorkflowExecutionClaimResult{}, err
	}
	current, _, err = r.findExecutionReceipt(ctx, receipt.WorkspaceID, receipt.WorkflowKey, receipt.IdempotencyKey)
	if err != nil {
		return workflowmodel.WorkflowExecutionClaimResult{}, err
	}
	if rows == 1 {
		r.store.ObserveIdempotency(ctx, receipt.WorkspaceID, "workflow.execute", idempotency.OutcomeReclaimed)
		return workflowmodel.WorkflowExecutionClaimResult{Decision: idempotency.DecisionAcquired, Receipt: current}, nil
	}
	r.store.ObserveIdempotency(ctx, receipt.WorkspaceID, "workflow.execute", idempotency.OutcomeInProgress)
	return workflowmodel.WorkflowExecutionClaimResult{Decision: idempotency.DecisionInProgress, Receipt: current}, nil
}

func (r WorkflowWorkerStore) CompleteExecutionReceipt(ctx context.Context, completion workflowmodel.WorkflowExecutionReceiptCompletion) error {
	workspaceID, err := requireWorkflowWorkspaceID(completion.WorkspaceID)
	if err != nil {
		return err
	}
	now := completion.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	statement, args, buildErr := query.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "_workflow_execution_receipts", workspaceID).
		Set("status", string(idempotency.StatusSucceeded)).
		Set("execution_id", strings.TrimSpace(completion.ExecutionID)).
		Set("expires_at", completion.ExpiresAt.UTC().Format(time.RFC3339Nano)).
		Set("updated_at", now.Format(time.RFC3339Nano)).
		Where(query.And(
			query.Equal("id", completion.ReceiptID),
			query.Equal("lease_owner", strings.TrimSpace(completion.LeaseOwner)),
			query.Equal("fencing_token", completion.FencingToken),
			query.Equal("status", string(idempotency.StatusProcessing)),
		)).Build()
	if buildErr != nil {
		return fmt.Errorf("build workflow execution receipt completion: %w", buildErr)
	}
	result, err := r.database().ExecContext(ctx, statement, args...)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		r.store.ObserveIdempotency(ctx, workspaceID, "workflow.execute", idempotency.OutcomeLeaseLost)
		return mutation.MutationConflict("workflow_execution_receipt", completion.ReceiptID, mutation.MutationConflictLeaseLost, nil)
	}
	return nil
}

func (r WorkflowWorkerStore) findExecutionReceipt(ctx context.Context, workspaceID, workflowKey, key string) (workflowmodel.WorkflowExecutionReceipt, bool, error) {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return workflowmodel.WorkflowExecutionReceipt{}, false, err
	}
	queryValue, args, err := query.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "_workflow_execution_receipts", workspaceID).
		Columns(workflowReceiptColumns()...).Where(query.And(query.Equal("workflow_key", strings.TrimSpace(workflowKey)), query.Equal("idempotency_key", strings.TrimSpace(key)))).Limit(1).Build()
	if err != nil {
		return workflowmodel.WorkflowExecutionReceipt{}, false, fmt.Errorf("build workflow execution receipt lookup: %w", err)
	}
	var value workflowmodel.WorkflowExecutionReceipt
	err = r.database().QueryRowContext(ctx, queryValue, args...).Scan(workflowReceiptScanTargets(&value)...)
	if errors.Is(err, sql.ErrNoRows) {
		return workflowmodel.WorkflowExecutionReceipt{}, false, nil
	}
	return value, err == nil, err
}

func workflowReceiptColumns() []string {
	return []string{"id", "workspace_id", "workflow_key", "idempotency_key", "request_fingerprint", "status", "execution_id", "lease_owner", "lease_expires_at", "fencing_token", "created_at", "updated_at", "expires_at"}
}

func workflowReceiptValues(value workflowmodel.WorkflowExecutionReceipt) []any {
	return []any{value.ID, value.WorkspaceID, value.WorkflowKey, value.IdempotencyKey, value.RequestFingerprint, value.Status, value.ExecutionID, value.LeaseOwner, value.LeaseExpiresAt, value.FencingToken, value.CreatedAt, value.UpdatedAt, value.ExpiresAt}
}

func workflowReceiptScanTargets(value *workflowmodel.WorkflowExecutionReceipt) []any {
	return []any{&value.ID, &value.WorkspaceID, &value.WorkflowKey, &value.IdempotencyKey, &value.RequestFingerprint, &value.Status, &value.ExecutionID, &value.LeaseOwner, &value.LeaseExpiresAt, &value.FencingToken, &value.CreatedAt, &value.UpdatedAt, &value.ExpiresAt}
}

func workflowReceiptLease(value workflowmodel.WorkflowExecutionReceipt) idempotency.Lease {
	expiresAt, _ := time.Parse(time.RFC3339Nano, value.LeaseExpiresAt)
	return idempotency.Lease{Owner: value.LeaseOwner, Token: value.FencingToken, ExpiresAt: expiresAt}
}

func workflowReceiptID(workspaceID, workflowKey, key string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{strings.TrimSpace(workspaceID), strings.TrimSpace(workflowKey), strings.TrimSpace(key)}, ":")))
	return "workflow_receipt:" + hex.EncodeToString(sum[:])[:20]
}

func workflowClaimBackoff(ctx context.Context, attempt int) error {
	timer := time.NewTimer(time.Duration(attempt+1) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
