// Workflow worker persistence.
package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	ormbuilder "github.com/domainry/domainry-orm/builder"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"sort"
	"strings"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type WorkflowWorkerStore struct {
	store        *database.RuntimeStore
	db           workflowDatabase
	claimBackoff func(context.Context, int) error
}

func NewWorkflowWorkerStore(s *database.RuntimeStore) WorkflowWorkerStore {
	return WorkflowWorkerStore{store: s}
}

func (r WorkflowWorkerStore) database() workflowDatabase {
	if r.db != nil {
		return r.db
	}
	return r.store.DB()
}

func (r WorkflowWorkerStore) waitForClaimRetry(ctx context.Context, attempt int) error {
	if r.claimBackoff != nil {
		return r.claimBackoff(ctx, attempt)
	}
	return workflowClaimBackoff(ctx, attempt)
}

func (r WorkflowWorkerStore) InsertExecution(ctx context.Context, workspaceID string, execution workflowmodel.WorkflowExecution) error {
	var err error
	if workspaceID, err = requireWorkflowWorkspaceID(workspaceID); err != nil {
		return err
	}
	execution.WorkspaceID = workspaceID
	columns, values, err := workflowExecutionInsertValues(execution)
	if err != nil {
		return err
	}
	tx, err := r.database().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, r.store.InsertStatement("_workflow_executions", columns), values...); err != nil {
		return err
	}
	if err := RegisterWorkflowContinuationScope(ctx, r.store, tx, workspaceID, execution.UpdatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

func (r WorkflowWorkerStore) ListWorkflowContinuationWorkspaces(ctx context.Context, scope principalmodel.SystemScope, limit int) ([]string, error) {
	if !scope.Valid() || scope.Kind != principalmodel.SystemScopeRuntimeGlobal {
		return nil, principalmodel.ErrSystemScopeRequired
	}
	return r.store.WorkerQueueScopePage(ctx, r.database(), workflowContinuationQueueKind, limit)
}

func (r WorkflowWorkerStore) GetExecution(ctx context.Context, workspaceID, executionID string) (workflowmodel.WorkflowExecution, bool, error) {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return workflowmodel.WorkflowExecution{}, false, err
	}
	s := r.store
	row := r.database().QueryRowContext(ctx, "SELECT "+strings.Join(database.QuotedColumns(s, workflowExecutionColumns()), ", ")+" FROM "+s.TableIdentifier("_workflow_executions")+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(1)+" AND "+s.Identifier("id")+" = "+s.Placeholder(2), workspaceID, executionID)
	execution, err := scanWorkflowExecution(row)
	if err == sql.ErrNoRows {
		return workflowmodel.WorkflowExecution{}, false, nil
	}
	return execution, err == nil, err
}

func (r WorkflowWorkerStore) ListExecutions(ctx context.Context, workspaceID string, limit int) ([]workflowmodel.WorkflowExecution, error) {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	s := r.store
	if limit <= 0 {
		limit = 100
	} else if limit > 1000 {
		limit = 1000
	}
	rows, err := r.database().QueryContext(ctx, "SELECT "+strings.Join(database.QuotedColumns(s, workflowExecutionColumns()), ", ")+" FROM "+s.TableIdentifier("_workflow_executions")+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(1)+" ORDER BY "+s.Identifier("created_at")+" DESC LIMIT "+s.Placeholder(2), workspaceID, limit)
	if err != nil {
		return nil, fmt.Errorf("list workflow executions: %w", err)
	}
	defer rows.Close()
	out := []workflowmodel.WorkflowExecution{}
	for rows.Next() {
		execution, err := scanWorkflowExecution(rows)
		if err != nil {
			return nil, fmt.Errorf("scan workflow execution: %w", err)
		}
		out = append(out, execution)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read workflow executions: %w", err)
	}
	return out, nil
}

func (r WorkflowWorkerStore) UpdateExecution(ctx context.Context, workspaceID string, execution workflowmodel.WorkflowExecution) error {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return err
	}
	columns := workflowExecutionMutableColumns()
	values, err := workflowExecutionMutableValues(execution)
	if err != nil {
		return err
	}
	return r.updateRow(ctx, "_workflow_executions", workspaceID, execution.ID, columns, values)
}

func (r WorkflowWorkerStore) UpdateExecutionWhere(ctx context.Context, workspaceID string, execution workflowmodel.WorkflowExecution, conditions map[string]any) (bool, error) {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return false, err
	}
	columns := workflowExecutionMutableColumns()
	values, err := workflowExecutionMutableValues(execution)
	if err != nil {
		return false, err
	}
	builder := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "_workflow_executions", workspaceID)
	for i, column := range columns {
		builder.Set(column, values[i])
	}
	predicates := []ormbuilder.Predicate{ormbuilder.Equal("id", execution.ID)}
	keys := make([]string, 0, len(conditions))
	for key := range conditions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		predicates = append(predicates, ormbuilder.Equal(key, workflowExecutionConditionValue(key, conditions[key])))
	}
	statement, args, buildErr := builder.Where(ormbuilder.And(predicates...)).Build()
	if buildErr != nil {
		return false, fmt.Errorf("build conditional workflow execution update: %w", buildErr)
	}
	result, err := r.database().ExecContext(ctx, statement, args...)
	if err != nil {
		return false, fmt.Errorf("update workflow execution with conditions: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect conditional workflow update: %w", err)
	}
	return affected == 1, nil
}

func (r WorkflowWorkerStore) ListTasks(ctx context.Context, workspaceID, processID, assigneeUserID, status string, limit int) ([]workflowmodel.WorkflowTask, error) {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	s := r.store
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	clauses, args := []string{s.Identifier("workspace_id") + " = " + s.Placeholder(1)}, []any{workspaceID}
	filters := []struct{ column, value string }{{"process_id", processID}, {"assignee_user_id", assigneeUserID}, {"status", status}}
	for _, filter := range filters {
		if strings.TrimSpace(filter.value) != "" {
			args = append(args, strings.TrimSpace(filter.value))
			clauses = append(clauses, s.Identifier(filter.column)+" = "+s.Placeholder(len(args)))
		}
	}
	where := " WHERE " + strings.Join(clauses, " AND ")
	args = append(args, limit)
	rows, err := r.database().QueryContext(ctx, "SELECT "+strings.Join(database.QuotedColumns(s, workflowTaskColumns()), ", ")+" FROM "+s.TableIdentifier("workflow_tasks")+where+" ORDER BY "+s.Identifier("created_at")+" DESC LIMIT "+s.Placeholder(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []workflowmodel.WorkflowTask{}
	for rows.Next() {
		task, err := scanWorkflowTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, task)
	}
	return out, rows.Err()
}

func (r WorkflowWorkerStore) GetProcess(ctx context.Context, workspaceID, processID string) (workflowmodel.WorkflowProcessInstance, bool, error) {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return workflowmodel.WorkflowProcessInstance{}, false, err
	}
	s := r.store
	columns := workflowProcessColumns
	row := r.database().QueryRowContext(ctx, "SELECT "+strings.Join(database.QuotedColumns(s, columns), ", ")+" FROM "+s.TableIdentifier("workflow_process_instances")+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(1)+" AND "+s.Identifier("id")+" = "+s.Placeholder(2), workspaceID, processID)
	process, err := scanWorkflowProcess(row)
	if err == sql.ErrNoRows {
		return workflowmodel.WorkflowProcessInstance{}, false, nil
	}
	return process, err == nil, err
}

func (r WorkflowWorkerStore) ListProcessEvents(ctx context.Context, workspaceID, processID string, limit int) ([]workflowmodel.WorkflowProcessEvent, error) {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	s := r.store
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	columns := workflowEventColumns
	rows, err := r.database().QueryContext(ctx, "SELECT "+strings.Join(database.QuotedColumns(s, columns), ", ")+" FROM "+s.TableIdentifier("workflow_process_events")+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(1)+" AND "+s.Identifier("process_id")+" = "+s.Placeholder(2)+" ORDER BY "+s.Identifier("created_at")+" ASC LIMIT "+s.Placeholder(3), workspaceID, processID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []workflowmodel.WorkflowProcessEvent{}
	for rows.Next() {
		var event workflowmodel.WorkflowProcessEvent
		var metadata string
		var nodeID, taskID sql.NullString
		if err := rows.Scan(&event.WorkspaceID, &event.ID, &event.ProcessID, &nodeID, &taskID, &event.Event, &event.ActorID, &event.Summary, &metadata, &event.CreatedAt); err != nil {
			return nil, err
		}
		event.NodeID = nodeID.String
		event.TaskID = taskID.String
		_ = json.Unmarshal([]byte(metadata), &event.Metadata)
		out = append(out, event)
	}
	return out, rows.Err()
}

func (r WorkflowWorkerStore) UpdateTask(ctx context.Context, workspaceID string, task workflowmodel.WorkflowTask) error {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return err
	}
	columns := []string{"assignee_user_id", "assignee_name", "assignee_role_key", "status", "decision", "comment", "due_at", "completed_by", "completed_at", "updated_at"}
	values := []any{task.AssigneeUserID, task.AssigneeName, task.AssigneeRoleKey, task.Status, task.Decision, task.Comment, database.NullableText(task.DueAt), task.CompletedBy, database.NullableText(task.CompletedAt), task.UpdatedAt}
	return r.updateRow(ctx, "workflow_tasks", workspaceID, task.ID, columns, values)
}

func (r WorkflowWorkerStore) InsertProcessEvent(ctx context.Context, workspaceID string, event workflowmodel.WorkflowProcessEvent) error {
	var err error
	if workspaceID, err = requireWorkflowWorkspaceID(workspaceID); err != nil {
		return err
	}
	event.WorkspaceID = workspaceID
	s := r.store
	metadata, _ := json.Marshal(database.NonNilMap(event.Metadata))
	columns := workflowEventColumns
	values := []any{event.WorkspaceID, event.ID, event.ProcessID, event.NodeID, event.TaskID, event.Event, event.ActorID, event.Summary, string(metadata), event.CreatedAt}
	_, err = r.database().ExecContext(ctx, "INSERT INTO "+s.TableIdentifier("workflow_process_events")+" ("+stringsJoinIdentifiers(s, columns...)+") VALUES ("+stringsJoinPlaceholders(s, len(columns))+")", values...)
	if err != nil {
		return fmt.Errorf("insert workflow_process_events: %w", err)
	}
	return nil
}

func (r WorkflowWorkerStore) updateRow(ctx context.Context, table, workspaceID, id string, columns []string, values []any) error {
	s := r.store
	assignments := make([]string, 0, len(columns))
	for i, column := range columns {
		assignments = append(assignments, s.Identifier(column)+" = "+s.Placeholder(i+1))
	}
	values = append(values, workspaceID, id)
	result, err := r.database().ExecContext(ctx, "UPDATE "+s.TableIdentifier(table)+" SET "+strings.Join(assignments, ", ")+" WHERE "+s.Identifier("workspace_id")+" = "+s.Placeholder(len(values)-1)+" AND "+s.Identifier("id")+" = "+s.Placeholder(len(values)), values...)
	if err != nil {
		return fmt.Errorf("update %s: %w", table, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("inspect %s update: %w", table, err)
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}
