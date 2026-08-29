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
	ormbuilder "github.com/domainry/domainry-orm/builder"
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
	receipt.WorkspaceID = workflowReceiptWorkspace(receipt.WorkspaceID)
	receipt.WorkflowKey, receipt.IdempotencyKey = strings.TrimSpace(receipt.WorkflowKey), strings.TrimSpace(receipt.IdempotencyKey)
	receipt.RequestFingerprint, receipt.LeaseOwner = strings.TrimSpace(request.RequestFingerprint), strings.TrimSpace(request.LeaseOwner)
	receipt.ID = workflowReceiptID(receipt.WorkspaceID, receipt.WorkflowKey, receipt.IdempotencyKey)
	receipt.Status, receipt.FencingToken = string(idempotency.StatusProcessing), 1
	receipt.LeaseExpiresAt = now.Add(request.LeaseTTL).Format(time.RFC3339Nano)
	receipt.CreatedAt, receipt.UpdatedAt = now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano)
	_, insertErr := r.database().ExecContext(ctx, "INSERT INTO "+r.store.TableIdentifier("workflow_execution_receipts")+" ("+strings.Join(r.storeQuotedWorkflowReceiptColumns(), ", ")+") VALUES ("+strings.Join(r.storePlaceholders(len(workflowReceiptColumns())), ", ")+")", workflowReceiptValues(receipt)...)
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
	statement, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "workflow_execution_receipts", receipt.WorkspaceID).
		Set("status", string(idempotency.StatusProcessing)).
		Set("lease_owner", receipt.LeaseOwner).
		Set("lease_expires_at", receipt.LeaseExpiresAt).
		SetExpression("fencing_token", ormbuilder.Add(ormbuilder.Column("fencing_token"), ormbuilder.Value(1))).
		Set("updated_at", receipt.UpdatedAt).
		Where(ormbuilder.And(
			ormbuilder.Equal("id", receipt.ID),
			ormbuilder.Equal("request_fingerprint", receipt.RequestFingerprint),
			ormbuilder.Equal("status", string(idempotency.StatusProcessing)),
			ormbuilder.LessThanOrEqual("lease_expires_at", now.Format(time.RFC3339Nano)),
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
	statement, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "workflow_execution_receipts", workspaceID).
		Set("status", string(idempotency.StatusSucceeded)).
		Set("execution_id", strings.TrimSpace(completion.ExecutionID)).
		Set("expires_at", completion.ExpiresAt.UTC().Format(time.RFC3339Nano)).
		Set("updated_at", now.Format(time.RFC3339Nano)).
		Where(ormbuilder.And(
			ormbuilder.Equal("id", completion.ReceiptID),
			ormbuilder.Equal("lease_owner", strings.TrimSpace(completion.LeaseOwner)),
			ormbuilder.Equal("fencing_token", completion.FencingToken),
			ormbuilder.Equal("status", string(idempotency.StatusProcessing)),
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
	query := "SELECT " + strings.Join(r.storeQuotedWorkflowReceiptColumns(), ", ") + " FROM " + r.store.TableIdentifier("workflow_execution_receipts") + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1) + " AND " + r.store.Identifier("workflow_key") + " = " + r.store.Placeholder(2) + " AND " + r.store.Identifier("idempotency_key") + " = " + r.store.Placeholder(3) + " LIMIT 1"
	var value workflowmodel.WorkflowExecutionReceipt
	err := r.database().QueryRowContext(ctx, query, workflowReceiptWorkspace(workspaceID), strings.TrimSpace(workflowKey), strings.TrimSpace(key)).Scan(workflowReceiptScanTargets(&value)...)
	if errors.Is(err, sql.ErrNoRows) {
		return workflowmodel.WorkflowExecutionReceipt{}, false, nil
	}
	return value, err == nil, err
}

func (r WorkflowWorkerStore) storeQuotedWorkflowReceiptColumns() []string {
	columns := workflowReceiptColumns()
	for index := range columns {
		columns[index] = r.store.Identifier(columns[index])
	}
	return columns
}

func (r WorkflowWorkerStore) storePlaceholders(count int) []string {
	values := make([]string, count)
	for index := range values {
		values[index] = r.store.Placeholder(index + 1)
	}
	return values
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
	sum := sha256.Sum256([]byte(strings.Join([]string{workflowReceiptWorkspace(workspaceID), strings.TrimSpace(workflowKey), strings.TrimSpace(key)}, ":")))
	return "workflow_receipt:" + hex.EncodeToString(sum[:])[:20]
}

func workflowReceiptWorkspace(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return "default"
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
