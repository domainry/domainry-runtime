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
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func (r WorkflowWorkerStore) TryBeginExecution(ctx context.Context, request workflowmodel.WorkflowExecutionClaimRequest) (workflowmodel.WorkflowExecutionClaimResult, error) {
	for attempt := 0; attempt < 50; attempt++ {
		claim, err := r.tryBeginExecutionOnce(ctx, request)
		if err == nil || !workflowSQLiteBusyError(r.store.Driver(), err) {
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
	result, err := r.database().ExecContext(ctx, "UPDATE "+r.store.TableIdentifier("workflow_execution_receipts")+" SET "+r.store.Identifier("status")+" = "+r.store.Placeholder(1)+", "+r.store.Identifier("lease_owner")+" = "+r.store.Placeholder(2)+", "+r.store.Identifier("lease_expires_at")+" = "+r.store.Placeholder(3)+", "+r.store.Identifier("fencing_token")+" = "+r.store.Identifier("fencing_token")+" + 1, "+r.store.Identifier("updated_at")+" = "+r.store.Placeholder(4)+" WHERE "+r.store.Identifier("id")+" = "+r.store.Placeholder(5)+" AND "+r.store.Identifier("request_fingerprint")+" = "+r.store.Placeholder(6)+" AND "+r.store.Identifier("status")+" = "+r.store.Placeholder(7)+" AND "+r.store.Identifier("lease_expires_at")+" <= "+r.store.Placeholder(8), string(idempotency.StatusProcessing), receipt.LeaseOwner, receipt.LeaseExpiresAt, receipt.UpdatedAt, receipt.ID, receipt.RequestFingerprint, string(idempotency.StatusProcessing), now.Format(time.RFC3339Nano))
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
	now := completion.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	result, err := r.database().ExecContext(ctx, "UPDATE "+r.store.TableIdentifier("workflow_execution_receipts")+" SET "+r.store.Identifier("status")+" = "+r.store.Placeholder(1)+", "+r.store.Identifier("execution_id")+" = "+r.store.Placeholder(2)+", "+r.store.Identifier("expires_at")+" = "+r.store.Placeholder(3)+", "+r.store.Identifier("updated_at")+" = "+r.store.Placeholder(4)+" WHERE "+r.store.Identifier("id")+" = "+r.store.Placeholder(5)+" AND "+r.store.Identifier("lease_owner")+" = "+r.store.Placeholder(6)+" AND "+r.store.Identifier("fencing_token")+" = "+r.store.Placeholder(7)+" AND "+r.store.Identifier("status")+" = "+r.store.Placeholder(8), string(idempotency.StatusSucceeded), strings.TrimSpace(completion.ExecutionID), completion.ExpiresAt.UTC().Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), completion.ReceiptID, strings.TrimSpace(completion.LeaseOwner), completion.FencingToken, string(idempotency.StatusProcessing))
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		if workspaceID, loadErr := r.workflowReceiptWorkspaceByID(ctx, completion.ReceiptID); loadErr == nil {
			r.store.ObserveIdempotency(ctx, workspaceID, "workflow.execute", idempotency.OutcomeLeaseLost)
		}
		return mutation.MutationConflict("workflow_execution_receipt", completion.ReceiptID, mutation.MutationConflictLeaseLost, nil)
	}
	return nil
}

func (r WorkflowWorkerStore) workflowReceiptWorkspaceByID(ctx context.Context, receiptID string) (string, error) {
	var workspaceID string
	err := r.database().QueryRowContext(ctx, "SELECT "+r.store.Identifier("workspace_id")+" FROM "+r.store.TableIdentifier("workflow_execution_receipts")+" WHERE "+r.store.Identifier("id")+" = "+r.store.Placeholder(1), receiptID).Scan(&workspaceID)
	return workspaceID, err
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

func workflowSQLiteBusyError(driver string, err error) bool {
	message := strings.ToLower(fmt.Sprint(err))
	return driver == "sqlite" && (strings.Contains(message, "sqlite_busy") || strings.Contains(message, "database is locked") || strings.Contains(message, "database table is locked"))
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
