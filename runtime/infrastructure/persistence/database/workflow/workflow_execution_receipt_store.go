package workflow

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
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

const (
	workflowReceiptOperationTable = "_operations"
	workflowReceiptOwner          = "workflow"
	workflowReceiptKind           = "workflow.execution.start"
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
	insertColumns, insertValues := append(columns[:1], columns[2:]...), append(values[:1], values[2:]...)
	insertBuilder, buildErr := r.store.SubjectEvidenceInsertBuilder(workspaceID, workflowReceiptOperationTable, insertColumns, insertValues)
	if buildErr != nil {
		return workflowmodel.WorkflowExecutionClaimResult{}, fmt.Errorf("build workflow execution receipt insert: %w", buildErr)
	}
	statement, args, buildErr := insertBuilder.Build()
	if buildErr != nil {
		return workflowmodel.WorkflowExecutionClaimResult{}, fmt.Errorf("build workflow execution receipt insert: %w", buildErr)
	}
	inserted, insertErr := r.database().ExecContext(ctx, statement, args...)
	if insertErr == nil {
		rows, rowsErr := inserted.RowsAffected()
		if rowsErr != nil {
			return workflowmodel.WorkflowExecutionClaimResult{}, rowsErr
		}
		if rows != 1 {
			return workflowmodel.WorkflowExecutionClaimResult{}, fmt.Errorf("runtime.subject_erased")
		}
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
	if request.PreventReclaim && decision == idempotency.DecisionAcquired {
		decision = idempotency.DecisionInProgress
	}
	if decision != idempotency.DecisionAcquired {
		r.store.ObserveIdempotency(ctx, receipt.WorkspaceID, "workflow.execute", idempotency.OutcomeForDecision(decision, false))
		return workflowmodel.WorkflowExecutionClaimResult{Decision: decision, Receipt: current}, nil
	}
	statement, args, buildErr = query.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, workflowReceiptOperationTable, receipt.WorkspaceID).
		Set("status", string(idempotency.StatusProcessing)).
		Set("lease_owner", receipt.LeaseOwner).
		Set("lease_expires_at", receipt.LeaseExpiresAt).
		SetExpression("fencing_token", query.Add(query.Column("fencing_token"), query.Value(1))).
		Set("result_json", "{}").Set("related_ids_json", "[]").Set("finished_at", "").Set("expires_at", "").
		Set("updated_at", receipt.UpdatedAt).
		Where(query.And(
			workflowReceiptOperationPredicate(receipt.ID),
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
	resultJSON := workflowReceiptResultJSON(strings.TrimSpace(completion.ExecutionID))
	relatedIDs := "[]"
	if strings.TrimSpace(completion.ExecutionID) != "" {
		related, _ := json.Marshal([]string{strings.TrimSpace(completion.ExecutionID)})
		relatedIDs = string(related)
	}
	statement, args, buildErr := query.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, workflowReceiptOperationTable, workspaceID).
		Set("status", string(idempotency.StatusSucceeded)).
		Set("result_json", resultJSON).
		Set("related_ids_json", relatedIDs).
		Set("expires_at", completion.ExpiresAt.UTC().Format(time.RFC3339Nano)).
		Set("finished_at", now.Format(time.RFC3339Nano)).
		Set("updated_at", now.Format(time.RFC3339Nano)).
		Where(query.And(
			workflowReceiptOperationPredicate(completion.ReceiptID),
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
	id := workflowReceiptID(workspaceID, workflowKey, key)
	queryValue, args, err := query.NewWorkspaceSelectBuilder(r.store.SQLRenderer, workflowReceiptOperationTable, workspaceID).
		Columns(workflowReceiptColumns()...).Where(workflowReceiptOperationPredicate(id)).Limit(1).Build()
	if err != nil {
		return workflowmodel.WorkflowExecutionReceipt{}, false, fmt.Errorf("build workflow execution receipt lookup: %w", err)
	}
	value, err := scanWorkflowReceipt(r.database().QueryRowContext(ctx, queryValue, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return workflowmodel.WorkflowExecutionReceipt{}, false, nil
	}
	return value, err == nil, err
}

// FindExecutionReceipt only reads a scoped receipt. Unlike TryBeginExecution,
// it neither creates a claim nor changes an expired lease.
func (r WorkflowWorkerStore) FindExecutionReceipt(ctx context.Context, workspaceID, workflowKey, key string) (workflowmodel.WorkflowExecutionReceipt, bool, error) {
	if strings.TrimSpace(workflowKey) == "" || strings.TrimSpace(key) == "" {
		return workflowmodel.WorkflowExecutionReceipt{}, false, fmt.Errorf("workflow and idempotency key are required")
	}
	return r.findExecutionReceipt(ctx, workspaceID, workflowKey, key)
}

func workflowReceiptColumns() []string {
	return []string{"id", "workspace_id", "owner", "kind", "action_key", "resource_type", "resource_id", "idempotency_key", "request_fingerprint", "requested_by", "reason", "reference", "status", "status_url", "result_json", "metadata_json", "error_code", "failure_class", "next_action", "related_ids_json", "correlation", "evidence_json", "lease_owner", "lease_expires_at", "fencing_token", "expires_at", "created_at", "started_at", "finished_at", "updated_at"}
}

func workflowReceiptValues(value workflowmodel.WorkflowExecutionReceipt) []any {
	relatedIDs := "[]"
	if strings.TrimSpace(value.ExecutionID) != "" {
		related, _ := json.Marshal([]string{strings.TrimSpace(value.ExecutionID)})
		relatedIDs = string(related)
	}
	return []any{value.ID, value.WorkspaceID, workflowReceiptOwner, workflowReceiptKind, "workflow.execute", "workflow", value.WorkflowKey, value.ID, value.RequestFingerprint, "", "", value.IdempotencyKey, value.Status, "/operations/" + value.ID, workflowReceiptResultJSON(value.ExecutionID), "{}", "", "", "", relatedIDs, value.ID, "[]", value.LeaseOwner, value.LeaseExpiresAt, value.FencingToken, value.ExpiresAt, value.CreatedAt, value.CreatedAt, "", value.UpdatedAt}
}

type workflowReceiptOperationResult struct {
	ExecutionID string `json:"execution_id,omitempty"`
}

func workflowReceiptResultJSON(executionID string) string {
	encoded, _ := json.Marshal(workflowReceiptOperationResult{ExecutionID: strings.TrimSpace(executionID)})
	return string(encoded)
}

type workflowReceiptScanner interface{ Scan(...any) error }

func scanWorkflowReceipt(row workflowReceiptScanner) (workflowmodel.WorkflowExecutionReceipt, error) {
	var value workflowmodel.WorkflowExecutionReceipt
	var owner, kind, actionKey, resourceType, operationKey, requestedBy, reason, statusURL string
	var resultJSON, metadataJSON, errorCode, failureClass, nextAction, relatedIDs, correlation, evidence, startedAt, finishedAt string
	if err := row.Scan(&value.ID, &value.WorkspaceID, &owner, &kind, &actionKey, &resourceType, &value.WorkflowKey, &operationKey, &value.RequestFingerprint, &requestedBy, &reason, &value.IdempotencyKey, &value.Status, &statusURL, &resultJSON, &metadataJSON, &errorCode, &failureClass, &nextAction, &relatedIDs, &correlation, &evidence, &value.LeaseOwner, &value.LeaseExpiresAt, &value.FencingToken, &value.ExpiresAt, &value.CreatedAt, &startedAt, &finishedAt, &value.UpdatedAt); err != nil {
		return workflowmodel.WorkflowExecutionReceipt{}, err
	}
	if owner != workflowReceiptOwner || kind != workflowReceiptKind || actionKey != "workflow.execute" || resourceType != "workflow" || operationKey != value.ID || correlation != value.ID {
		return workflowmodel.WorkflowExecutionReceipt{}, fmt.Errorf("workflow receipt operation identity is invalid")
	}
	var result workflowReceiptOperationResult
	if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
		return workflowmodel.WorkflowExecutionReceipt{}, fmt.Errorf("decode workflow receipt result: %w", err)
	}
	value.ExecutionID = result.ExecutionID
	return value, nil
}

func workflowReceiptOperationPredicate(receiptID string) query.Predicate {
	return query.And(
		query.Equal("id", strings.TrimSpace(receiptID)),
		query.Equal("owner", workflowReceiptOwner),
		query.Equal("kind", workflowReceiptKind),
	)
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
