package workflow

import (
	"database/sql"
	"encoding/json"
	"fmt"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

func workflowExecutionInsertValues(execution workflowmodel.WorkflowExecution) ([]string, []any, error) {
	actionJSON, err := json.Marshal(execution.Action)
	if err != nil {
		return nil, nil, fmt.Errorf("encode Workflow graph descriptor: %w", err)
	}
	payloadJSON, err := json.Marshal(execution.Payload)
	if err != nil {
		return nil, nil, fmt.Errorf("encode workflow payload: %w", err)
	}
	resultJSON, err := json.Marshal(execution.Result)
	if err != nil {
		return nil, nil, fmt.Errorf("encode workflow result: %w", err)
	}
	columns := workflowExecutionColumns()
	values := []any{execution.WorkspaceID, execution.ID, execution.OperationID, execution.WorkflowKey, execution.Name, execution.Trigger, execution.Status, execution.ActionType, string(actionJSON), string(payloadJSON), string(resultJSON), execution.ProcessID, execution.NodeID, execution.ObjectKey, execution.RecordID, execution.ActorID, execution.RunAs, execution.IdempotencyKey, execution.Attempt, execution.MaxAttempts, database.NullableText(execution.NextRunAt), execution.LastError, execution.LeaseOwner, execution.LeaseExpiresAt, execution.FencingToken, execution.Message, execution.CreatedAt, execution.UpdatedAt}
	return columns, values, nil
}

func workflowExecutionMutableColumns() []string {
	return []string{"operation_id", "workflow_key", "name", "trigger", "status", "action_type", "action_json", "payload_json", "result_json", "process_id", "node_id", "object_key", "record_id", "actor_id", "run_as", "idempotency_key", "attempt", "max_attempts", "next_run_at", "last_error", "lease_owner", "lease_expires_at", "fencing_token", "message", "created_at", "updated_at"}
}

func workflowExecutionMutableValues(execution workflowmodel.WorkflowExecution) ([]any, error) {
	actionJSON, err := json.Marshal(execution.Action)
	if err != nil {
		return nil, fmt.Errorf("encode Workflow graph descriptor: %w", err)
	}
	payloadJSON, err := json.Marshal(execution.Payload)
	if err != nil {
		return nil, fmt.Errorf("encode workflow payload: %w", err)
	}
	resultJSON, err := json.Marshal(execution.Result)
	if err != nil {
		return nil, fmt.Errorf("encode workflow result: %w", err)
	}
	return []any{execution.OperationID, execution.WorkflowKey, execution.Name, execution.Trigger, execution.Status, execution.ActionType, string(actionJSON), string(payloadJSON), string(resultJSON), execution.ProcessID, execution.NodeID, execution.ObjectKey, execution.RecordID, execution.ActorID, execution.RunAs, execution.IdempotencyKey, execution.Attempt, execution.MaxAttempts, database.NullableText(execution.NextRunAt), execution.LastError, execution.LeaseOwner, execution.LeaseExpiresAt, execution.FencingToken, execution.Message, execution.CreatedAt, execution.UpdatedAt}, nil
}

func workflowExecutionConditionValue(key string, value any) any {
	if key == "next_run_at" {
		if text, ok := value.(string); ok {
			return database.NullableText(text)
		}
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
	var processID, nodeID, nextRunAt sql.NullString
	if err := scanner.Scan(&execution.WorkspaceID, &execution.ID, &execution.OperationID, &execution.WorkflowKey, &execution.Name, &execution.Trigger, &execution.Status, &execution.ActionType, &actionJSON, &payloadJSON, &resultJSON, &processID, &nodeID, &execution.ObjectKey, &execution.RecordID, &execution.ActorID, &execution.RunAs, &execution.IdempotencyKey, &execution.Attempt, &execution.MaxAttempts, &nextRunAt, &execution.LastError, &execution.LeaseOwner, &execution.LeaseExpiresAt, &execution.FencingToken, &execution.Message, &execution.CreatedAt, &execution.UpdatedAt); err != nil {
		return workflowmodel.WorkflowExecution{}, err
	}
	_ = json.Unmarshal([]byte(actionJSON), &execution.Action)
	_ = json.Unmarshal([]byte(payloadJSON), &execution.Payload)
	_ = json.Unmarshal([]byte(resultJSON), &execution.Result)
	if execution.Action == nil {
		execution.Action = map[string]any{}
	}
	if execution.Payload == nil {
		execution.Payload = map[string]any{}
	}
	if execution.Result == nil {
		execution.Result = map[string]any{}
	}
	execution.NextRunAt, execution.ProcessID, execution.NodeID = nextRunAt.String, processID.String, nodeID.String
	return execution, nil
}

type workflowScanner interface{ Scan(dest ...any) error }

func scanWorkflowProcess(scanner workflowScanner) (workflowmodel.WorkflowProcessInstance, error) {
	var process workflowmodel.WorkflowProcessInstance
	var definition, currentNodes, variables, result string
	var objectKey, recordID, initiatorRoleKey, errorCode, completedAt sql.NullString
	err := scanner.Scan(&process.WorkspaceID, &process.ID, &process.OperationID, &process.WorkflowKey, &process.WorkflowName, &process.DefinitionVersionID, &process.DefinitionVersion, &process.DefinitionHash, &definition, &objectKey, &recordID, &process.InitiatorID, &initiatorRoleKey, &process.Status, &currentNodes, &variables, &result, &errorCode, &process.CreatedAt, &process.UpdatedAt, &completedAt)
	if err != nil {
		return process, err
	}
	process.ObjectKey, process.RecordID, process.InitiatorRoleKey, process.ErrorCode, process.CompletedAt = objectKey.String, recordID.String, initiatorRoleKey.String, errorCode.String, completedAt.String
	_ = json.Unmarshal([]byte(definition), &process.DefinitionSnapshot)
	// DefinitionVersionID and PublishedVersion are runtime metadata and are
	// intentionally excluded from the authored Workflow JSON. Rehydrate them
	// from the canonical process columns so durable continuations resolve the
	// exact workload release that was active when the process started.
	process.DefinitionSnapshot.DefinitionVersionID = process.DefinitionVersionID
	process.DefinitionSnapshot.PublishedVersion = process.DefinitionVersion
	_ = json.Unmarshal([]byte(currentNodes), &process.CurrentNodeIDs)
	_ = json.Unmarshal([]byte(variables), &process.Variables)
	_ = json.Unmarshal([]byte(result), &process.Result)
	return process, nil
}

func workflowTaskColumns() []string {
	return []string{"workspace_id", "id", "process_id", "node_instance_id", "node_id", "title", "assignee_user_id", "assignee_name", "assignee_role_key", "assignee_resolver_key", "assignee_evidence_json", "resolver_snapshot_json", "candidate_source", "node_definition_version", "sequence_no", "status", "decision", "comment", "due_at", "completed_by", "completed_at", "created_at", "updated_at"}
}

func scanWorkflowTask(scanner workflowScanner) (workflowmodel.WorkflowTask, error) {
	var task workflowmodel.WorkflowTask
	var assigneeUserID, assigneeName, assigneeRoleKey, assigneeResolverKey, candidateSource, decision, comment, dueAt, completedBy, completedAt sql.NullString
	var assigneeEvidence, resolverSnapshot string
	err := scanner.Scan(&task.WorkspaceID, &task.ID, &task.ProcessID, &task.NodeInstanceID, &task.NodeID, &task.Title, &assigneeUserID, &assigneeName, &assigneeRoleKey, &assigneeResolverKey, &assigneeEvidence, &resolverSnapshot, &candidateSource, &task.NodeDefinitionVersion, &task.Sequence, &task.Status, &decision, &comment, &dueAt, &completedBy, &completedAt, &task.CreatedAt, &task.UpdatedAt)
	task.AssigneeUserID, task.AssigneeName, task.AssigneeRoleKey, task.AssigneeResolverKey, task.CandidateSource = assigneeUserID.String, assigneeName.String, assigneeRoleKey.String, assigneeResolverKey.String, candidateSource.String
	_ = json.Unmarshal([]byte(assigneeEvidence), &task.AssigneeEvidence)
	_ = json.Unmarshal([]byte(resolverSnapshot), &task.ResolverSnapshot)
	task.Decision, task.Comment, task.DueAt, task.CompletedBy, task.CompletedAt = decision.String, comment.String, dueAt.String, completedBy.String, completedAt.String
	return task, err
}
