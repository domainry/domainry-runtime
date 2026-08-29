// Workflow process persistence.
package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/builder"
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
	return r.store.InsertSystemRowContext(ctx, "workflow_process_instances", workflowProcessColumns, workflowProcessValues(process))
}
func (r WorkflowProcessStore) UpdateProcess(ctx context.Context, workspaceID string, process workflowmodel.WorkflowProcessInstance) error {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return err
	}
	process.WorkspaceID = workspaceID
	values := workflowProcessValues(process)
	result, err := r.database().ExecContext(ctx, "UPDATE "+r.store.TableIdentifier("workflow_process_instances")+" SET "+workflowAssignments(r.store, workflowProcessColumns[2:], 1)+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(len(values)-1)+" AND "+r.store.Identifier("id")+" = "+r.store.Placeholder(len(values)), append(values[2:], workspaceID, process.ID)...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}
func (r WorkflowProcessStore) GetProcess(ctx context.Context, workspaceID, id string) (workflowmodel.WorkflowProcessInstance, bool, error) {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return workflowmodel.WorkflowProcessInstance{}, false, err
	}
	row := r.database().QueryRowContext(ctx, "SELECT "+strings.Join(database.QuotedColumns(r.store, workflowProcessColumns), ", ")+" FROM "+r.store.TableIdentifier("workflow_process_instances")+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(1)+" AND "+r.store.Identifier("id")+" = "+r.store.Placeholder(2), workspaceID, id)
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
	clauses, args := []string{r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1)}, []any{workspaceID}
	for _, item := range []struct{ column, value string }{{"id", filter.ProcessID}, {"status", filter.Status}, {"workflow_key", filter.WorkflowKey}, {"object_key", filter.ObjectKey}, {"record_id", filter.RecordID}, {"initiator_id", filter.InitiatorID}} {
		if value := strings.TrimSpace(item.value); value != "" {
			args = append(args, value)
			clauses = append(clauses, r.store.Identifier(item.column)+" = "+r.store.Placeholder(len(args)))
		}
	}
	if len(filter.Statuses) > 0 {
		placeholders := make([]string, 0, len(filter.Statuses))
		for _, rawStatus := range filter.Statuses {
			if status := strings.TrimSpace(rawStatus); status != "" {
				args = append(args, status)
				placeholders = append(placeholders, r.store.Placeholder(len(args)))
			}
		}
		if len(placeholders) > 0 {
			clauses = append(clauses, r.store.Identifier("status")+" IN ("+strings.Join(placeholders, ", ")+")")
		}
	}
	if filter.DefinitionVersion > 0 {
		args = append(args, filter.DefinitionVersion)
		clauses = append(clauses, r.store.Identifier("definition_version")+" = "+r.store.Placeholder(len(args)))
	}
	if approverID := strings.TrimSpace(filter.ApproverID); approverID != "" {
		args = append(args, approverID)
		clauses = append(clauses, "EXISTS (SELECT 1 FROM "+r.store.TableIdentifier("workflow_tasks")+" AS "+r.store.Identifier("approver_task")+
			" WHERE "+r.store.Identifier("approver_task")+"."+r.store.Identifier("workspace_id")+" = "+r.store.Identifier("workflow_process_instances")+"."+r.store.Identifier("workspace_id")+
			" AND "+r.store.Identifier("approver_task")+"."+r.store.Identifier("process_id")+" = "+r.store.Identifier("workflow_process_instances")+"."+r.store.Identifier("id")+
			" AND "+r.store.Identifier("approver_task")+"."+r.store.Identifier("assignee_user_id")+" = "+r.store.Placeholder(len(args))+")")
	}
	if visibleToUserID := strings.TrimSpace(filter.VisibleToUserID); visibleToUserID != "" {
		args = append(args, visibleToUserID)
		initiatorPlaceholder := r.store.Placeholder(len(args))
		args = append(args, visibleToUserID)
		assigneePlaceholder := r.store.Placeholder(len(args))
		clauses = append(clauses, "("+r.store.Identifier("initiator_id")+" = "+initiatorPlaceholder+
			" OR EXISTS (SELECT 1 FROM "+r.store.TableIdentifier("workflow_tasks")+" AS "+r.store.Identifier("visible_task")+
			" WHERE "+r.store.Identifier("visible_task")+"."+r.store.Identifier("workspace_id")+" = "+r.store.Identifier("workflow_process_instances")+"."+r.store.Identifier("workspace_id")+
			" AND "+r.store.Identifier("visible_task")+"."+r.store.Identifier("process_id")+" = "+r.store.Identifier("workflow_process_instances")+"."+r.store.Identifier("id")+
			" AND "+r.store.Identifier("visible_task")+"."+r.store.Identifier("assignee_user_id")+" = "+assigneePlaceholder+"))")
	}
	for _, item := range []struct{ value, operator string }{{filter.UpdatedFrom, ">="}, {filter.UpdatedTo, "<="}} {
		if value := strings.TrimSpace(item.value); value != "" {
			args = append(args, value)
			clauses = append(clauses, r.store.Identifier("updated_at")+" "+item.operator+" "+r.store.Placeholder(len(args)))
		}
	}
	where := " WHERE " + strings.Join(clauses, " AND ")
	args = append(args, limit)
	rows, err := r.database().QueryContext(ctx, "SELECT "+strings.Join(database.QuotedColumns(r.store, workflowProcessColumns), ", ")+" FROM "+r.store.TableIdentifier("workflow_process_instances")+where+" ORDER BY "+r.store.Identifier("created_at")+" DESC LIMIT "+r.store.Placeholder(len(args)), args...)
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
	return r.store.InsertSystemRowContext(ctx, "workflow_node_instances", workflowNodeColumns, workflowNodeValues(node))
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
	rows, err := r.database().QueryContext(ctx, "SELECT "+strings.Join(database.QuotedColumns(r.store, workflowNodeColumns), ", ")+" FROM "+r.store.TableIdentifier("workflow_node_instances")+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(1)+" AND "+r.store.Identifier("process_id")+" = "+r.store.Placeholder(2)+" ORDER BY "+r.store.Identifier("started_at")+", "+r.store.Identifier("node_id"), workspaceID, processID)
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
	args := []any{workspaceID}
	placeholders := make([]string, 0, len(processIDs))
	for _, processID := range processIDs {
		processID = strings.TrimSpace(processID)
		if processID == "" {
			continue
		}
		args = append(args, processID)
		placeholders = append(placeholders, r.store.Placeholder(len(args)))
	}
	if len(placeholders) == 0 {
		return nil, nil
	}
	query := "SELECT " + strings.Join(database.QuotedColumns(r.store, workflowNodeColumns), ", ") + " FROM " + r.store.TableIdentifier("workflow_node_instances") +
		" WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1) +
		" AND " + r.store.Identifier("process_id") + " IN (" + strings.Join(placeholders, ", ") + ")" +
		" ORDER BY " + r.store.Identifier("process_id") + ", " + r.store.Identifier("started_at") + ", " + r.store.Identifier("node_id")
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
	row := r.database().QueryRowContext(ctx, "SELECT "+strings.Join(database.QuotedColumns(r.store, workflowTaskColumns()), ", ")+" FROM "+r.store.TableIdentifier("workflow_tasks")+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(1)+" AND "+r.store.Identifier("id")+" = "+r.store.Placeholder(2), workspaceID, id)
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
	clauses, args := []string{r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1)}, []any{workspaceID}
	for _, item := range []struct{ column, value string }{{"process_id", processID}, {"assignee_user_id", userID}, {"status", status}} {
		if value := strings.TrimSpace(item.value); value != "" {
			args = append(args, value)
			clauses = append(clauses, r.store.Identifier(item.column)+" = "+r.store.Placeholder(len(args)))
		}
	}
	where := " WHERE " + strings.Join(clauses, " AND ")
	args = append(args, limit)
	rows, err := r.database().QueryContext(ctx, "SELECT "+strings.Join(database.QuotedColumns(r.store, workflowTaskColumns()), ", ")+" FROM "+r.store.TableIdentifier("workflow_tasks")+where+" ORDER BY "+r.store.Identifier("created_at")+" DESC LIMIT "+r.store.Placeholder(len(args)), args...)
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
	query := "UPDATE " + r.store.TableIdentifier("workflow_tasks") + " SET " + r.store.Identifier("status") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("decision") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("comment") + " = " + r.store.Placeholder(3) + ", " + r.store.Identifier("completed_by") + " = " + r.store.Placeholder(4) + ", " + r.store.Identifier("completed_at") + " = " + r.store.Placeholder(5) + ", " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(6) + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(7) + " AND " + r.store.Identifier("id") + " = " + r.store.Placeholder(8) + " AND " + r.store.Identifier("assignee_user_id") + " = " + r.store.Placeholder(9) + " AND " + r.store.Identifier("status") + " = 'open'"
	result, err := r.database().ExecContext(ctx, query, decision, decision, comment, assigneeUserID, completedAt, completedAt, workspaceID, taskID, assigneeUserID)
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
	return r.store.InsertSystemRowContext(ctx, "workflow_process_events", workflowEventColumns, []any{event.WorkspaceID, event.ID, event.ProcessID, event.NodeID, event.TaskID, event.Event, event.ActorID, event.Summary, string(metadata), event.CreatedAt})
}
func (r WorkflowProcessStore) ListEvents(ctx context.Context, workspaceID, processID string, limit int) ([]workflowmodel.WorkflowProcessEvent, error) {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := r.database().QueryContext(ctx, "SELECT "+strings.Join(database.QuotedColumns(r.store, workflowEventColumns), ", ")+" FROM "+r.store.TableIdentifier("workflow_process_events")+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(1)+" AND "+r.store.Identifier("process_id")+" = "+r.store.Placeholder(2)+" ORDER BY "+r.store.Identifier("created_at")+" ASC LIMIT "+r.store.Placeholder(3), workspaceID, processID, limit)
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
	result, err := r.database().ExecContext(ctx, "UPDATE "+r.store.TableIdentifier(table)+" SET "+workflowAssignments(r.store, columns, 1)+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(len(values)+1)+" AND "+r.store.Identifier("id")+" = "+r.store.Placeholder(len(values)+2), append(values, workspaceID, id)...)
	if err != nil {
		return err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func workflowAssignments(store *database.RuntimeStore, columns []string, offset int) string {
	assignments := make([]string, 0, len(columns))
	for index, column := range columns {
		assignments = append(assignments, store.Identifier(column)+" = "+store.Placeholder(offset+index))
	}
	return strings.Join(assignments, ", ")
}
