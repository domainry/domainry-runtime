package workflow

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
	sharedoperation "github.com/domainry/domainry-foundation/operation"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

const (
	workflowReceiptOwner = "workflow"
	workflowReceiptKind  = "workflow.execution.start"
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
	operationStore := sharedoperation.NewSQLStore(r.database(), r.store.SQLRenderer)
	tx, beginErr := r.database().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if beginErr != nil {
		return workflowmodel.WorkflowExecutionClaimResult{}, beginErr
	}
	if guardErr := r.store.GuardSubjectEvidenceWrite(ctx, tx, workspaceID, sharedoperation.TableName,
		[]string{"id", "owner", "resource_type", "resource_id"}, []any{receipt.ID, workflowReceiptOwner, "workflow", receipt.WorkflowKey}); guardErr != nil {
		_ = tx.Rollback()
		return workflowmodel.WorkflowExecutionClaimResult{}, guardErr
	}
	inserted, insertErr := operationStore.InsertRecord(sharedoperation.WithExecutor(ctx, tx), workflowReceiptRecord(receipt))
	if insertErr == nil && inserted {
		if commitErr := tx.Commit(); commitErr != nil {
			return workflowmodel.WorkflowExecutionClaimResult{}, commitErr
		}
		r.store.ObserveIdempotency(ctx, receipt.WorkspaceID, "workflow.execute", idempotency.OutcomeAcquired)
		return workflowmodel.WorkflowExecutionClaimResult{Decision: idempotency.DecisionAcquired, Receipt: receipt}, nil
	}
	_ = tx.Rollback()
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
	status, empty := string(idempotency.StatusProcessing), ""
	resultJSON, relatedIDs := json.RawMessage(`{}`), json.RawMessage(`[]`)
	nowText := now.Format(time.RFC3339Nano)
	changed, err := operationStore.PatchRecord(ctx, sharedoperation.RecordFilter{
		WorkspaceID: receipt.WorkspaceID, ID: receipt.ID, Owner: workflowReceiptOwner, Kind: workflowReceiptKind,
		RequestFingerprint: receipt.RequestFingerprint, Status: string(idempotency.StatusProcessing), LeaseExpiresAtOrBefore: nowText,
	}, sharedoperation.RecordChanges{
		Status: &status, LeaseOwner: &receipt.LeaseOwner, LeaseExpiresAt: &receipt.LeaseExpiresAt, IncrementFencingToken: true,
		ResultJSON: &resultJSON, RelatedIDsJSON: &relatedIDs, FinishedAt: &empty, ExpiresAt: &empty, UpdatedAt: &receipt.UpdatedAt,
	})
	if err != nil {
		return workflowmodel.WorkflowExecutionClaimResult{}, err
	}
	current, _, err = r.findExecutionReceipt(ctx, receipt.WorkspaceID, receipt.WorkflowKey, receipt.IdempotencyKey)
	if err != nil {
		return workflowmodel.WorkflowExecutionClaimResult{}, err
	}
	if changed {
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
	resultJSON := json.RawMessage(workflowReceiptResultJSON(strings.TrimSpace(completion.ExecutionID)))
	relatedIDs := json.RawMessage(`[]`)
	if strings.TrimSpace(completion.ExecutionID) != "" {
		related, _ := json.Marshal([]string{strings.TrimSpace(completion.ExecutionID)})
		relatedIDs = related
	}
	status := string(idempotency.StatusSucceeded)
	expiresAt, finishedAt, token := completion.ExpiresAt.UTC().Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), completion.FencingToken
	changed, err := sharedoperation.NewSQLStore(r.database(), r.store.SQLRenderer).PatchRecord(ctx, sharedoperation.RecordFilter{
		WorkspaceID: workspaceID, ID: strings.TrimSpace(completion.ReceiptID), Owner: workflowReceiptOwner, Kind: workflowReceiptKind,
		LeaseOwner: strings.TrimSpace(completion.LeaseOwner), FencingToken: &token, Status: string(idempotency.StatusProcessing),
	}, sharedoperation.RecordChanges{
		Status: &status, ResultJSON: &resultJSON, RelatedIDsJSON: &relatedIDs, ExpiresAt: &expiresAt, FinishedAt: &finishedAt, UpdatedAt: &finishedAt,
	})
	if err != nil {
		return err
	}
	if !changed {
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
	record, found, err := sharedoperation.NewSQLStore(r.database(), r.store.SQLRenderer).GetRecord(ctx, sharedoperation.RecordFilter{
		WorkspaceID: workspaceID, ID: id, Owner: workflowReceiptOwner, Kind: workflowReceiptKind,
	})
	if err != nil || !found {
		return workflowmodel.WorkflowExecutionReceipt{}, found, err
	}
	value, err := workflowReceipt(record)
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

func workflowReceiptRecord(value workflowmodel.WorkflowExecutionReceipt) sharedoperation.Record {
	relatedIDs := json.RawMessage(`[]`)
	if strings.TrimSpace(value.ExecutionID) != "" {
		related, _ := json.Marshal([]string{strings.TrimSpace(value.ExecutionID)})
		relatedIDs = related
	}
	return sharedoperation.Record{
		ID: value.ID, WorkspaceID: value.WorkspaceID, Owner: workflowReceiptOwner, Kind: workflowReceiptKind,
		ActionKey: "workflow.execute", ResourceType: "workflow", ResourceID: value.WorkflowKey,
		IdempotencyKey: value.ID, RequestFingerprint: value.RequestFingerprint, Reference: value.IdempotencyKey,
		Status: value.Status, StatusURL: "/operations/" + value.ID, ResultJSON: json.RawMessage(workflowReceiptResultJSON(value.ExecutionID)),
		MetadataJSON: json.RawMessage(`{}`), RelatedIDsJSON: relatedIDs, Correlation: value.ID, EvidenceJSON: json.RawMessage(`[]`),
		LeaseOwner: value.LeaseOwner, LeaseExpiresAt: value.LeaseExpiresAt, FencingToken: value.FencingToken, ExpiresAt: value.ExpiresAt,
		CreatedAt: value.CreatedAt, StartedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

type workflowReceiptOperationResult struct {
	ExecutionID string `json:"execution_id,omitempty"`
}

func workflowReceiptResultJSON(executionID string) string {
	encoded, _ := json.Marshal(workflowReceiptOperationResult{ExecutionID: strings.TrimSpace(executionID)})
	return string(encoded)
}

func workflowReceipt(record sharedoperation.Record) (workflowmodel.WorkflowExecutionReceipt, error) {
	value := workflowmodel.WorkflowExecutionReceipt{
		ID: record.ID, WorkspaceID: record.WorkspaceID, WorkflowKey: record.ResourceID, IdempotencyKey: record.Reference,
		RequestFingerprint: record.RequestFingerprint, Status: record.Status, LeaseOwner: record.LeaseOwner,
		LeaseExpiresAt: record.LeaseExpiresAt, FencingToken: record.FencingToken, ExpiresAt: record.ExpiresAt,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
	if record.Owner != workflowReceiptOwner || record.Kind != workflowReceiptKind || record.ActionKey != "workflow.execute" || record.ResourceType != "workflow" || record.IdempotencyKey != value.ID || record.Correlation != value.ID {
		return workflowmodel.WorkflowExecutionReceipt{}, fmt.Errorf("workflow receipt operation identity is invalid")
	}
	var result workflowReceiptOperationResult
	if err := json.Unmarshal(record.ResultJSON, &result); err != nil {
		return workflowmodel.WorkflowExecutionReceipt{}, fmt.Errorf("decode workflow receipt result: %w", err)
	}
	value.ExecutionID = result.ExecutionID
	return value, nil
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
