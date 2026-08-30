// Workflow process persistence.
package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/query"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type WorkflowProcessStore struct {
	store *database.RuntimeStore
	db    workflowDatabase
}

func NewWorkflowProcessStore(store *database.RuntimeStore) WorkflowProcessStore {
	return WorkflowProcessStore{store: store}
}

func (r WorkflowProcessStore) database() workflowDatabase {
	if r.db != nil {
		return r.db
	}
	return r.store.DB()
}

var workflowProcessColumns = []string{"workspace_id", "id", "workflow_key", "workflow_name", "workflow_definition_version_id", "definition_version", "definition_hash", "definition_json", "object_key", "record_id", "initiator_id", "initiator_role_key", "status", "current_node_ids_json", "variables_json", "result_json", "error_code", "created_at", "updated_at", "completed_at"}
var workflowNodeColumns = []string{"workspace_id", "id", "process_id", "node_id", "node_type", "iteration", "status", "input_json", "output_json", "error_code", "started_at", "completed_at"}
var workflowEventColumns = []string{"workspace_id", "id", "process_id", "node_id", "task_id", "event", "actor_id", "summary", "metadata_json", "created_at"}

func workflowProcessValues(process workflowmodel.WorkflowProcessInstance) []any {
	definition, _ := json.Marshal(process.DefinitionSnapshot)
	currentNodes, _ := json.Marshal(process.CurrentNodeIDs)
	variables, _ := json.Marshal(database.NonNilMap(process.Variables))
	result, _ := json.Marshal(database.NonNilMap(process.Result))
	return []any{process.WorkspaceID, process.ID, process.WorkflowKey, process.WorkflowName, process.DefinitionVersionID, process.DefinitionVersion, process.DefinitionHash, string(definition), process.ObjectKey, process.RecordID, process.InitiatorID, process.InitiatorRoleKey, process.Status, string(currentNodes), string(variables), string(result), process.ErrorCode, process.CreatedAt, process.UpdatedAt, database.NullableText(process.CompletedAt)}
}

func (r WorkflowProcessStore) InsertProcess(ctx context.Context, workspaceID string, process workflowmodel.WorkflowProcessInstance) error {
	var err error
	if workspaceID, err = requireWorkflowWorkspaceID(workspaceID); err != nil {
		return err
	}
	process.WorkspaceID = workspaceID
	values := workflowProcessValues(process)
	query, args, err := ormbuilder.NewWorkspaceInsertBuilder(r.store.SQLRenderer, "workflow_process_instances", workspaceID).Columns(workflowProcessColumns[1:]...).Values(values[1:]...).Build()
	if err != nil {
		return fmt.Errorf("build workflow process insert: %w", err)
	}
	_, err = r.database().ExecContext(ctx, query, args...)
	return err
}
func (r WorkflowProcessStore) UpdateProcess(ctx context.Context, workspaceID string, process workflowmodel.WorkflowProcessInstance) error {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return err
	}
	process.WorkspaceID = workspaceID
	values := workflowProcessValues(process)
	return r.updateScopedRow(ctx, "workflow_process_instances", workspaceID, process.ID, workflowProcessColumns[2:], values[2:])
}
func (r WorkflowProcessStore) GetProcess(ctx context.Context, workspaceID, id string) (workflowmodel.WorkflowProcessInstance, bool, error) {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return workflowmodel.WorkflowProcessInstance{}, false, err
	}
	query, args, err := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "workflow_process_instances", workspaceID).Columns(workflowProcessColumns...).Where(ormbuilder.Equal("id", id)).Build()
	if err != nil {
		return workflowmodel.WorkflowProcessInstance{}, false, err
	}
	row := r.database().QueryRowContext(ctx, query, args...)
	value, err := scanWorkflowProcess(row)
	if err == sql.ErrNoRows {
		return workflowmodel.WorkflowProcessInstance{}, false, nil
	}
	return value, err == nil, err
}
func (r WorkflowProcessStore) ListProcesses(ctx context.Context, workspaceID string, filter workflowmodel.WorkflowProcessFilter) ([]workflowmodel.WorkflowProcessInstance, error) {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	predicates := []ormbuilder.Predicate{}
	for _, item := range []struct{ column, value string }{{"id", filter.ProcessID}, {"status", filter.Status}, {"workflow_key", filter.WorkflowKey}, {"object_key", filter.ObjectKey}, {"record_id", filter.RecordID}, {"initiator_id", filter.InitiatorID}} {
		if value := strings.TrimSpace(item.value); value != "" {
			predicates = append(predicates, ormbuilder.Equal(item.column, value))
		}
	}
	if len(filter.Statuses) > 0 {
		statuses := make([]any, 0, len(filter.Statuses))
		for _, rawStatus := range filter.Statuses {
			if status := strings.TrimSpace(rawStatus); status != "" {
				statuses = append(statuses, status)
			}
		}
		if len(statuses) > 0 {
			predicates = append(predicates, ormbuilder.In("status", statuses...))
		}
	}
	if filter.DefinitionVersion > 0 {
		predicates = append(predicates, ormbuilder.Equal("definition_version", filter.DefinitionVersion))
	}
	if approverID := strings.TrimSpace(filter.ApproverID); approverID != "" {
		approver := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "workflow_tasks", workspaceID).Alias("approver_task").Columns("id").Where(ormbuilder.And(
			ormbuilder.EqualExpressions(ormbuilder.QualifiedColumn("approver_task", "process_id"), ormbuilder.QualifiedColumn("workflow_process_instances", "id")),
			ormbuilder.EqualValue(ormbuilder.QualifiedColumn("approver_task", "assignee_user_id"), approverID),
		))
		predicates = append(predicates, ormbuilder.ExistsSubquery(approver))
	}
	if visibleToUserID := strings.TrimSpace(filter.VisibleToUserID); visibleToUserID != "" {
		visible := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "workflow_tasks", workspaceID).Alias("visible_task").Columns("id").Where(ormbuilder.And(
			ormbuilder.EqualExpressions(ormbuilder.QualifiedColumn("visible_task", "process_id"), ormbuilder.QualifiedColumn("workflow_process_instances", "id")),
			ormbuilder.EqualValue(ormbuilder.QualifiedColumn("visible_task", "assignee_user_id"), visibleToUserID),
		))
		predicates = append(predicates, ormbuilder.Or(ormbuilder.Equal("initiator_id", visibleToUserID), ormbuilder.ExistsSubquery(visible)))
	}
	if value := strings.TrimSpace(filter.UpdatedFrom); value != "" {
		predicates = append(predicates, ormbuilder.GreaterThanOrEqual("updated_at", value))
	}
	if value := strings.TrimSpace(filter.UpdatedTo); value != "" {
		predicates = append(predicates, ormbuilder.LessThanOrEqual("updated_at", value))
	}
	builder := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "workflow_process_instances", workspaceID).Columns(workflowProcessColumns...).OrderBy(ormbuilder.Descending("created_at"), ormbuilder.Descending("id")).Limit(limit)
	if len(predicates) > 0 {
		builder.Where(ormbuilder.And(predicates...))
	}
	query, args, err := builder.Build()
	if err != nil {
		return nil, fmt.Errorf("build workflow process list: %w", err)
	}
	rows, err := r.database().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []workflowmodel.WorkflowProcessInstance{}
	for rows.Next() {
		value, err := scanWorkflowProcess(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func workflowNodeValues(node workflowmodel.WorkflowNodeInstance) []any {
	input, _ := json.Marshal(database.NonNilMap(node.Input))
	output, _ := json.Marshal(database.NonNilMap(node.Output))
	return []any{node.WorkspaceID, node.ID, node.ProcessID, node.NodeID, node.NodeType, node.Iteration, node.Status, string(input), string(output), node.ErrorCode, node.StartedAt, database.NullableText(node.CompletedAt)}
}
func (r WorkflowProcessStore) InsertNode(ctx context.Context, workspaceID string, node workflowmodel.WorkflowNodeInstance) error {
	var err error
	if workspaceID, err = requireWorkflowWorkspaceID(workspaceID); err != nil {
		return err
	}
	node.WorkspaceID = workspaceID
	values := workflowNodeValues(node)
	query, args, err := ormbuilder.NewWorkspaceInsertBuilder(r.store.SQLRenderer, "workflow_node_instances", workspaceID).Columns(workflowNodeColumns[1:]...).Values(values[1:]...).Build()
	if err != nil {
		return fmt.Errorf("build workflow node insert: %w", err)
	}
	_, err = r.database().ExecContext(ctx, query, args...)
	return err
}
func (r WorkflowProcessStore) UpdateNode(ctx context.Context, workspaceID string, node workflowmodel.WorkflowNodeInstance) error {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return err
	}
	values := workflowNodeValues(node)
	return r.updateScopedRow(ctx, "workflow_node_instances", workspaceID, node.ID, []string{"status", "input_json", "output_json", "error_code", "completed_at"}, []any{values[6], values[7], values[8], values[9], values[11]})
}
func (r WorkflowProcessStore) ListNodes(ctx context.Context, workspaceID, processID string) ([]workflowmodel.WorkflowNodeInstance, error) {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	query, args, err := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "workflow_node_instances", workspaceID).Columns(workflowNodeColumns...).Where(ormbuilder.Equal("process_id", processID)).OrderBy(ormbuilder.Ascending("started_at"), ormbuilder.Ascending("node_id"), ormbuilder.Ascending("id")).Build()
	if err != nil {
		return nil, err
	}
	rows, err := r.database().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []workflowmodel.WorkflowNodeInstance{}
	for rows.Next() {
		var node workflowmodel.WorkflowNodeInstance
		var input, output string
		var errorCode, completedAt sql.NullString
		if err := rows.Scan(&node.WorkspaceID, &node.ID, &node.ProcessID, &node.NodeID, &node.NodeType, &node.Iteration, &node.Status, &input, &output, &errorCode, &node.StartedAt, &completedAt); err != nil {
			return nil, err
		}
		node.ErrorCode, node.CompletedAt = errorCode.String, completedAt.String
		_ = json.Unmarshal([]byte(input), &node.Input)
		_ = json.Unmarshal([]byte(output), &node.Output)
		out = append(out, node)
	}
	return out, rows.Err()
}

func (r WorkflowProcessStore) ListNodesForProcesses(ctx context.Context, workspaceID string, processIDs []string) ([]workflowmodel.WorkflowNodeInstance, error) {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil || len(processIDs) == 0 {
		return nil, err
	}
	values := make([]any, 0, len(processIDs))
	for _, processID := range processIDs {
		processID = strings.TrimSpace(processID)
		if processID == "" {
			continue
		}
		values = append(values, processID)
	}
	if len(values) == 0 {
		return nil, nil
	}
	query, args, err := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "workflow_node_instances", workspaceID).Columns(workflowNodeColumns...).Where(ormbuilder.In("process_id", values...)).OrderBy(ormbuilder.Ascending("process_id"), ormbuilder.Ascending("started_at"), ormbuilder.Ascending("node_id"), ormbuilder.Ascending("id")).Build()
	if err != nil {
		return nil, err
	}
	rows, err := r.database().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []workflowmodel.WorkflowNodeInstance{}
	for rows.Next() {
		node, scanErr := scanWorkflowNode(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, node)
	}
	return out, rows.Err()
}

func scanWorkflowNode(scanner interface{ Scan(...any) error }) (workflowmodel.WorkflowNodeInstance, error) {
	var node workflowmodel.WorkflowNodeInstance
	var input, output string
	var errorCode, completedAt sql.NullString
	err := scanner.Scan(&node.WorkspaceID, &node.ID, &node.ProcessID, &node.NodeID, &node.NodeType, &node.Iteration, &node.Status, &input, &output, &errorCode, &node.StartedAt, &completedAt)
	node.ErrorCode, node.CompletedAt = errorCode.String, completedAt.String
	_ = json.Unmarshal([]byte(input), &node.Input)
	_ = json.Unmarshal([]byte(output), &node.Output)
	return node, err
}

func workflowTaskValues(task workflowmodel.WorkflowTask) []any {
	resolver, _ := json.Marshal(task.ResolverSnapshot)
	return []any{task.WorkspaceID, task.ID, task.ProcessID, task.NodeInstanceID, task.NodeID, task.Title, task.AssigneeUserID, task.AssigneeName, task.AssigneeRoleKey, string(resolver), task.CandidateSource, task.NodeDefinitionVersion, task.Sequence, task.Status, task.Decision, task.Comment, database.NullableText(task.DueAt), task.CompletedBy, database.NullableText(task.CompletedAt), task.CreatedAt, task.UpdatedAt}
}
func (r WorkflowProcessStore) InsertTask(ctx context.Context, workspaceID string, task workflowmodel.WorkflowTask) error {
	var err error
	if workspaceID, err = requireWorkflowWorkspaceID(workspaceID); err != nil {
		return err
	}
	task.WorkspaceID = workspaceID
	query, args, buildErr := ormbuilder.NewWorkspaceInsertBuilder(r.store.SQLRenderer, "workflow_tasks", workspaceID).
		Columns(workflowTaskColumns()[1:]...).Values(workflowTaskValues(task)[1:]...).Build()
	if buildErr != nil {
		return fmt.Errorf("build workflow task insert: %w", buildErr)
	}
	_, err = r.database().ExecContext(ctx, query, args...)
	return err
}
func (r WorkflowProcessStore) UpdateTask(ctx context.Context, workspaceID string, task workflowmodel.WorkflowTask) error {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return err
	}
	values := workflowTaskValues(task)
	return r.updateScopedRow(ctx, "workflow_tasks", workspaceID, task.ID, []string{"assignee_user_id", "assignee_name", "assignee_role_key", "status", "decision", "comment", "due_at", "completed_by", "completed_at", "updated_at"}, []any{values[6], values[7], values[8], values[13], values[14], values[15], values[16], values[17], values[18], values[20]})
}

func (r WorkflowProcessStore) UpdateTasks(ctx context.Context, workspaceID string, tasks []workflowmodel.WorkflowTask) error {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return err
	}
	byID := make(map[string]workflowmodel.WorkflowTask, len(tasks))
	order := make([]string, 0, len(tasks))
	for _, task := range tasks {
		id := strings.TrimSpace(task.ID)
		if id == "" {
			continue
		}
		if _, exists := byID[id]; !exists {
			order = append(order, id)
		}
		task.WorkspaceID = workspaceID
		byID[id] = task
	}
	if len(order) == 0 {
		return nil
	}
	tx, err := r.database().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	columns := workflowTaskColumns()[1:]
	assignments := []ormbuilder.Assignment{}
	for _, column := range []string{"assignee_user_id", "assignee_name", "assignee_role_key", "status", "decision", "comment", "due_at", "completed_by", "completed_at", "updated_at"} {
		assignments = append(assignments, ormbuilder.AssignExpression(column, ormbuilder.InsertedValue(column)))
	}
	for start := 0; start < len(order); start += 20 {
		end := min(start+20, len(order))
		insert := ormbuilder.NewWorkspaceInsertBuilder(r.store.SQLRenderer, "workflow_tasks", workspaceID).Columns(columns...)
		for _, id := range order[start:end] {
			insert.Values(slices.Clone(workflowTaskValues(byID[id])[1:])...)
		}
		insert, buildErr := r.store.Engine.ApplyUpsert(insert, []string{"workspace_id", "id"}, assignments...)
		if buildErr != nil {
			return buildErr
		}
		query, args, buildErr := insert.Build()
		if buildErr != nil {
			return buildErr
		}
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (r WorkflowProcessStore) GetTask(ctx context.Context, workspaceID, id string) (workflowmodel.WorkflowTask, bool, error) {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return workflowmodel.WorkflowTask{}, false, err
	}
	query, args, err := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "workflow_tasks", workspaceID).Columns(workflowTaskColumns()...).Where(ormbuilder.Equal("id", id)).Build()
	if err != nil {
		return workflowmodel.WorkflowTask{}, false, err
	}
	row := r.database().QueryRowContext(ctx, query, args...)
	value, err := scanWorkflowTask(row)
	if err == sql.ErrNoRows {
		return workflowmodel.WorkflowTask{}, false, nil
	}
	return value, err == nil, err
}
func (r WorkflowProcessStore) ListTasks(ctx context.Context, workspaceID, processID, userID, status string, limit int) ([]workflowmodel.WorkflowTask, error) {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	predicates := []ormbuilder.Predicate{}
	for _, item := range []struct{ column, value string }{{"process_id", processID}, {"assignee_user_id", userID}, {"status", status}} {
		if value := strings.TrimSpace(item.value); value != "" {
			predicates = append(predicates, ormbuilder.Equal(item.column, value))
		}
	}
	builder := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "workflow_tasks", workspaceID).Columns(workflowTaskColumns()...).OrderBy(ormbuilder.Descending("created_at"), ormbuilder.Descending("id")).Limit(limit)
	if len(predicates) > 0 {
		builder.Where(ormbuilder.And(predicates...))
	}
	query, args, err := builder.Build()
	if err != nil {
		return nil, err
	}
	rows, err := r.database().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []workflowmodel.WorkflowTask{}
	for rows.Next() {
		value, err := scanWorkflowTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func (r WorkflowProcessStore) DecideTask(ctx context.Context, workspaceID, taskID, assigneeUserID, decision, comment, completedAt string) (workflowmodel.WorkflowTask, bool, error) {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return workflowmodel.WorkflowTask{}, false, err
	}
	query, args, err := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "workflow_tasks", workspaceID).Set("status", decision).Set("decision", decision).Set("comment", comment).Set("completed_by", assigneeUserID).Set("completed_at", completedAt).Set("updated_at", completedAt).Where(ormbuilder.And(ormbuilder.Equal("id", taskID), ormbuilder.Equal("assignee_user_id", assigneeUserID), ormbuilder.Equal("status", "open"))).Build()
	if err != nil {
		return workflowmodel.WorkflowTask{}, false, err
	}
	result, err := r.database().ExecContext(ctx, query, args...)
	if err != nil {
		return workflowmodel.WorkflowTask{}, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return workflowmodel.WorkflowTask{}, false, err
	}
	if affected == 0 {
		return workflowmodel.WorkflowTask{}, false, nil
	}
	return r.GetTask(ctx, workspaceID, taskID)
}

func (r WorkflowProcessStore) InsertEvent(ctx context.Context, workspaceID string, event workflowmodel.WorkflowProcessEvent) error {
	var err error
	if workspaceID, err = requireWorkflowWorkspaceID(workspaceID); err != nil {
		return err
	}
	event.WorkspaceID = workspaceID
	metadata, _ := json.Marshal(database.NonNilMap(event.Metadata))
	values := []any{event.ID, event.ProcessID, event.NodeID, event.TaskID, event.Event, event.ActorID, event.Summary, string(metadata), event.CreatedAt}
	query, args, err := ormbuilder.NewWorkspaceInsertBuilder(r.store.SQLRenderer, "workflow_process_events", workspaceID).Columns(workflowEventColumns[1:]...).Values(values...).Build()
	if err != nil {
		return fmt.Errorf("build workflow process event insert: %w", err)
	}
	_, err = r.database().ExecContext(ctx, query, args...)
	return err
}
func (r WorkflowProcessStore) ListEvents(ctx context.Context, workspaceID, processID string, limit int) ([]workflowmodel.WorkflowProcessEvent, error) {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	query, args, err := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "workflow_process_events", workspaceID).Columns(workflowEventColumns...).Where(ormbuilder.Equal("process_id", processID)).OrderBy(ormbuilder.Ascending("created_at"), ormbuilder.Ascending("id")).Limit(limit).Build()
	if err != nil {
		return nil, err
	}
	rows, err := r.database().QueryContext(ctx, query, args...)
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
		event.NodeID, event.TaskID = nodeID.String, taskID.String
		_ = json.Unmarshal([]byte(metadata), &event.Metadata)
		out = append(out, event)
	}
	return out, rows.Err()
}

func (r WorkflowProcessStore) updateScopedRow(ctx context.Context, table, workspaceID, id string, columns []string, values []any) error {
	if len(columns) != len(values) {
		return fmt.Errorf("workflow %s update columns=%d values=%d", table, len(columns), len(values))
	}
	builder := ormbuilder.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, table, workspaceID)
	for index, column := range columns {
		builder.Set(column, values[index])
	}
	query, args, err := builder.Where(ormbuilder.Equal("id", id)).Build()
	if err != nil {
		return err
	}
	result, err := r.database().ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}
