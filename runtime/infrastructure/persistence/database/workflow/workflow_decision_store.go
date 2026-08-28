// Workflow decision persistence.
package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"

	"strings"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	agentpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/agent"

	"github.com/domainry/domainry-foundation/apperror"
	notificationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/notification"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
)

type WorkflowDecisionStore struct {
	store *database.RuntimeStore
	db    workflowDatabase
}

func NewWorkflowDecisionStore(store *database.RuntimeStore) WorkflowDecisionStore {
	return WorkflowDecisionStore{store: store}
}

func (r WorkflowDecisionStore) database() workflowDatabase {
	if r.db != nil {
		return r.db
	}
	return r.store.DB()
}

func (r WorkflowDecisionStore) CommitWorkflowDecision(ctx context.Context, commit transactionmodel.WorkflowDecisionCommit) (bool, error) {
	workspaceID, err := requireWorkflowWorkspaceID(commit.WorkspaceID)
	if err != nil {
		return false, err
	}
	commit.WorkspaceID = workspaceID
	tx, err := r.database().BeginTx(ctx, recordMutationTxOptions())
	if err != nil {
		return false, fmt.Errorf("begin workflow decision: %w", err)
	}
	defer tx.Rollback()
	decided, err := r.decideTaskTx(ctx, tx, commit)
	if err != nil || !decided {
		return decided, err
	}
	for _, mutation := range commit.RecordMutations {
		if err := recordpersistence.NewRecordStore(r.store).ApplyRecordMutationTx(ctx, tx, commit.WorkspaceID, mutation); err != nil {
			return false, err
		}
	}
	for _, node := range commit.InsertNodes {
		node.WorkspaceID = workspaceID
		if err := r.insertNodeTx(ctx, tx, node); err != nil {
			return false, err
		}
	}
	for _, node := range commit.UpdateNodes {
		node.WorkspaceID = workspaceID
		if err := r.updateNodeTx(ctx, tx, node); err != nil {
			return false, err
		}
	}
	for _, task := range commit.InsertTasks {
		task.WorkspaceID = workspaceID
		if err := r.insertTaskTx(ctx, tx, task); err != nil {
			return false, err
		}
	}
	for _, task := range commit.UpdateTasks {
		task.WorkspaceID = workspaceID
		if task.ID == commit.DecidedTask.ID {
			continue
		}
		if err := r.updateTaskTx(ctx, tx, task); err != nil {
			return false, err
		}
	}
	if commit.Process != nil {
		commit.Process.WorkspaceID = workspaceID
		if err := r.updateProcessTx(ctx, tx, *commit.Process); err != nil {
			return false, err
		}
	}
	for _, event := range commit.Events {
		event.WorkspaceID = workspaceID
		if err := r.insertEventTx(ctx, tx, event); err != nil {
			return false, err
		}
	}
	for _, execution := range commit.InsertExecutions {
		execution.WorkspaceID = workspaceID
		if err := r.insertExecutionTx(ctx, tx, execution); err != nil {
			return false, err
		}
		if err := RegisterWorkflowContinuationScope(ctx, r.store, tx, workspaceID, execution.UpdatedAt); err != nil {
			return false, err
		}
	}
	for _, event := range commit.NotificationEvents {
		event.WorkspaceID = workspaceID
		if err := notificationpersistence.NewInboxEventWriter(r.store).InsertEventTx(ctx, tx, event); err != nil {
			return false, err
		}
	}
	if commit.WorkflowExecution != nil {
		commit.WorkflowExecution.WorkspaceID = workspaceID
		if err := r.updateExecutionTx(ctx, tx, *commit.WorkflowExecution); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit workflow decision: %w", err)
	}
	recordpersistence.NewRecordStore(r.store).PublishCommittedOutboxWakeups(ctx, workspaceID, commit.RecordMutations)
	recordpersistence.NewRecordStore(r.store).PublishCommittedNotificationWakeups(ctx, workspaceID, commit.RecordMutations)
	notifications := notificationpersistence.NewInboxEventWriter(r.store)
	for _, event := range commit.NotificationEvents {
		event.WorkspaceID = workspaceID
		notifications.PublishCommittedEventWakeup(event)
	}
	return true, nil
}

func (r WorkflowDecisionStore) CommitWorkflowState(ctx context.Context, commit transactionmodel.WorkflowStateCommit) error {
	workspaceID, err := requireWorkflowWorkspaceID(commit.WorkspaceID)
	if err != nil {
		return err
	}
	if len(commit.InsertAgentTasks) > 0 {
		if err := agentpersistence.NewAgentTaskRunStore(r.store).EnsureSchema(ctx); err != nil {
			return err
		}
	}
	tx, err := r.database().BeginTx(ctx, recordMutationTxOptions())
	if err != nil {
		return fmt.Errorf("begin workflow state commit: %w", err)
	}
	defer tx.Rollback()
	for _, node := range commit.InsertNodes {
		node.WorkspaceID = workspaceID
		if err := r.insertNodeTx(ctx, tx, node); err != nil {
			return err
		}
	}
	for _, node := range commit.UpdateNodes {
		node.WorkspaceID = workspaceID
		if err := r.updateNodeTx(ctx, tx, node); err != nil {
			return err
		}
	}
	for _, task := range commit.InsertAgentTasks {
		task.WorkspaceID = workspaceID
		if err := r.insertAgentTaskTx(ctx, tx, task); err != nil {
			return err
		}
		if err := agentpersistence.RegisterAgentTaskWorkerScope(ctx, r.store, tx, workspaceID, time.UnixMilli(task.UpdatedAtMillis)); err != nil {
			return err
		}
	}
	for _, task := range commit.UpdateAgentTasks {
		task.WorkspaceID = workspaceID
		if err := r.updateAgentTaskTx(ctx, tx, task); err != nil {
			return err
		}
	}
	for _, task := range commit.UpdateTasks {
		task.WorkspaceID = workspaceID
		if err := r.updateTaskTx(ctx, tx, task); err != nil {
			return err
		}
	}
	if commit.Process != nil {
		commit.Process.WorkspaceID = workspaceID
		if err := r.updateProcessTx(ctx, tx, *commit.Process); err != nil {
			return err
		}
	}
	for _, event := range commit.Events {
		event.WorkspaceID = workspaceID
		if err := r.insertEventTx(ctx, tx, event); err != nil {
			return err
		}
	}
	if commit.WorkflowExecution != nil {
		commit.WorkflowExecution.WorkspaceID = workspaceID
		if err := r.updateExecutionTx(ctx, tx, *commit.WorkflowExecution); err != nil {
			return err
		}
	}
	for _, execution := range commit.InsertExecutions {
		execution.WorkspaceID = workspaceID
		if err := r.insertExecutionTx(ctx, tx, execution); err != nil {
			return err
		}
		if err := RegisterWorkflowContinuationScope(ctx, r.store, tx, workspaceID, execution.UpdatedAt); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit workflow state: %w", err)
	}
	return nil
}

func (r WorkflowDecisionStore) insertAgentTaskTx(ctx context.Context, tx *sql.Tx, run transactionmodel.WorkflowAgentTaskCommit) error {
	columns := []string{"workspace_id", "run_id", "idempotency_key", "task_key", "process_id", "status", "lease_owner", "fencing_token", "lease_expires_at", "next_attempt_at", "payload_json", "created_at", "updated_at"}
	values := []any{run.WorkspaceID, run.RunID, run.IdempotencyKey, run.TaskKey, run.ProcessID, run.Status, run.LeaseOwner, run.FencingToken, run.LeaseExpiresAt, run.NextAttemptAt, run.Payload, run.CreatedAtMillis, run.UpdatedAtMillis}
	return r.insertTx(ctx, tx, "agent_task_runs", columns, values)
}

func (r WorkflowDecisionStore) updateAgentTaskTx(ctx context.Context, tx *sql.Tx, run transactionmodel.WorkflowAgentTaskCommit) error {
	expectedStatus := strings.TrimSpace(run.ExpectedStatus)
	if expectedStatus == "" {
		expectedStatus = "running"
	}
	query := "UPDATE " + r.store.TableIdentifier("agent_task_runs") + " SET " + r.store.Identifier("status") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("payload_json") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(3) + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(4) + " AND " + r.store.Identifier("run_id") + " = " + r.store.Placeholder(5) + " AND " + r.store.Identifier("status") + " = " + r.store.Placeholder(6)
	args := []any{run.Status, run.Payload, run.UpdatedAtMillis, run.WorkspaceID, run.RunID, expectedStatus}
	if expectedStatus == "running" {
		query += " AND " + r.store.Identifier("lease_owner") + " = " + r.store.Placeholder(7) + " AND " + r.store.Identifier("fencing_token") + " = " + r.store.Placeholder(8)
		args = append(args, run.LeaseOwner, run.FencingToken)
	}
	result, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	rows, rowsErr := result.RowsAffected()
	if rowsErr != nil {
		return rowsErr
	}
	if rows != 1 {
		return apperror.New(apperror.KindConflict, "agent.task.terminal_fence_rejected", nil, nil)
	}
	return nil
}

func recordMutationTxOptions() *sql.TxOptions {
	return &sql.TxOptions{Isolation: sql.LevelSerializable}
}

func stringsJoinIdentifiers(store *database.RuntimeStore, columns ...string) string {
	values := make([]string, 0, len(columns))
	for _, column := range columns {
		values = append(values, store.Identifier(column))
	}
	return strings.Join(values, ", ")
}

func stringsJoinPlaceholders(store *database.RuntimeStore, count int) string {
	values := make([]string, 0, count)
	for position := 1; position <= count; position++ {
		values = append(values, store.Placeholder(position))
	}
	return strings.Join(values, ", ")
}

func (r WorkflowDecisionStore) insertExecutionTx(ctx context.Context, tx *sql.Tx, execution workflowmodel.WorkflowExecution) error {
	columns, values, err := workflowExecutionInsertValues(execution)
	if err != nil {
		return err
	}
	return r.insertTx(ctx, tx, "_workflow_executions", columns, values)
}

func (r WorkflowDecisionStore) decideTaskTx(ctx context.Context, tx *sql.Tx, commit transactionmodel.WorkflowDecisionCommit) (bool, error) {
	task := commit.DecidedTask
	status := strings.TrimSpace(commit.ExpectedTaskStatus)
	if status == "" {
		status = "open"
	}
	query := "UPDATE " + r.store.TableIdentifier("workflow_tasks") + " SET " + r.store.Identifier("status") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("decision") + " = " + r.store.Placeholder(2) + ", " + r.store.Identifier("comment") + " = " + r.store.Placeholder(3) + ", " + r.store.Identifier("completed_by") + " = " + r.store.Placeholder(4) + ", " + r.store.Identifier("completed_at") + " = " + r.store.Placeholder(5) + ", " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(6) + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(7) + " AND " + r.store.Identifier("id") + " = " + r.store.Placeholder(8) + " AND " + r.store.Identifier("assignee_user_id") + " = " + r.store.Placeholder(9) + " AND " + r.store.Identifier("status") + " = " + r.store.Placeholder(10)
	result, err := tx.ExecContext(ctx, query, task.Status, task.Decision, task.Comment, task.CompletedBy, database.NullableText(task.CompletedAt), task.UpdatedAt, commit.WorkspaceID, task.ID, commit.ExpectedAssigneeID, status)
	if err != nil {
		return false, fmt.Errorf("decide workflow task: %w", err)
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (r WorkflowDecisionStore) insertNodeTx(ctx context.Context, tx *sql.Tx, node workflowmodel.WorkflowNodeInstance) error {
	input, _ := json.Marshal(database.NonNilMap(node.Input))
	output, _ := json.Marshal(database.NonNilMap(node.Output))
	columns := workflowNodeColumns
	values := []any{node.WorkspaceID, node.ID, node.ProcessID, node.NodeID, node.NodeType, node.Iteration, node.Status, string(input), string(output), node.ErrorCode, node.StartedAt, database.NullableText(node.CompletedAt)}
	return r.insertTx(ctx, tx, "workflow_node_instances", columns, values)
}

func (r WorkflowDecisionStore) updateNodeTx(ctx context.Context, tx *sql.Tx, node workflowmodel.WorkflowNodeInstance) error {
	input, _ := json.Marshal(database.NonNilMap(node.Input))
	output, _ := json.Marshal(database.NonNilMap(node.Output))
	return r.updateTx(ctx, tx, "workflow_node_instances", node.WorkspaceID, node.ID, []string{"status", "input_json", "output_json", "error_code", "completed_at"}, []any{node.Status, string(input), string(output), node.ErrorCode, database.NullableText(node.CompletedAt)})
}

func (r WorkflowDecisionStore) insertTaskTx(ctx context.Context, tx *sql.Tx, task workflowmodel.WorkflowTask) error {
	resolver, _ := json.Marshal(task.ResolverSnapshot)
	columns := workflowTaskColumns()
	values := []any{task.WorkspaceID, task.ID, task.ProcessID, task.NodeInstanceID, task.NodeID, task.Title, task.AssigneeUserID, task.AssigneeName, task.AssigneeRoleKey, string(resolver), task.CandidateSource, task.NodeDefinitionVersion, task.Sequence, task.Status, task.Decision, task.Comment, database.NullableText(task.DueAt), task.CompletedBy, database.NullableText(task.CompletedAt), task.CreatedAt, task.UpdatedAt}
	return r.insertTx(ctx, tx, "workflow_tasks", columns, values)
}

func (r WorkflowDecisionStore) updateTaskTx(ctx context.Context, tx *sql.Tx, task workflowmodel.WorkflowTask) error {
	columns := []string{"assignee_user_id", "assignee_name", "assignee_role_key", "status", "decision", "comment", "due_at", "completed_by", "completed_at", "updated_at"}
	values := []any{task.AssigneeUserID, task.AssigneeName, task.AssigneeRoleKey, task.Status, task.Decision, task.Comment, database.NullableText(task.DueAt), task.CompletedBy, database.NullableText(task.CompletedAt), task.UpdatedAt}
	return r.updateTx(ctx, tx, "workflow_tasks", task.WorkspaceID, task.ID, columns, values)
}

func (r WorkflowDecisionStore) updateProcessTx(ctx context.Context, tx *sql.Tx, process workflowmodel.WorkflowProcessInstance) error {
	definition, _ := json.Marshal(process.DefinitionSnapshot)
	currentNodes, _ := json.Marshal(process.CurrentNodeIDs)
	variables, _ := json.Marshal(database.NonNilMap(process.Variables))
	result, _ := json.Marshal(database.NonNilMap(process.Result))
	columns := []string{"workflow_key", "workflow_name", "workflow_definition_version_id", "definition_version", "definition_hash", "definition_json", "object_key", "record_id", "initiator_id", "initiator_role_key", "status", "current_node_ids_json", "variables_json", "result_json", "error_code", "created_at", "updated_at", "completed_at"}
	values := []any{process.WorkflowKey, process.WorkflowName, process.DefinitionVersionID, process.DefinitionVersion, process.DefinitionHash, string(definition), process.ObjectKey, process.RecordID, process.InitiatorID, process.InitiatorRoleKey, process.Status, string(currentNodes), string(variables), string(result), process.ErrorCode, process.CreatedAt, process.UpdatedAt, database.NullableText(process.CompletedAt)}
	return r.updateTx(ctx, tx, "workflow_process_instances", process.WorkspaceID, process.ID, columns, values)
}

func (r WorkflowDecisionStore) insertEventTx(ctx context.Context, tx *sql.Tx, event workflowmodel.WorkflowProcessEvent) error {
	metadata, _ := json.Marshal(database.NonNilMap(event.Metadata))
	return r.insertTx(ctx, tx, "workflow_process_events", workflowEventColumns, []any{event.WorkspaceID, event.ID, event.ProcessID, event.NodeID, event.TaskID, event.Event, event.ActorID, event.Summary, string(metadata), event.CreatedAt})
}

func (r WorkflowDecisionStore) updateExecutionTx(ctx context.Context, tx *sql.Tx, execution workflowmodel.WorkflowExecution) error {
	columns := workflowExecutionMutableColumns()
	values, err := workflowExecutionMutableValues(execution)
	if err != nil {
		return err
	}
	return r.updateTx(ctx, tx, "_workflow_executions", execution.WorkspaceID, execution.ID, columns, values)
}

func (r WorkflowDecisionStore) insertTx(ctx context.Context, tx *sql.Tx, table string, columns []string, values []any) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO "+r.store.TableIdentifier(table)+" ("+stringsJoinIdentifiers(r.store, columns...)+") VALUES ("+stringsJoinPlaceholders(r.store, len(columns))+")", values...)
	return err
}

func (r WorkflowDecisionStore) updateTx(ctx context.Context, tx *sql.Tx, table, workspaceID, id string, columns []string, values []any) error {
	assignments := make([]string, 0, len(columns))
	for index, column := range columns {
		assignments = append(assignments, r.store.Identifier(column)+" = "+r.store.Placeholder(index+1))
	}
	values = append(values, workspaceID, id)
	result, err := tx.ExecContext(ctx, "UPDATE "+r.store.TableIdentifier(table)+" SET "+strings.Join(assignments, ", ")+" WHERE "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(len(values)-1)+" AND "+r.store.Identifier("id")+" = "+r.store.Placeholder(len(values)), values...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err == nil && affected == 0 {
		return sql.ErrNoRows
	}
	return err
}
