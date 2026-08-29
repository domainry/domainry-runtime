// Automation worker persistence.
package automation

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
	ormbuilder "github.com/domainry/domainry-orm/builder"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type AutomationWorkerStore struct {
	store *database.RuntimeStore
	db    *sql.DB
	wait  func(context.Context, time.Duration) error
}

func NewAutomationWorkerStore(s *database.RuntimeStore) AutomationWorkerStore {
	return AutomationWorkerStore{store: s, db: s.DB(), wait: waitAutomationClaimRetry}
}

func waitAutomationClaimRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (r AutomationWorkerStore) ClaimInstruction(ctx context.Context, workspaceID string, execution automationmodel.AutomationInstructionExecution, owner, now, leaseExpiresAt string) (automationmodel.AutomationInstructionExecution, bool, error) {
	for attempt := 0; attempt < 50; attempt++ {
		claimed, ok, err := r.claimOnce(ctx, workspaceID, execution, owner, now, leaseExpiresAt)
		if err == nil || !r.store.IsTransientError(err) {
			return claimed, ok, err
		}
		if waitErr := r.wait(ctx, time.Duration(attempt+1)*time.Millisecond); waitErr != nil {
			return automationmodel.AutomationInstructionExecution{}, false, waitErr
		}
	}
	return automationmodel.AutomationInstructionExecution{}, false, fmt.Errorf("claim automation instruction execution: sqlite remained busy after retry")
}

func (r AutomationWorkerStore) claimOnce(ctx context.Context, workspaceID string, execution automationmodel.AutomationInstructionExecution, owner, now, leaseExpiresAt string) (automationmodel.AutomationInstructionExecution, bool, error) {
	workspaceID, err := automationExecutionWorkspaceID(workspaceID, execution.WorkspaceID)
	if err != nil {
		return automationmodel.AutomationInstructionExecution{}, false, err
	}
	execution.WorkspaceID = workspaceID
	execution.IdempotencyKey = strings.TrimSpace(execution.IdempotencyKey)
	if execution.IdempotencyKey == "" {
		return automationmodel.AutomationInstructionExecution{}, false, fmt.Errorf("automation instruction idempotency key is required")
	}
	if strings.TrimSpace(now) == "" {
		now = time.Now().UTC().Format(time.RFC3339)
	}
	if strings.TrimSpace(leaseExpiresAt) == "" {
		leaseExpiresAt = time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339)
	}
	execution.ID = "automation_instruction:" + businessActionShortHash(execution.WorkspaceID+":"+execution.IdempotencyKey)
	execution.Status = string(idempotency.StatusProcessing)
	execution.Result = nonNilMap(execution.Result)
	execution.ErrorCode = ""
	execution.LeaseOwner = strings.TrimSpace(owner)
	if execution.LeaseOwner == "" {
		return automationmodel.AutomationInstructionExecution{}, false, fmt.Errorf("automation instruction worker owner is required")
	}
	execution.LeaseExpiresAt = leaseExpiresAt
	execution.FencingToken = 1
	execution.CreatedAt = now
	execution.UpdatedAt = now
	resultJSON, err := json.Marshal(execution.Result)
	if err != nil {
		return automationmodel.AutomationInstructionExecution{}, false, fmt.Errorf("encode automation instruction result: %w", err)
	}
	columns := automationInstructionExecutionColumns()
	values := []any{execution.ID, execution.WorkspaceID, execution.IdempotencyKey, execution.RuleKey, execution.ObjectKey, execution.RecordID, execution.RecordVersion, execution.Operation, execution.InstructionKey, execution.Status, string(resultJSON), execution.ErrorCode, execution.LeaseOwner, execution.LeaseExpiresAt, execution.FencingToken, execution.CreatedAt, execution.UpdatedAt}
	query, args, buildErr := ormbuilder.NewWorkspaceInsertBuilder(r.store.SQLRenderer, "automation_instruction_executions", workspaceID).Columns(append(columns[:1], columns[2:]...)...).Values(append(values[:1], values[2:]...)...).Build()
	if buildErr != nil {
		return automationmodel.AutomationInstructionExecution{}, false, fmt.Errorf("build automation instruction execution insert: %w", buildErr)
	}
	_, insertErr := r.db.ExecContext(ctx, query, args...)
	if insertErr == nil {
		return execution, true, nil
	}
	existing, found, err := r.find(ctx, execution.WorkspaceID, execution.IdempotencyKey)
	if err != nil {
		return automationmodel.AutomationInstructionExecution{}, false, err
	}
	if !found {
		return automationmodel.AutomationInstructionExecution{}, false, fmt.Errorf("insert automation instruction execution: %w", insertErr)
	}
	if existing.Status == "succeeded" {
		return existing, false, nil
	}
	query, args, err = ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "automation_instruction_executions", workspaceID).Set("status", string(idempotency.StatusProcessing)).Set("result_json", "{}").Set("error_code", "").Set("lease_owner", execution.LeaseOwner).Set("lease_expires_at", leaseExpiresAt).SetExpression("fencing_token", ormbuilder.Add(ormbuilder.Column("fencing_token"), ormbuilder.Value(1))).Set("updated_at", now).Where(ormbuilder.And(ormbuilder.Equal("idempotency_key", execution.IdempotencyKey), ormbuilder.Or(ormbuilder.NotEqual("status", string(idempotency.StatusProcessing)), ormbuilder.LessThanOrEqual("lease_expires_at", now)))).Build()
	if err != nil {
		return automationmodel.AutomationInstructionExecution{}, false, fmt.Errorf("build automation instruction reclaim: %w", err)
	}
	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return automationmodel.AutomationInstructionExecution{}, false, fmt.Errorf("reclaim automation instruction execution: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return automationmodel.AutomationInstructionExecution{}, false, fmt.Errorf("read automation instruction claim result: %w", err)
	}
	if rows == 0 {
		return existing, false, nil
	}
	reclaimed, found, err := r.find(ctx, execution.WorkspaceID, execution.IdempotencyKey)
	if err != nil {
		return automationmodel.AutomationInstructionExecution{}, false, err
	}
	if !found {
		return automationmodel.AutomationInstructionExecution{}, false, sql.ErrNoRows
	}
	return reclaimed, true, nil
}

func (r AutomationWorkerStore) CompleteInstruction(ctx context.Context, workspaceID, idempotencyKey, expectedLeaseOwner string, expectedFencingToken int64, status string, result map[string]any, errorCode, now string) (automationmodel.AutomationInstructionExecution, error) {
	workspaceID, err := automationWorkspaceID(workspaceID)
	if err != nil {
		return automationmodel.AutomationInstructionExecution{}, err
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	resultJSON, err := json.Marshal(nonNilMap(result))
	if err != nil {
		return automationmodel.AutomationInstructionExecution{}, fmt.Errorf("encode automation instruction completion result: %w", err)
	}
	now = strings.TrimSpace(now)
	if now == "" {
		return automationmodel.AutomationInstructionExecution{}, fmt.Errorf("automation instruction completion time is required")
	}
	query, args, err := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "automation_instruction_executions", workspaceID).Set("status", strings.TrimSpace(status)).Set("result_json", string(resultJSON)).Set("error_code", strings.TrimSpace(errorCode)).Set("lease_owner", "").Set("lease_expires_at", "").Set("updated_at", now).Where(automationInstructionLeasePredicate(idempotencyKey, expectedLeaseOwner, expectedFencingToken)).Build()
	if err != nil {
		return automationmodel.AutomationInstructionExecution{}, fmt.Errorf("build automation instruction completion: %w", err)
	}
	update, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return automationmodel.AutomationInstructionExecution{}, fmt.Errorf("complete automation instruction execution: %w", err)
	}
	affected, err := update.RowsAffected()
	if err != nil {
		return automationmodel.AutomationInstructionExecution{}, err
	}
	if affected != 1 {
		return automationmodel.AutomationInstructionExecution{}, mutation.MutationConflict("automation_instruction", idempotencyKey, mutation.MutationConflictLeaseLost, nil)
	}
	execution, found, err := r.find(ctx, workspaceID, idempotencyKey)
	if err != nil {
		return automationmodel.AutomationInstructionExecution{}, err
	}
	if !found {
		return automationmodel.AutomationInstructionExecution{}, sql.ErrNoRows
	}
	return execution, nil
}

func (r AutomationWorkerStore) HeartbeatInstruction(ctx context.Context, workspaceID, idempotencyKey, expectedLeaseOwner string, expectedFencingToken int64, leaseExpiresAt, now string) (automationmodel.AutomationInstructionExecution, error) {
	workspaceID, err := automationWorkspaceID(workspaceID)
	if err != nil {
		return automationmodel.AutomationInstructionExecution{}, err
	}
	now = strings.TrimSpace(now)
	if now == "" {
		return automationmodel.AutomationInstructionExecution{}, fmt.Errorf("automation instruction heartbeat time is required")
	}
	query, args, err := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "automation_instruction_executions", workspaceID).Set("lease_expires_at", strings.TrimSpace(leaseExpiresAt)).Set("updated_at", now).Where(automationInstructionLeasePredicate(idempotencyKey, expectedLeaseOwner, expectedFencingToken)).Build()
	if err != nil {
		return automationmodel.AutomationInstructionExecution{}, fmt.Errorf("build automation instruction heartbeat: %w", err)
	}
	update, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return automationmodel.AutomationInstructionExecution{}, err
	}
	n, err := update.RowsAffected()
	if err != nil {
		return automationmodel.AutomationInstructionExecution{}, err
	}
	if n != 1 {
		return automationmodel.AutomationInstructionExecution{}, mutation.MutationConflict("automation_instruction", idempotencyKey, mutation.MutationConflictLeaseLost, nil)
	}
	execution, _, err := r.find(ctx, workspaceID, idempotencyKey)
	return execution, err
}

func (r AutomationWorkerStore) find(ctx context.Context, workspaceID, idempotencyKey string) (automationmodel.AutomationInstructionExecution, bool, error) {
	workspaceID, err := automationWorkspaceID(workspaceID)
	if err != nil {
		return automationmodel.AutomationInstructionExecution{}, false, err
	}
	query, args, err := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "automation_instruction_executions", workspaceID).Columns(automationInstructionExecutionColumns()...).Where(ormbuilder.Equal("idempotency_key", strings.TrimSpace(idempotencyKey))).Limit(1).Build()
	if err != nil {
		return automationmodel.AutomationInstructionExecution{}, false, err
	}
	row := r.db.QueryRowContext(ctx, query, args...)
	execution, err := scanAutomationInstructionExecution(row)
	if err == sql.ErrNoRows {
		return automationmodel.AutomationInstructionExecution{}, false, nil
	}
	if err != nil {
		return automationmodel.AutomationInstructionExecution{}, false, err
	}
	return execution, true, nil
}

func automationInstructionLeasePredicate(idempotencyKey, owner string, token int64) ormbuilder.Predicate {
	return ormbuilder.And(ormbuilder.Equal("idempotency_key", strings.TrimSpace(idempotencyKey)), ormbuilder.Equal("status", string(idempotency.StatusProcessing)), ormbuilder.Equal("lease_owner", strings.TrimSpace(owner)), ormbuilder.Equal("fencing_token", token))
}

func automationInstructionExecutionColumns() []string {
	return []string{"id", "workspace_id", "idempotency_key", "rule_key", "object_key", "record_id", "record_version", "operation", "instruction_key", "status", "result_json", "error_code", "lease_owner", "lease_expires_at", "fencing_token", "created_at", "updated_at"}
}

func scanAutomationInstructionExecution(scanner interface{ Scan(...any) error }) (automationmodel.AutomationInstructionExecution, error) {
	var execution automationmodel.AutomationInstructionExecution
	var resultJSON string
	if err := scanner.Scan(&execution.ID, &execution.WorkspaceID, &execution.IdempotencyKey, &execution.RuleKey, &execution.ObjectKey, &execution.RecordID, &execution.RecordVersion, &execution.Operation, &execution.InstructionKey, &execution.Status, &resultJSON, &execution.ErrorCode, &execution.LeaseOwner, &execution.LeaseExpiresAt, &execution.FencingToken, &execution.CreatedAt, &execution.UpdatedAt); err != nil {
		return automationmodel.AutomationInstructionExecution{}, err
	}
	if err := json.Unmarshal([]byte(resultJSON), &execution.Result); err != nil {
		return automationmodel.AutomationInstructionExecution{}, fmt.Errorf("decode automation instruction result: %w", err)
	}
	execution.Result = nonNilMap(execution.Result)
	return execution, nil
}

func businessActionShortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:16]
}
