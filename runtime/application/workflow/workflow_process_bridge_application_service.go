package workflow

import (
	"context"
	"strings"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

type workflowTaskBatchUpdater interface {
	UpdateTasks(context.Context, string, []workflowmodel.WorkflowTask) error
}

type workflowProcessBatchNodeLister interface {
	ListNodesForProcesses(context.Context, string, []string) ([]workflowmodel.WorkflowNodeInstance, error)
}

func (s *WorkflowApplicationService) loadWorkflowProcessSummaryNodes(ctx context.Context, workspaceID string, processes []workflowmodel.WorkflowProcessInstance) (map[string][]workflowmodel.WorkflowNodeInstance, bool) {
	repository, ok := s.processRepo.(workflowProcessBatchNodeLister)
	if !ok {
		return nil, false
	}
	failedProcessIDs := make([]string, 0, len(processes))
	for _, process := range processes {
		if process.Status == "failed" || process.Status == "configuration_error" {
			failedProcessIDs = append(failedProcessIDs, process.ID)
		}
	}
	if len(failedProcessIDs) == 0 {
		return map[string][]workflowmodel.WorkflowNodeInstance{}, true
	}
	nodes, err := repository.ListNodesForProcesses(ctx, workspaceID, failedProcessIDs)
	if err != nil {
		return nil, false
	}
	nodesByProcess := make(map[string][]workflowmodel.WorkflowNodeInstance, len(failedProcessIDs))
	for _, node := range nodes {
		nodesByProcess[node.ProcessID] = append(nodesByProcess[node.ProcessID], node)
	}
	return nodesByProcess, true
}

func updateWorkflowTasks(ctx context.Context, repository workflowcontract.WorkflowProcessStore, workspaceID string, tasks []workflowmodel.WorkflowTask) error {
	if batch, ok := repository.(workflowTaskBatchUpdater); ok {
		return batch.UpdateTasks(ctx, workspaceID, tasks)
	}
	var firstErr error
	for _, task := range tasks {
		if err := repository.UpdateTask(ctx, workspaceID, task); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (e *WorkflowProcessEngine) cancelUnfinishedApprovalTasks(ctx context.Context, tasks []workflowmodel.WorkflowTask, exceptTaskID, actorID string) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	updates := make([]workflowmodel.WorkflowTask, 0, len(tasks))
	for _, task := range tasks {
		if task.ID == exceptTaskID || (task.Status != "open" && task.Status != "pending") {
			continue
		}
		task.Status = "cancelled"
		task.UpdatedAt = now
		updates = append(updates, task)
		e.appendEvent(ctx, task.WorkspaceID, task.ProcessID, task.NodeID, task.ID, "task_cancelled", actorID, task.Title, nil)
	}
	if len(updates) > 0 {
		_ = updateWorkflowTasks(ctx, e.runtime.dependencies.Processes, updates[0].WorkspaceID, updates)
	}
}

func (s *WorkflowApplicationService) executeWorkflowGraphProcess(ctx context.Context, workflow definitionmodel.WorkflowSchema, payload map[string]any, principal principalmodel.Principal, trigger string, attempt int, ignoreIdempotency bool) (workflowmodel.WorkflowExecution, error) {
	idempotencyKey := workflowpolicy.WorkflowIdempotencyKey(workflow, payload)
	return s.executeWorkflowGraphProcessWithIdempotencyKey(ctx, workflow, payload, principal, trigger, idempotencyKey, attempt, ignoreIdempotency)
}

func (s *WorkflowApplicationService) executeWorkflowGraphProcessWithIdempotencyKey(ctx context.Context, workflow definitionmodel.WorkflowSchema, payload map[string]any, principal principalmodel.Principal, trigger, idempotencyKey string, attempt int, ignoreIdempotency bool) (workflowmodel.WorkflowExecution, error) {
	claim, replay, found, err := s.beginWorkflowExecution(ctx, workflow, payload, principal, trigger, idempotencyKey, attempt, ignoreIdempotency)
	if err != nil {
		return workflowmodel.WorkflowExecution{}, err
	}
	if found {
		if strings.HasPrefix(strings.TrimSpace(trigger), "scheduled:") {
			replay.Status = "duplicate"
			replay.Message = "workflow.message.duplicateSkipped"
		}
		return replay, nil
	}
	process, err := s.processEngine.Start(ctx, workflow, payload, principal)
	if err != nil {
		if process.ID == "" {
			return workflowmodel.WorkflowExecution{}, err
		}
		execution := s.failedWorkflowProcessExecution(ctx, workflow, process, payload, principal, trigger, attempt, idempotencyKey, err)
		if insertErr := s.workerRepo.InsertExecution(ctx, principal.WorkspaceID, execution); insertErr != nil {
			return workflowmodel.WorkflowExecution{}, internalError("insert failed workflow process execution", insertErr)
		}
		if completeErr := s.completeWorkflowExecutionReceipt(ctx, claim, execution, principal); completeErr != nil {
			return workflowmodel.WorkflowExecution{}, internalError("complete failed workflow execution receipt", completeErr)
		}
		s.processEngine.appendEvent(ctx, process.WorkspaceID, process.ID, execution.NodeID, "", "workflow_execution_"+execution.Status, principal.UserID, execution.LastError, map[string]any{"execution_id": execution.ID, "attempt": execution.Attempt})
		return execution, nil
	}
	execution := workflowmodel.WorkflowExecution{
		WorkspaceID: principal.WorkspaceID,
		ID:          process.ID, WorkflowKey: workflow.Key, Name: workflow.Name, Trigger: trigger,
		Status: process.Status, ActionType: "workflow_graph", Action: workflowpolicy.WorkflowCloneMap(workflow.Action), Payload: workflowpolicy.WorkflowCloneMap(payload),
		Result: map[string]any{"process_id": process.ID, "current_node_ids": process.CurrentNodeIDs, "definition_hash": process.DefinitionHash}, ProcessID: process.ID,
		ObjectKey: process.ObjectKey, RecordID: process.RecordID, ActorID: principal.UserID, RunAs: workflowpolicy.WorkflowRunAs(workflow),
		IdempotencyKey: idempotencyKey, Attempt: attempt, MaxAttempts: workflowpolicy.WorkflowMaxAttempts(workflow), Message: "workflow.message.processStarted",
		CreatedAt: process.CreatedAt, UpdatedAt: process.UpdatedAt,
	}
	if err := s.workerRepo.InsertExecution(ctx, principal.WorkspaceID, execution); err != nil {
		return workflowmodel.WorkflowExecution{}, internalError("insert workflow process execution", err)
	}
	if err := s.completeWorkflowExecutionReceipt(ctx, claim, execution, principal); err != nil {
		return workflowmodel.WorkflowExecution{}, internalError("complete workflow execution receipt", err)
	}
	return execution, nil
}
