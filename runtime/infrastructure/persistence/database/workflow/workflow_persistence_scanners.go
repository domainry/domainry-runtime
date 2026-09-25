package workflow

import (
	"database/sql"
	"fmt"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/timevalue"
)

func workflowExecutionInsertValues(execution workflowmodel.WorkflowExecution) ([]string, []any, error) {
	actionJSON, err := timevalue.MarshalJSON(execution.Action)
	if err != nil {
		return nil, nil, fmt.Errorf("encode Workflow graph descriptor: %w", err)
	}
	payloadJSON, err := timevalue.MarshalJSON(execution.Payload)
	if err != nil {
		return nil, nil, fmt.Errorf("encode workflow payload: %w", err)
	}
	resultJSON, err := timevalue.MarshalJSON(execution.Result)
	if err != nil {
		return nil, nil, fmt.Errorf("encode workflow result: %w", err)
	}
	columns := workflowExecutionColumns()
	values := []any{execution.WorkspaceID, execution.ID, execution.OperationID, execution.WorkflowKey, execution.Name, execution.Trigger, execution.Status, execution.ActionType, string(actionJSON), string(payloadJSON), string(resultJSON), execution.ProcessID, execution.NodeID, execution.ObjectKey, execution.RecordID, execution.ActorID, execution.RunAs, execution.IdempotencyKey, execution.Attempt, execution.MaxAttempts, timevalue.Millis(execution.NextRunAt), execution.LastError, execution.LeaseOwner, timevalue.Millis(execution.LeaseExpiresAt), execution.FencingToken, execution.Message, timevalue.Millis(execution.CreatedAt), timevalue.Millis(execution.UpdatedAt)}
	return columns, values, nil
}

func workflowExecutionMutableColumns() []string {
	return []string{"operation_id", "workflow_key", "name", "trigger", "status", "action_type", "action_json", "payload_json", "result_json", "process_id", "node_id", "object_key", "record_id", "actor_id", "run_as", "idempotency_key", "attempt", "max_attempts", "next_run_at", "last_error", "lease_owner", "lease_expires_at", "fencing_token", "message", "created_at", "updated_at"}
}

func workflowExecutionMutableValues(execution workflowmodel.WorkflowExecution) ([]any, error) {
	actionJSON, err := timevalue.MarshalJSON(execution.Action)
	if err != nil {
		return nil, fmt.Errorf("encode Workflow graph descriptor: %w", err)
	}
	payloadJSON, err := timevalue.MarshalJSON(execution.Payload)
	if err != nil {
		return nil, fmt.Errorf("encode workflow payload: %w", err)
	}
	resultJSON, err := timevalue.MarshalJSON(execution.Result)
	if err != nil {
		return nil, fmt.Errorf("encode workflow result: %w", err)
	}
	return []any{execution.OperationID, execution.WorkflowKey, execution.Name, execution.Trigger, execution.Status, execution.ActionType, string(actionJSON), string(payloadJSON), string(resultJSON), execution.ProcessID, execution.NodeID, execution.ObjectKey, execution.RecordID, execution.ActorID, execution.RunAs, execution.IdempotencyKey, execution.Attempt, execution.MaxAttempts, timevalue.Millis(execution.NextRunAt), execution.LastError, execution.LeaseOwner, timevalue.Millis(execution.LeaseExpiresAt), execution.FencingToken, execution.Message, timevalue.Millis(execution.CreatedAt), timevalue.Millis(execution.UpdatedAt)}, nil
}

func workflowExecutionConditionValue(key string, value any) any {
	switch key {
	case "next_run_at", "lease_expires_at", "created_at", "updated_at":
		return timevalue.Millis(value)
	}
	return value
}

func workflowExecutionColumns() []string {
	return []string{"workspace_id", "id", "operation_id", "workflow_key", "name", "trigger", "status", "action_type", "action_json", "payload_json", "result_json", "process_id", "node_id", "object_key", "record_id", "actor_id", "run_as", "idempotency_key", "attempt", "max_attempts", "next_run_at", "last_error", "lease_owner", "lease_expires_at", "fencing_token", "message", "created_at", "updated_at"}
}

type workflowExecutionScanner interface{ Scan(dest ...any) error }

func scanWorkflowExecution(scanner workflowExecutionScanner) (workflowmodel.WorkflowExecution, error) {
	var execution workflowmodel.WorkflowExecution
	var actionJSON, payloadJSON, resultJSON string
	var processID, nodeID sql.NullString
	var nextRunAt, leaseExpiresAt, createdAt, updatedAt int64
	if err := scanner.Scan(&execution.WorkspaceID, &execution.ID, &execution.OperationID, &execution.WorkflowKey, &execution.Name, &execution.Trigger, &execution.Status, &execution.ActionType, &actionJSON, &payloadJSON, &resultJSON, &processID, &nodeID, &execution.ObjectKey, &execution.RecordID, &execution.ActorID, &execution.RunAs, &execution.IdempotencyKey, &execution.Attempt, &execution.MaxAttempts, &nextRunAt, &execution.LastError, &execution.LeaseOwner, &leaseExpiresAt, &execution.FencingToken, &execution.Message, &createdAt, &updatedAt); err != nil {
		return workflowmodel.WorkflowExecution{}, err
	}
	_ = timevalue.UnmarshalJSON([]byte(actionJSON), &execution.Action)
	_ = timevalue.UnmarshalJSON([]byte(payloadJSON), &execution.Payload)
	_ = timevalue.UnmarshalJSON([]byte(resultJSON), &execution.Result)
	if execution.Action == nil {
		execution.Action = map[string]any{}
	}
	if execution.Payload == nil {
		execution.Payload = map[string]any{}
	}
	if execution.Result == nil {
		execution.Result = map[string]any{}
	}
	execution.NextRunAt, execution.LeaseExpiresAt = timevalue.String(nextRunAt), timevalue.String(leaseExpiresAt)
	execution.CreatedAt, execution.UpdatedAt = timevalue.String(createdAt), timevalue.String(updatedAt)
	execution.ProcessID, execution.NodeID = processID.String, nodeID.String
	return execution, nil
}

type workflowScanner interface{ Scan(dest ...any) error }

func scanWorkflowProcess(scanner workflowScanner) (workflowmodel.WorkflowProcessInstance, error) {
	var process workflowmodel.WorkflowProcessInstance
	var definition, currentNodes, variables, result string
	var objectKey, recordID, initiatorRoleKey, errorCode sql.NullString
	var createdAt, updatedAt, completedAt int64
	err := scanner.Scan(&process.WorkspaceID, &process.ID, &process.OperationID, &process.WorkflowKey, &process.WorkflowName, &process.DefinitionVersionID, &process.DefinitionVersion, &process.DefinitionHash, &definition, &objectKey, &recordID, &process.InitiatorID, &initiatorRoleKey, &process.Status, &currentNodes, &variables, &result, &errorCode, &createdAt, &updatedAt, &completedAt)
	if err != nil {
		return process, err
	}
	process.ObjectKey, process.RecordID, process.InitiatorRoleKey, process.ErrorCode = objectKey.String, recordID.String, initiatorRoleKey.String, errorCode.String
	process.CreatedAt, process.UpdatedAt, process.CompletedAt = timevalue.String(createdAt), timevalue.String(updatedAt), timevalue.String(completedAt)
	_ = timevalue.UnmarshalJSON([]byte(definition), &process.DefinitionSnapshot)
	// DefinitionVersionID and PublishedVersion are runtime metadata and are
	// intentionally excluded from the authored Workflow JSON. Rehydrate them
	// from the canonical process columns so durable continuations resolve the
	// exact workload release that was active when the process started.
	process.DefinitionSnapshot.DefinitionVersionID = process.DefinitionVersionID
	process.DefinitionSnapshot.PublishedVersion = process.DefinitionVersion
	_ = timevalue.UnmarshalJSON([]byte(currentNodes), &process.CurrentNodeIDs)
	_ = timevalue.UnmarshalJSON([]byte(variables), &process.Variables)
	_ = timevalue.UnmarshalJSON([]byte(result), &process.Result)
	return process, nil
}

func workflowTaskColumns() []string {
	return []string{"workspace_id", "id", "process_id", "node_instance_id", "node_id", "title", "assignee_user_id", "assignee_name", "assignee_role_key", "assignee_resolver_key", "assignee_evidence_json", "resolver_snapshot_json", "candidate_source", "node_definition_version", "sequence_no", "status", "decision", "comment", "due_at", "completed_by", "completed_at", "created_at", "updated_at"}
}

func scanWorkflowTask(scanner workflowScanner) (workflowmodel.WorkflowTask, error) {
	var task workflowmodel.WorkflowTask
	var assigneeUserID, assigneeName, assigneeRoleKey, assigneeResolverKey, candidateSource, decision, comment, completedBy sql.NullString
	var dueAt, completedAt, createdAt, updatedAt int64
	var assigneeEvidence, resolverSnapshot string
	err := scanner.Scan(&task.WorkspaceID, &task.ID, &task.ProcessID, &task.NodeInstanceID, &task.NodeID, &task.Title, &assigneeUserID, &assigneeName, &assigneeRoleKey, &assigneeResolverKey, &assigneeEvidence, &resolverSnapshot, &candidateSource, &task.NodeDefinitionVersion, &task.Sequence, &task.Status, &decision, &comment, &dueAt, &completedBy, &completedAt, &createdAt, &updatedAt)
	task.AssigneeUserID, task.AssigneeName, task.AssigneeRoleKey, task.AssigneeResolverKey, task.CandidateSource = assigneeUserID.String, assigneeName.String, assigneeRoleKey.String, assigneeResolverKey.String, candidateSource.String
	_ = timevalue.UnmarshalJSON([]byte(assigneeEvidence), &task.AssigneeEvidence)
	_ = timevalue.UnmarshalJSON([]byte(resolverSnapshot), &task.ResolverSnapshot)
	task.Decision, task.Comment, task.DueAt, task.CompletedBy, task.CompletedAt = decision.String, comment.String, timevalue.String(dueAt), completedBy.String, timevalue.String(completedAt)
	task.CreatedAt, task.UpdatedAt = timevalue.String(createdAt), timevalue.String(updatedAt)
	return task, err
}
