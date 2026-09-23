package automation

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-orm/query"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type AutomationExecutionStore struct {
	store *database.RuntimeStore
	db    *sql.DB
}

const (
	automationRunsTable       = "_automation_runs"
	automationRuleRunKind     = "rule"
	automationInstructionKind = "instruction"
)

func NewAutomationExecutionStore(store *database.RuntimeStore) AutomationExecutionStore {
	return AutomationExecutionStore{store: store, db: store.DB()}
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
	columns := []string{"id", "run_kind", "idempotency_key", "rule_key", "object_key", "record_id", "phase", "operation", "status", "actor_id", "role_key", "request_id", "correlation_id", "event_id", "duration_ms", "error_code", "candidate_json", "trace_json", "created_at", "updated_at"}
	values := []any{value.ID, automationRuleRunKind, value.ID, value.RuleKey, value.ObjectKey, value.RecordID, value.Phase, value.Operation, value.Status, value.ActorID, value.RoleKey, value.RequestID, value.CorrelationID, value.EventID, value.DurationMS, value.ErrorCode, string(candidateJSON), string(traceJSON), value.CreatedAt, value.UpdatedAt}
	builder, buildErr := r.store.SubjectEvidenceInsertBuilder(workspaceID, automationRunsTable, columns, values)
	if buildErr != nil {
		return automationmodel.AutomationRuleExecution{}, buildErr
	}
	statement, args, buildErr := builder.Build()
	if buildErr != nil {
		return automationmodel.AutomationRuleExecution{}, fmt.Errorf("build automation rule execution insert: %w", buildErr)
	}
	result, err := r.executor(ctx).ExecContext(ctx, statement, args...)
	if err != nil {
		return automationmodel.AutomationRuleExecution{}, fmt.Errorf("insert automation rule execution: %w", err)
	}
	if affected, err := result.RowsAffected(); err != nil {
		return automationmodel.AutomationRuleExecution{}, err
	} else if affected != 1 {
		return automationmodel.AutomationRuleExecution{}, fmt.Errorf("runtime.subject_erased")
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

func (r AutomationExecutionStore) ListExecutions(ctx context.Context, workspaceID string, filter automationmodel.AutomationExecutionFilter) ([]automationmodel.AutomationRuleExecution, error) {
	workspaceID, err := automationWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	predicates := make([]query.Predicate, 0, 9)
	addEqual := func(column, value string) {
		if value = strings.TrimSpace(value); value != "" {
			predicates = append(predicates, query.Equal(column, value))
		}
	}
	addEqual("rule_key", filter.RuleKey)
	addEqual("object_key", filter.ObjectKey)
	addEqual("record_id", filter.RecordID)
	addEqual("phase", filter.Phase)
	addEqual("status", filter.Status)
	if value := strings.TrimSpace(filter.ConnectorKey); value != "" {
		predicates = append(predicates, query.Like("trace_json", "%\"connector_key\":\""+value+"\"%"))
	}
	if value := strings.TrimSpace(filter.From); value != "" {
		predicates = append(predicates, query.GreaterThanOrEqual("created_at", value))
	}
	if value := strings.TrimSpace(filter.To); value != "" {
		predicates = append(predicates, query.LessThanOrEqual("created_at", value))
	}
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	predicates = append(predicates, query.Equal("run_kind", automationRuleRunKind))
	builder := query.NewWorkspaceSelectBuilder(r.store.SQLRenderer, automationRunsTable, workspaceID).
		Columns("id", "workspace_id", "rule_key", "object_key", "record_id", "phase", "operation", "status", "actor_id", "role_key", "request_id", "correlation_id", "event_id", "duration_ms", "error_code", "candidate_json", "trace_json", "created_at", "updated_at").
		OrderBy(query.Descending("created_at")).Limit(limit)
	if len(predicates) > 0 {
		builder.Where(query.And(predicates...))
	}
	statement, args, buildErr := builder.Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build automation rule execution list: %w", buildErr)
	}
	rows, err := r.db.QueryContext(ctx, statement, args...)
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
