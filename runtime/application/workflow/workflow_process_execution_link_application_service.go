package workflow

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"context"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"time"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

func (s *WorkflowApplicationService) failedWorkflowProcessExecution(
	ctx context.Context,
	workflow definitionmodel.WorkflowSchema,
	process workflowmodel.WorkflowProcessInstance,
	payload map[string]any,
	principal principalmodel.Principal,
	trigger string,
	attempt int,
	idempotencyKey string,
	processErr error,
) workflowmodel.WorkflowExecution {
	now := time.Now().UTC()
	execution := workflowmodel.WorkflowExecution{
		WorkspaceID: process.WorkspaceID,
		ExecutionID: workflowProcessID(ctx, "execution"), OperationID: process.OperationID, WorkflowKey: workflow.Key, Name: workflow.Name, Trigger: trigger,
		Status: "running", ActionType: "workflow_graph", Action: workflowpolicy.WorkflowCloneMap(workflow.Action), Payload: workflowpolicy.WorkflowCloneMap(payload),
		Result:    map[string]any{"process_id": process.ProcessID, "definition_hash": process.DefinitionHash},
		ProcessID: process.ProcessID, ObjectKey: process.ObjectKey, RecordID: process.RecordID,
		ActorID: principal.UserID, RunAs: workflowpolicy.WorkflowRunAs(workflow), IdempotencyKey: idempotencyKey,
		Attempt: attempt, MaxAttempts: workflowpolicy.WorkflowMaxAttempts(workflow), CreatedAt: process.CreatedAt, UpdatedAt: now.Format(time.RFC3339),
	}
	workflowpolicy.WorkflowMarkFailed(&execution, workflow, processErr, now)
	nodes, _ := s.processRepo.ListNodes(ctx, process.WorkspaceID, process.ProcessID)
	for index := len(nodes) - 1; index >= 0; index-- {
		if nodes[index].ErrorCode == "" && nodes[index].Status != "configuration_error" {
			continue
		}
		execution.NodeID = nodes[index].NodeID
		execution.Result["node_id"] = nodes[index].NodeID
		execution.Result["node_instance_id"] = nodes[index].NodeInstanceID
		break
	}
	return execution
}

func (s *WorkflowApplicationService) syncWorkflowExecutionWithProcess(ctx context.Context, process workflowmodel.WorkflowProcessInstance, decisionErr error) error {
	if process.ProcessID == "" || s == nil || s.workerRepo == nil {
		return nil
	}
	execution, ok, err := s.workerRepo.GetExecutionByProcessID(ctx, process.WorkspaceID, process.ProcessID)
	if err != nil || !ok {
		return err
	}
	execution.ProcessID = process.ProcessID
	if process.OperationID != "" {
		execution.OperationID = process.OperationID
	}
	execution.Status = process.Status
	execution.UpdatedAt = process.UpdatedAt
	execution.Result = workflowpolicy.WorkflowCloneMap(execution.Result)
	execution.Result["process_id"] = process.ProcessID
	execution.Result["current_node_ids"] = process.CurrentNodeIDs
	execution.Result["process_status"] = process.Status
	if len(process.CurrentNodeIDs) > 0 {
		execution.NodeID = process.CurrentNodeIDs[0]
		execution.Result["node_id"] = execution.NodeID
	} else {
		execution.NodeID = ""
		delete(execution.Result, "node_id")
	}
	if decisionErr != nil {
		execution.LastError = serviceErrorCode(decisionErr)
		execution.Result["last_decision_error"] = execution.LastError
	} else if process.Status == "completed" || process.Status == "rejected" || process.Status == "cancelled" {
		execution.LastError = ""
		execution.NextRunAt = ""
	}
	return s.workerRepo.UpdateExecution(ctx, process.WorkspaceID, execution)
}
