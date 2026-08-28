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
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type AutomationWorkerStore struct {
	store  *database.RuntimeStore
	db     *sql.DB
	driver string
	wait   func(context.Context, time.Duration) error
}

func NewAutomationWorkerStore(s *database.RuntimeStore) AutomationWorkerStore {
	return AutomationWorkerStore{store: s, db: s.DB(), driver: s.Driver(), wait: waitAutomationClaimRetry}
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
		if err == nil || !r.isSQLiteBusyError(err) {
			return claimed, ok, err
		}
		if waitErr := r.wait(ctx, time.Duration(attempt+1)*time.Millisecond); waitErr != nil {
			return automationmodel.AutomationInstructionExecution{}, false, waitErr
		}
	}
	return automationmodel.AutomationInstructionExecution{}, false, fmt.Errorf("claim automation instruction execution: sqlite remained busy after retry")
}

func (r AutomationWorkerStore) claimOnce(ctx context.Context, workspaceID string, execution automationmodel.AutomationInstructionExecution, owner, now, leaseExpiresAt string) (automationmodel.AutomationInstructionExecution, bool, error) {
	s := r.store
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
	_, insertErr := r.db.ExecContext(ctx, "INSERT INTO "+s.TableIdentifier("automation_instruction_executions")+" ("+stringsJoinIdentifiers(s, columns...)+") VALUES ("+stringsJoinPlaceholders(s, len(columns))+")", values...)
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
	result, err := r.db.ExecContext(ctx, "UPDATE "+s.TableIdentifier("automation_instruction_executions")+" SET "+s.Identifier("status")+" = "+s.Placeholder(1)+", "+s.Identifier("result_json")+" = "+s.Placeholder(2)+", "+s.Identifier("error_code")+" = "+s.Placeholder(3)+", "+s.Identifier("lease_owner")+" = "+s.Placeholder(4)+", "+s.Identifier("lease_expires_at")+" = "+s.Placeholder(5)+", "+s.Identifier("fencing_token")+" = "+s.Identifier("fencing_token")+" + 1, "+s.Identifier("updated_at")+" = "+s.Placeholder(6)+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(7)+" AND "+s.Identifier("idempotency_key")+" = "+s.Placeholder(8)+" AND ("+s.Identifier("status")+" <> "+s.Placeholder(9)+" OR "+s.Identifier("lease_expires_at")+" <= "+s.Placeholder(10)+")", string(idempotency.StatusProcessing), "{}", "", execution.LeaseOwner, leaseExpiresAt, now, execution.WorkspaceID, execution.IdempotencyKey, string(idempotency.StatusProcessing), now)
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
	s := r.store
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
	update, err := r.db.ExecContext(ctx, "UPDATE "+s.TableIdentifier("automation_instruction_executions")+" SET "+s.Identifier("status")+" = "+s.Placeholder(1)+", "+s.Identifier("result_json")+" = "+s.Placeholder(2)+", "+s.Identifier("error_code")+" = "+s.Placeholder(3)+", "+s.Identifier("lease_owner")+" = '', "+s.Identifier("lease_expires_at")+" = '', "+s.Identifier("updated_at")+" = "+s.Placeholder(4)+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(5)+" AND "+s.Identifier("idempotency_key")+" = "+s.Placeholder(6)+" AND "+s.Identifier("status")+" = "+s.Placeholder(7)+" AND "+s.Identifier("lease_owner")+" = "+s.Placeholder(8)+" AND "+s.Identifier("fencing_token")+" = "+s.Placeholder(9), strings.TrimSpace(status), string(resultJSON), strings.TrimSpace(errorCode), now, workspaceID, idempotencyKey, string(idempotency.StatusProcessing), strings.TrimSpace(expectedLeaseOwner), expectedFencingToken)
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
	s := r.store
	workspaceID, err := automationWorkspaceID(workspaceID)
	if err != nil {
		return automationmodel.AutomationInstructionExecution{}, err
	}
	now = strings.TrimSpace(now)
	if now == "" {
		return automationmodel.AutomationInstructionExecution{}, fmt.Errorf("automation instruction heartbeat time is required")
	}
	update, err := r.db.ExecContext(ctx, "UPDATE "+s.TableIdentifier("automation_instruction_executions")+" SET "+s.Identifier("lease_expires_at")+" = "+s.Placeholder(1)+", "+s.Identifier("updated_at")+" = "+s.Placeholder(2)+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(3)+" AND "+s.Identifier("idempotency_key")+" = "+s.Placeholder(4)+" AND "+s.Identifier("status")+" = "+s.Placeholder(5)+" AND "+s.Identifier("lease_owner")+" = "+s.Placeholder(6)+" AND "+s.Identifier("fencing_token")+" = "+s.Placeholder(7), strings.TrimSpace(leaseExpiresAt), now, workspaceID, strings.TrimSpace(idempotencyKey), string(idempotency.StatusProcessing), strings.TrimSpace(expectedLeaseOwner), expectedFencingToken)
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
	s := r.store
	workspaceID, err := automationWorkspaceID(workspaceID)
	if err != nil {
		return automationmodel.AutomationInstructionExecution{}, false, err
	}
	row := r.db.QueryRowContext(ctx, "SELECT "+stringsJoinIdentifiers(s, automationInstructionExecutionColumns()...)+" FROM "+s.TableIdentifier("automation_instruction_executions")+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(1)+" AND "+s.Identifier("idempotency_key")+" = "+s.Placeholder(2)+" LIMIT 1", workspaceID, strings.TrimSpace(idempotencyKey))
	execution, err := scanAutomationInstructionExecution(row)
	if err == sql.ErrNoRows {
		return automationmodel.AutomationInstructionExecution{}, false, nil
	}
	if err != nil {
		return automationmodel.AutomationInstructionExecution{}, false, err
	}
	return execution, true, nil
}

func (r AutomationWorkerStore) isSQLiteBusyError(err error) bool {
	if err == nil || r.driver != "sqlite" {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "sqlite_busy") || strings.Contains(message, "database is locked") || strings.Contains(message, "database table is locked")
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
