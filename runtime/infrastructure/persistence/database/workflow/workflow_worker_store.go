package workflow

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/domainry/domainry-orm/query"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/timevalue"
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
	_, scopedColumns, scopedValues, err := workflowScopedInsert(columns, values)
	if err != nil {
		return err
	}
	if execution.ProcessID != "" && execution.Status != "cancelled" {
		if err := r.guardExecutionProcessTx(ctx, tx, workspaceID, execution.ProcessID); err != nil {
			return err
		}
	}
	if err := r.store.GuardSubjectEvidenceWrite(ctx, tx, workspaceID, "_workflow_executions", columns, values); err != nil {
		return err
	}
	statement, args, err := query.NewWorkspaceInsertBuilder(r.store.SQLRenderer, "_workflow_executions", workspaceID).Columns(scopedColumns...).Values(scopedValues...).Build()
	if err != nil {
		return fmt.Errorf("build workflow execution insert: %w", err)
	}
	if _, err := tx.ExecContext(ctx, statement, args...); err != nil {
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
	queryValue, args, err := query.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "_workflow_executions", workspaceID).Columns(workflowExecutionColumns()...).Where(query.Equal("id", executionID)).Build()
	if err != nil {
		return workflowmodel.WorkflowExecution{}, false, fmt.Errorf("build workflow execution lookup: %w", err)
	}
	row := r.database().QueryRowContext(ctx, queryValue, args...)
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
	if limit <= 0 {
		limit = 100
	} else if limit > 1000 {
		limit = 1000
	}
	queryValue, args, err := query.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "_workflow_executions", workspaceID).Columns(workflowExecutionColumns()...).OrderBy(query.Descending("created_at")).Limit(limit).Build()
	if err != nil {
		return nil, fmt.Errorf("build workflow execution list: %w", err)
	}
	rows, err := r.database().QueryContext(ctx, queryValue, args...)
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
	updated, err := r.UpdateExecutionWhere(ctx, workspaceID, execution, nil)
	if err != nil {
		return err
	}
	if !updated {
		return sql.ErrNoRows
	}
	return nil
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
	builder := query.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "_workflow_executions", workspaceID)
	for i, column := range columns {
		builder.Set(column, values[i])
	}
	predicates := []query.Predicate{query.Equal("id", execution.ID), r.store.SubjectEvidenceWriteAllowed(workspaceID, "_workflow_executions", execution.ID)}
	if execution.Status != "cancelled" {
		predicates = append(predicates, query.NotEqual("status", "cancelled"))
	}
	keys := make([]string, 0, len(conditions))
	for key := range conditions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		predicates = append(predicates, query.Equal(key, workflowExecutionConditionValue(key, conditions[key])))
	}
	statement, args, buildErr := builder.Where(query.And(predicates...)).Build()
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
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	predicates := []query.Predicate{}
	filters := []struct{ column, value string }{{"process_id", processID}, {"assignee_user_id", assigneeUserID}, {"status", status}}
	for _, filter := range filters {
		if strings.TrimSpace(filter.value) != "" {
			predicates = append(predicates, query.Equal(filter.column, strings.TrimSpace(filter.value)))
		}
	}
	builder := query.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "_workflow_tasks", workspaceID).Columns(workflowTaskColumns()...).OrderBy(query.Descending("created_at")).Limit(limit)
	if len(predicates) > 0 {
		builder.Where(query.And(predicates...))
	}
	queryValue, args, err := builder.Build()
	if err != nil {
		return nil, fmt.Errorf("build workflow task list: %w", err)
	}
	rows, err := r.database().QueryContext(ctx, queryValue, args...)
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
	columns := workflowProcessColumns
	queryValue, args, err := query.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "_workflow_process_instances", workspaceID).Columns(columns...).Where(query.Equal("id", processID)).Build()
	if err != nil {
		return workflowmodel.WorkflowProcessInstance{}, false, fmt.Errorf("build workflow process lookup: %w", err)
	}
	row := r.database().QueryRowContext(ctx, queryValue, args...)
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
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	columns := workflowEventColumns
	queryValue, args, err := query.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "_workflow_process_events", workspaceID).Columns(columns...).Where(query.Equal("process_id", processID)).OrderBy(query.Ascending("created_at")).Limit(limit).Build()
	if err != nil {
		return nil, fmt.Errorf("build workflow process event list: %w", err)
	}
	rows, err := r.database().QueryContext(ctx, queryValue, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []workflowmodel.WorkflowProcessEvent{}
	for rows.Next() {
		var event workflowmodel.WorkflowProcessEvent
		var metadata string
		var nodeID, taskID sql.NullString
		var createdAt int64
		if err := rows.Scan(&event.WorkspaceID, &event.ID, &event.ProcessID, &nodeID, &taskID, &event.Event, &event.ActorID, &event.Summary, &metadata, &createdAt); err != nil {
			return nil, err
		}
		event.NodeID = nodeID.String
		event.TaskID = taskID.String
		event.CreatedAt = timevalue.String(createdAt)
		_ = database.UnmarshalTimeJSON([]byte(metadata), &event.Metadata)
		out = append(out, event)
	}
	return out, rows.Err()
}

func (r WorkflowWorkerStore) UpdateTask(ctx context.Context, workspaceID string, task workflowmodel.WorkflowTask) error {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return err
	}
	evidence, _ := database.MarshalTimeJSON(task.AssigneeEvidence)
	columns := []string{"assignee_user_id", "assignee_name", "assignee_role_key", "assignee_resolver_key", "assignee_evidence_json", "status", "decision", "comment", "due_at", "completed_by", "completed_at", "updated_at"}
	values := []any{task.AssigneeUserID, task.AssigneeName, task.AssigneeRoleKey, task.AssigneeResolverKey, string(evidence), task.Status, task.Decision, task.Comment, timevalue.Millis(task.DueAt), task.CompletedBy, timevalue.Millis(task.CompletedAt), timevalue.Millis(task.UpdatedAt)}
	return r.updateRow(ctx, "_workflow_tasks", workspaceID, task.ID, columns, values)
}

func (r WorkflowWorkerStore) InsertProcessEvent(ctx context.Context, workspaceID string, event workflowmodel.WorkflowProcessEvent) error {
	var err error
	if workspaceID, err = requireWorkflowWorkspaceID(workspaceID); err != nil {
		return err
	}
	event.WorkspaceID = workspaceID
	metadata, _ := database.MarshalTimeJSON(database.NonNilMap(event.Metadata))
	columns := workflowEventColumns
	values := []any{event.WorkspaceID, event.ID, event.ProcessID, event.NodeID, event.TaskID, event.Event, event.ActorID, event.Summary, string(metadata), timevalue.Millis(event.CreatedAt)}
	_, scopedColumns, scopedValues, err := workflowScopedInsert(columns, values)
	if err != nil {
		return err
	}
	if err := r.store.GuardSubjectEvidenceWrite(ctx, r.database(), workspaceID, "_workflow_process_events", columns, values); err != nil {
		return err
	}
	queryValue, args, err := query.NewWorkspaceInsertBuilder(r.store.SQLRenderer, "_workflow_process_events", workspaceID).Columns(scopedColumns...).Values(scopedValues...).Build()
	if err == nil {
		_, err = r.database().ExecContext(ctx, queryValue, args...)
	}
	if err != nil {
		return fmt.Errorf("insert _workflow_process_events: %w", err)
	}
	return nil
}

func (r WorkflowWorkerStore) updateRow(ctx context.Context, table, workspaceID, id string, columns []string, values []any) error {
	if len(columns) != len(values) {
		return fmt.Errorf("update %s: columns=%d values=%d", table, len(columns), len(values))
	}
	builder := query.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, table, workspaceID)
	for i, column := range columns {
		builder.Set(column, values[i])
	}
	predicates := []query.Predicate{query.Equal("id", id), r.store.SubjectEvidenceWriteAllowed(workspaceID, table, id)}
	for i, column := range columns {
		if column == "status" && values[i] != "cancelled" {
			predicates = append(predicates, query.NotEqual("status", "cancelled"))
		}
	}
	queryValue, args, err := builder.Where(query.And(predicates...)).Build()
	if err != nil {
		return fmt.Errorf("build %s update: %w", table, err)
	}
	result, err := r.database().ExecContext(ctx, queryValue, args...)
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

// Lock the process before inserting a late engine receipt, following the same
// process-then-execution lock order as withdrawal. Missing process IDs remain
// valid for execution intents that have not created a process yet.
func (r WorkflowWorkerStore) guardExecutionProcessTx(ctx context.Context, tx *sql.Tx, workspaceID, processID string) error {
	statement, args, err := query.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "_workflow_process_instances", workspaceID).SetExpression("updated_at", query.Column("updated_at")).Where(query.Equal("id", processID)).Build()
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, statement, args...); err != nil {
		return err
	}
	statement, args, err = query.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "_workflow_process_instances", workspaceID).Columns("status").Where(query.Equal("id", processID)).Build()
	if err != nil {
		return err
	}
	var status string
	if err := tx.QueryRowContext(ctx, statement, args...).Scan(&status); err != nil && err != sql.ErrNoRows {
		return err
	}
	if status == "cancelled" {
		return workflowcontract.ErrWorkflowDecisionSnapshotChanged
	}
	return nil
}
