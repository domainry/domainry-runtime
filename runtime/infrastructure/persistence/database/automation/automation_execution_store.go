// Automation execution persistence.
package automation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type AutomationExecutionStore struct {
	store  *database.RuntimeStore
	db     *sql.DB
	driver string
}

func NewAutomationExecutionStore(store *database.RuntimeStore) AutomationExecutionStore {
	return AutomationExecutionStore{store: store, db: store.DB(), driver: store.Driver()}
}

func automationExecutionColumnsSQL(store *database.RuntimeStore) string {
	return stringsJoinIdentifiers(store, "id", "workspace_id", "rule_key", "object_key", "record_id", "phase", "operation", "status", "actor_id", "role_key", "request_id", "correlation_id", "event_id", "duration_ms", "error_code", "candidate_json", "trace_json", "created_at", "updated_at")
}

func scanAutomationRuleExecution(scanner interface{ Scan(...any) error }) (automationmodel.AutomationRuleExecution, error) {
	var execution automationmodel.AutomationRuleExecution
	var candidateJSON, traceJSON string
	if err := scanner.Scan(&execution.ID, &execution.WorkspaceID, &execution.RuleKey, &execution.ObjectKey, &execution.RecordID, &execution.Phase, &execution.Operation, &execution.Status, &execution.ActorID, &execution.RoleKey, &execution.RequestID, &execution.CorrelationID, &execution.EventID, &execution.DurationMS, &execution.ErrorCode, &candidateJSON, &traceJSON, &execution.CreatedAt, &execution.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return automationmodel.AutomationRuleExecution{}, err
		}
		return automationmodel.AutomationRuleExecution{}, fmt.Errorf("scan automation rule execution: %w", err)
	}
	if err := json.Unmarshal([]byte(candidateJSON), &execution.Candidate); err != nil {
		return automationmodel.AutomationRuleExecution{}, fmt.Errorf("decode automation execution candidate: %w", err)
	}
	if err := json.Unmarshal([]byte(traceJSON), &execution.Trace); err != nil {
		return automationmodel.AutomationRuleExecution{}, fmt.Errorf("decode automation execution trace: %w", err)
	}
	return execution, nil
}

func (r AutomationExecutionStore) InsertExecution(ctx context.Context, workspaceID string, value automationmodel.AutomationRuleExecution) (automationmodel.AutomationRuleExecution, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	workspaceID, err := automationExecutionWorkspaceID(workspaceID, value.WorkspaceID)
	if err != nil {
		return automationmodel.AutomationRuleExecution{}, err
	}
	value.WorkspaceID = workspaceID
	if strings.TrimSpace(value.ID) == "" {
		value.ID = fmt.Sprintf("automation_execution_%d", time.Now().UnixNano())
	}
	if value.CreatedAt == "" {
		value.CreatedAt = now
	}
	value.UpdatedAt = now
	candidateJSON, err := json.Marshal(nonNilMap(value.Candidate))
	if err != nil {
		return automationmodel.AutomationRuleExecution{}, fmt.Errorf("encode automation execution candidate: %w", err)
	}
	traceJSON, err := json.Marshal(nonNilMap(value.Trace))
	if err != nil {
		return automationmodel.AutomationRuleExecution{}, fmt.Errorf("encode automation execution trace: %w", err)
	}
	columns := []string{"id", "workspace_id", "rule_key", "object_key", "record_id", "phase", "operation", "status", "actor_id", "role_key", "request_id", "correlation_id", "event_id", "duration_ms", "error_code", "candidate_json", "trace_json", "created_at", "updated_at"}
	values := []any{value.ID, value.WorkspaceID, value.RuleKey, value.ObjectKey, value.RecordID, value.Phase, value.Operation, value.Status, value.ActorID, value.RoleKey, value.RequestID, value.CorrelationID, value.EventID, value.DurationMS, value.ErrorCode, string(candidateJSON), string(traceJSON), value.CreatedAt, value.UpdatedAt}
	if _, err := r.executor(ctx).ExecContext(ctx, "INSERT INTO "+r.store.TableIdentifier("automation_rule_executions")+" ("+stringsJoinIdentifiers(r.store, columns...)+") VALUES ("+stringsJoinPlaceholders(r.store, len(columns))+")", values...); err != nil {
		return automationmodel.AutomationRuleExecution{}, fmt.Errorf("insert automation rule execution: %w", err)
	}
	return value, nil
}

type automationExecutionExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (r AutomationExecutionStore) executor(ctx context.Context) automationExecutionExecutor {
	if tx := database.ActionExecutionTransaction(ctx); tx != nil {
		return tx
	}
	return r.db
}

// InsertExecutionSeed inserts immutable fixture evidence once.
func (r AutomationExecutionStore) InsertExecutionSeed(ctx context.Context, workspaceID string, value automationmodel.AutomationRuleExecution) (automationmodel.AutomationRuleExecution, error) {
	workspaceID, err := automationExecutionWorkspaceID(workspaceID, value.WorkspaceID)
	if err != nil {
		return automationmodel.AutomationRuleExecution{}, err
	}
	value.WorkspaceID = workspaceID
	if strings.TrimSpace(value.ID) == "" {
		return automationmodel.AutomationRuleExecution{}, fmt.Errorf("automation execution seed id is required")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if strings.TrimSpace(value.CreatedAt) == "" {
		value.CreatedAt = now
	}
	if strings.TrimSpace(value.UpdatedAt) == "" {
		value.UpdatedAt = value.CreatedAt
	}
	candidateJSON, err := json.Marshal(nonNilMap(value.Candidate))
	if err != nil {
		return automationmodel.AutomationRuleExecution{}, fmt.Errorf("encode automation execution seed candidate: %w", err)
	}
	traceJSON, err := json.Marshal(nonNilMap(value.Trace))
	if err != nil {
		return automationmodel.AutomationRuleExecution{}, fmt.Errorf("encode automation execution seed trace: %w", err)
	}
	columns := []string{"id", "workspace_id", "rule_key", "object_key", "record_id", "phase", "operation", "status", "actor_id", "role_key", "request_id", "correlation_id", "event_id", "duration_ms", "error_code", "candidate_json", "trace_json", "created_at", "updated_at"}
	values := []any{value.ID, value.WorkspaceID, value.RuleKey, value.ObjectKey, value.RecordID, value.Phase, value.Operation, value.Status, value.ActorID, value.RoleKey, value.RequestID, value.CorrelationID, value.EventID, value.DurationMS, value.ErrorCode, string(candidateJSON), string(traceJSON), value.CreatedAt, value.UpdatedAt}
	s := r.store
	insert := "INSERT INTO "
	conflict := " ON CONFLICT (" + stringsJoinIdentifiers(s, "workspace_id", "id") + ") DO NOTHING"
	if r.driver == "mysql" {
		insert = "INSERT IGNORE INTO "
		conflict = ""
	}
	query := insert + s.TableIdentifier("automation_rule_executions") + " (" + stringsJoinIdentifiers(s, columns...) + ") VALUES (" + stringsJoinPlaceholders(s, len(columns)) + ")" + conflict
	if _, err := r.db.ExecContext(ctx, query, values...); err != nil {
		return automationmodel.AutomationRuleExecution{}, fmt.Errorf("insert automation execution seed: %w", err)
	}
	return value, nil
}

func (r AutomationExecutionStore) ListExecutions(ctx context.Context, workspaceID string, filter automationmodel.AutomationExecutionFilter) ([]automationmodel.AutomationRuleExecution, error) {
	workspaceID, err := automationWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	where, args := []string{r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1)}, []any{workspaceID}
	addEqual := func(column, value string) {
		if value = strings.TrimSpace(value); value != "" {
			args = append(args, value)
			where = append(where, r.store.Identifier(column)+" = "+r.store.Placeholder(len(args)))
		}
	}
	addEqual("rule_key", filter.RuleKey)
	addEqual("object_key", filter.ObjectKey)
	addEqual("record_id", filter.RecordID)
	addEqual("phase", filter.Phase)
	addEqual("status", filter.Status)
	if value := strings.TrimSpace(filter.ConnectorKey); value != "" {
		args = append(args, "%\"connector_key\":\""+value+"\"%")
		where = append(where, r.store.Identifier("trace_json")+" LIKE "+r.store.Placeholder(len(args)))
	}
	if value := strings.TrimSpace(filter.From); value != "" {
		args = append(args, value)
		where = append(where, r.store.Identifier("created_at")+" >= "+r.store.Placeholder(len(args)))
	}
	if value := strings.TrimSpace(filter.To); value != "" {
		args = append(args, value)
		where = append(where, r.store.Identifier("created_at")+" <= "+r.store.Placeholder(len(args)))
	}
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, "SELECT "+automationExecutionColumnsSQL(r.store)+" FROM "+r.store.TableIdentifier("automation_rule_executions")+" WHERE "+strings.Join(where, " AND ")+" ORDER BY "+r.store.Identifier("created_at")+" DESC LIMIT "+r.store.Placeholder(len(args)), args...)
	if err != nil {
		return nil, fmt.Errorf("list automation rule executions: %w", err)
	}
	defer rows.Close()
	out := []automationmodel.AutomationRuleExecution{}
	for rows.Next() {
		value, err := scanAutomationRuleExecution(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list automation rule executions: %w", err)
	}
	return out, nil
}
