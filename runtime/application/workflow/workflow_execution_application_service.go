package workflow

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
	workflowvalidation "github.com/domainry/domainry-runtime/runtime/domain/workflow/validation"
)

func (s *WorkflowApplicationService) ProcessDueWorkflowExecutionsForScheduledWindow(ctx context.Context, targetKey string, scheduledFor time.Time, limit int, principal principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	return s.processDueWorkflowExecutions(ctx, targetKey, scheduledFor.UTC(), limit, principal, true, "")
}

func (s *WorkflowApplicationService) processScheduledWorkflowExecutionsForTarget(ctx context.Context, targetKey string, limit int, principal principalmodel.Principal, now time.Time) ([]workflowmodel.WorkflowExecution, error) {
	return s.processScheduledWorkflowExecutionsForTargetWindow(ctx, targetKey, limit, principal, now, now)
}

func (s *WorkflowApplicationService) processScheduledWorkflowExecutionsForTargetWindow(ctx context.Context, targetKey string, limit int, principal principalmodel.Principal, _ time.Time, scheduledFor time.Time) ([]workflowmodel.WorkflowExecution, error) {
	processed := make([]workflowmodel.WorkflowExecution, 0)
	if limit <= 0 {
		return processed, nil
	}
	targetKey = strings.TrimPrefix(strings.TrimSpace(targetKey), "scheduled:")
	if targetKey == "*" {
		targetKey = ""
	}
	registered := s.registry.List()
	workflows := make([]definitionmodel.WorkflowSchema, 0, len(registered))
	for _, workflow := range registered {
		if !workflow.Enabled || workflow.TriggerContract == nil || strings.TrimSpace(workflow.TriggerContract.Type) != "scheduled" || (targetKey != "" && workflow.Key != targetKey) {
			continue
		}
		workflows = append(workflows, workflow)
	}
	if targetKey != "" && len(workflows) == 0 {
		return processed, notFound("backend.workflow.scheduled_target_not_found", "workflow", targetKey)
	}
	sort.Slice(workflows, func(i, j int) bool { return workflows[i].Key < workflows[j].Key })
	for _, workflow := range workflows {
		objectKeys := workflowpolicy.WorkflowTriggerObjectKeys(workflow)
		if len(objectKeys) == 0 {
			execution, completed, err := s.executeGlobalScheduledWorkflow(ctx, workflow, principal, scheduledFor)
			if err != nil {
				return processed, err
			}
			if completed {
				processed = append(processed, execution)
			}
			continue
		}
		scanPrincipal, err := s.workflowPrincipal(ctx, workflow, principal)
		if err != nil {
			return processed, err
		}
		for _, objectKey := range objectKeys {
			if err := ctx.Err(); err != nil {
				return processed, err
			}
			if len(processed) >= limit {
				return processed, nil
			}
			object, ok := s.schemaMap(ctx)[objectKey]
			if !ok {
				continue
			}
			afterID := ""
			for {
				result, err := s.recordReader.ListWorkflowRecords(ctx, scanPrincipal.WorkspaceID, object, recordmodel.RecordListQuery{Page: 1, PageSize: 200, SkipTotal: true, AfterID: afterID, Sort: []recordmodel.RecordSortRule{{Field: "id", Direction: "asc"}}}, scanPrincipal)
				if err != nil {
					return processed, internalError("list records for scheduled workflow", err)
				}
				for _, record := range result.Items {
					if len(processed) >= limit {
						return processed, nil
					}
					if !workflowpolicy.WorkflowConditionMatches(ctx, workflow, record.Data) {
						continue
					}
					payload := workflowPayloadForRecord(objectKey, record)
					payload["scheduled_at"] = scheduledFor.UTC().Format(time.RFC3339Nano)
					execution, err := s.executeWorkflowAttempt(ctx, workflow, payload, principal, "scheduled:"+workflow.Key, 1, false)
					if err != nil {
						if code := apperror.CodeOf(err); code == idempotency.ErrorCodeKeyReused || code == idempotency.ErrorCodeInProgress {
							continue
						}
						return processed, err
					}
					if execution.Status != "duplicate" {
						processed = append(processed, execution)
					}
				}
				if !result.HasNext {
					break
				}
				afterID = result.Items[len(result.Items)-1].ID
			}
		}
	}
	return processed, nil
}

func (s *WorkflowApplicationService) SimulateWorkflow(ctx context.Context, workflowKey string, payload map[string]any, principal principalmodel.Principal) (workflowmodel.WorkflowSimulationResult, error) {
	if err := workflowAuthorizeQuery(principal); err != nil {
		return workflowmodel.WorkflowSimulationResult{}, err
	}
	workflow, ok := s.registry.Get(workflowKey)
	if !ok {
		return workflowmodel.WorkflowSimulationResult{}, notFound("backend.workflow.not_found")
	}
	wouldExecute := workflow.Enabled && workflowpolicy.WorkflowConditionMatches(ctx, workflow, payload)
	status := "skipped"
	message := "Workflow condition did not match"
	if wouldExecute {
		status = "simulated"
		message = "Workflow would execute"
	}
	if !workflow.Enabled {
		message = "Workflow is disabled"
	}
	nodes := []workflowmodel.WorkflowSimulationNode{}
	if wouldExecute {
		var err error
		nodes, err = s.processEngine.Simulate(ctx, workflow, payload, principal)
		if err != nil {
			return workflowmodel.WorkflowSimulationResult{}, err
		}
	}
	return workflowmodel.WorkflowSimulationResult{
		WorkflowKey:    workflow.Key,
		Name:           workflow.Name,
		Status:         status,
		WouldExecute:   wouldExecute,
		Action:         workflowpolicy.WorkflowCloneMap(workflow.Action),
		Payload:        workflowpolicy.WorkflowCloneMap(payload),
		RunAs:          workflowpolicy.WorkflowRunAs(workflow),
		IdempotencyKey: workflowpolicy.WorkflowIdempotencyKey(workflow, payload),
		Message:        message,
		Nodes:          nodes,
	}, nil
}

func (s *WorkflowApplicationService) SimulateWorkflowCandidate(ctx context.Context, workflow definitionmodel.WorkflowSchema, payload map[string]any, principal principalmodel.Principal) (workflowmodel.WorkflowSimulationResult, error) {
	if err := workflowAuthorizeQuery(principal); err != nil {
		return workflowmodel.WorkflowSimulationResult{}, err
	}
	workflow.Key = strings.TrimSpace(workflow.Key)
	if workflow.Key == "" {
		return workflowmodel.WorkflowSimulationResult{}, badRequest("backend.workflow.key_required")
	}
	if err := workflowvalidation.WorkflowValidateGraph(workflow.Graph); err != nil {
		return workflowmodel.WorkflowSimulationResult{}, err
	}
	wouldExecute := workflow.Enabled && workflowpolicy.WorkflowConditionMatches(ctx, workflow, payload)
	status := "skipped"
	message := "Workflow condition did not match"
	if wouldExecute {
		status = "simulated"
		message = "Workflow would execute"
	}
	if !workflow.Enabled {
		message = "Workflow is disabled"
	}
	nodes := []workflowmodel.WorkflowSimulationNode{}
	if wouldExecute {
		var err error
		nodes, err = s.processEngine.Simulate(ctx, workflow, payload, principal)
		if err != nil {
			return workflowmodel.WorkflowSimulationResult{}, err
		}
	}
	return workflowmodel.WorkflowSimulationResult{
		WorkflowKey:    workflow.Key,
		Name:           workflow.Name,
		Status:         status,
		WouldExecute:   wouldExecute,
		Action:         workflowpolicy.WorkflowCloneMap(workflow.Action),
		Payload:        workflowpolicy.WorkflowCloneMap(payload),
		RunAs:          workflowpolicy.WorkflowRunAs(workflow),
		IdempotencyKey: workflowpolicy.WorkflowIdempotencyKey(workflow, payload),
		Message:        message,
		Nodes:          nodes,
	}, nil
}

func (s *WorkflowApplicationService) RetryWorkflowExecution(ctx context.Context, executionID string, principal principalmodel.Principal) (workflowmodel.WorkflowRunResult, error) {
	return s.RetryWorkflowExecutionWithKey(ctx, executionID, "system:"+strings.TrimSpace(executionID)+":retry", principal)
}

func (s *WorkflowApplicationService) InspectWorkflowExecution(ctx context.Context, executionID string, principal principalmodel.Principal) (workflowmodel.WorkflowExecution, error) {
	if err := workflowAuthorizeQuery(principal); err != nil {
		return workflowmodel.WorkflowExecution{}, err
	}
	execution, found, err := s.workerRepo.GetExecution(ctx, principal.WorkspaceID, strings.TrimSpace(executionID))
	if err != nil {
		return workflowmodel.WorkflowExecution{}, internalError("get workflow execution", err)
	}
	if !found {
		return workflowmodel.WorkflowExecution{}, notFound("backend.workflow.execution_not_found")
	}
	return execution, nil
}

func (s *WorkflowApplicationService) RetryWorkflowExecutionWithKey(ctx context.Context, executionID, callerKey string, principal principalmodel.Principal) (workflowmodel.WorkflowRunResult, error) {
	if err := workflowAuthorizeCommand(principal); err != nil {
		return workflowmodel.WorkflowRunResult{}, err
	}
	previous, ok, err := s.workerRepo.GetExecution(ctx, principal.WorkspaceID, strings.TrimSpace(executionID))
	if err != nil {
		return workflowmodel.WorkflowRunResult{}, internalError("get workflow execution", err)
	}
	if !ok {
		return workflowmodel.WorkflowRunResult{}, notFound("backend.workflow.execution_not_found")
	}
	commandKey := workflowCommandKey("execution.retry", previous.ID, callerKey, map[string]any{"attempt": previous.Attempt + 1})
	if previous.Status == "skipped" {
		storedKey := workflowStringValue(previous.Result[workflowCommandResultField("execution.retry")])
		if storedKey == "" || storedKey == commandKey {
			if retryID := workflowStringValue(previous.Result["manual_retry_execution_id"]); retryID != "" {
				retryExecution, found, err := s.workerRepo.GetExecution(ctx, principal.WorkspaceID, retryID)
				if err != nil {
					return workflowmodel.WorkflowRunResult{}, internalError("get workflow retry execution", err)
				}
				if found {
					workflow, ok := s.registry.Get(previous.WorkflowKey)
					if !ok {
						return workflowmodel.WorkflowRunResult{}, notFound("backend.workflow.not_found")
					}
					return workflowmodel.WorkflowRunResult{
						WorkflowKey: workflow.Key,
						Name:        workflow.Name,
						Status:      retryExecution.Status,
						Action:      workflow.Action,
						Payload:     retryExecution.Payload,
						Execution:   retryExecution,
					}, nil
				}
			}
		}
	}
	if previous.Status != "failed" && previous.Status != "dead_letter" {
		return workflowmodel.WorkflowRunResult{}, badRequest("backend.workflow.retry_status_invalid")
	}
	workflow, ok := s.registry.Get(previous.WorkflowKey)
	if !ok {
		return workflowmodel.WorkflowRunResult{}, notFound("backend.workflow.not_found")
	}
	nextAttempt := previous.Attempt + 1
	if nextAttempt <= 0 {
		nextAttempt = 1
	}
	maxAttempts := workflowpolicy.WorkflowMaxAttempts(workflow)
	if nextAttempt > maxAttempts {
		return workflowmodel.WorkflowRunResult{}, badRequest("backend.workflow.max_attempts_reached")
	}
	payload := s.workflowRetryPayload(ctx, principal.WorkspaceID, previous, principal)
	execution, err := s.executeWorkflowAttempt(ctx, workflow, payload, principal, "retry:"+previous.ID, nextAttempt, true)
	if err != nil {
		return workflowmodel.WorkflowRunResult{}, err
	}
	previous.Status = "skipped"
	previous.NextRunAt = ""
	previous.Message = "workflow.message.retryContinued"
	previous.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	previous.Result = workflowpolicy.WorkflowCloneMap(previous.Result)
	previous.Result["manual_retry_execution_id"] = execution.ID
	previous.Result["manual_retry_requested_by"] = principal.UserID
	previous.Result[workflowCommandResultField("execution.retry")] = commandKey
	if err := s.workerRepo.UpdateExecution(ctx, principal.WorkspaceID, previous); err != nil {
		return workflowmodel.WorkflowRunResult{}, internalError("mark workflow retry continuation", err)
	}
	return workflowmodel.WorkflowRunResult{
		WorkflowKey: workflow.Key,
		Name:        workflow.Name,
		Status:      execution.Status,
		Action:      workflow.Action,
		Payload:     payload,
		Execution:   execution,
	}, nil
}

func (s *WorkflowApplicationService) ResolveWorkflowExecution(ctx context.Context, executionID string, reason string, principal principalmodel.Principal) (workflowmodel.WorkflowResolveResult, error) {
	if err := workflowAuthorizeCommand(principal); err != nil {
		return workflowmodel.WorkflowResolveResult{}, err
	}
	execution, ok, err := s.workerRepo.GetExecution(ctx, principal.WorkspaceID, strings.TrimSpace(executionID))
	if err != nil {
		return workflowmodel.WorkflowResolveResult{}, internalError("get workflow execution", err)
	}
	if !ok {
		return workflowmodel.WorkflowResolveResult{}, notFound("backend.workflow.execution_not_found")
	}
	if execution.Status == "resolved" {
		return workflowmodel.WorkflowResolveResult{
			Status:    execution.Status,
			Message:   execution.Message,
			Execution: execution,
		}, nil
	}
	if execution.Status != "dead_letter" {
		return workflowmodel.WorkflowResolveResult{}, badRequest("backend.workflow.resolve_status_invalid")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "Acknowledged and removed from active dead-letter queue"
	}
	execution.Status = "resolved"
	execution.NextRunAt = ""
	execution.Message = "workflow.message.deadLetterResolved"
	execution.UpdatedAt = now
	execution.Result = workflowpolicy.WorkflowCloneMap(execution.Result)
	execution.Result["resolved"] = true
	execution.Result["resolved_at"] = now
	execution.Result["resolved_by"] = principal.UserID
	execution.Result["resolve_reason"] = reason
	if err := s.workerRepo.UpdateExecution(ctx, principal.WorkspaceID, execution); err != nil {
		return workflowmodel.WorkflowResolveResult{}, internalError("resolve workflow execution", err)
	}
	s.auditMetadata(ctx, "workflow_dead_letter_resolved", execution.ObjectKey, execution.RecordID, principal, execution.Message, nil, nil, map[string]any{
		"workflow_key":       execution.WorkflowKey,
		"workflow_name":      execution.Name,
		"execution_id":       execution.ID,
		"previous_status":    "dead_letter",
		"status":             execution.Status,
		"trigger":            execution.Trigger,
		"trigger_object_key": valueOrDefault(workflowpolicy.WorkflowPayloadString(execution.Payload, "object_key"), execution.ObjectKey),
		"trigger_record_id":  valueOrDefault(workflowpolicy.WorkflowPayloadString(execution.Payload, "record_id"), execution.RecordID),
		"attempt":            execution.Attempt,
		"max_attempts":       execution.MaxAttempts,
		"run_as":             execution.RunAs,
		"action_type":        execution.ActionType,
		"last_error":         execution.LastError,
		"resolved_by":        principal.UserID,
		"resolve_reason":     reason,
	})
	return workflowmodel.WorkflowResolveResult{
		Status:    execution.Status,
		Message:   execution.Message,
		Execution: execution,
	}, nil
}
