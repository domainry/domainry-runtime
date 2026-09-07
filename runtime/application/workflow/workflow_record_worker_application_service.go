package workflow

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"context"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

func (s *WorkflowApplicationService) ProcessDueWorkflowExecutions(ctx context.Context, limit int, principal principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	return s.ProcessDueWorkflowExecutionsForTarget(ctx, "", limit, principal)
}

func (s *WorkflowApplicationService) ProcessDueWorkflowExecutionsForTarget(ctx context.Context, targetKey string, limit int, principal principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	return s.processDueWorkflowExecutions(ctx, targetKey, time.Time{}, limit, principal, true, "", "")
}

// ProcessDueWorkflowContinuations consumes durable retry and Agent-task resume
// intents without also evaluating scheduled Workflow definitions. Scheduled
// definitions are owned by the Scheduler worker and must not run on every
// continuation poll.
func (s *WorkflowApplicationService) ProcessDueWorkflowContinuations(ctx context.Context, limit int, principal principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	if limit <= 0 {
		limit = 25
	} else if limit > 500 {
		limit = 500
	}
	if recovery, ok := s.workerRepo.(WorkflowContinuationRecoveryStore); ok && principal.SystemScope.Kind == principalmodel.SystemScopeRuntimeGlobal {
		workspaces, err := recovery.ListWorkflowContinuationWorkspaces(ctx, principal.SystemScope, max(32, limit*2))
		if err != nil {
			return workflowmodel.WorkflowProcessResult{}, internalError("list workflow continuation workspaces", err)
		}
		combined := workflowmodel.WorkflowProcessResult{}
		for _, workspaceID := range workspaces {
			if combined.Processed >= limit {
				break
			}
			workspacePrincipal := principal
			workspacePrincipal.WorkspaceID = workspaceID
			result, err := s.processDueWorkflowExecutions(ctx, "", time.Time{}, limit-combined.Processed, workspacePrincipal, false, "", "")
			if err != nil {
				return workflowmodel.WorkflowProcessResult{}, err
			}
			combined.Processed += result.Processed
			combined.Executions = append(combined.Executions, result.Executions...)
		}
		return combined, nil
	}
	return s.processDueWorkflowExecutions(ctx, "", time.Time{}, limit, principal, false, "", "")
}

func (s *WorkflowApplicationService) processDueWorkflowExecutions(ctx context.Context, targetKey string, scheduledFor time.Time, limit int, principal principalmodel.Principal, includeScheduled bool, exactExecutionID, callbackIdempotencyKey string) (workflowmodel.WorkflowProcessResult, error) {
	if err := workflowAuthorizeCommand(principal); err != nil {
		return workflowmodel.WorkflowProcessResult{}, err
	}
	ctx = requestcontext.WithWorkspaceID(ctx, principal.WorkspaceID)
	if limit <= 0 {
		limit = 25
	} else if limit > 500 {
		limit = 500
	}
	now := s.worker.Clock.Now()
	processed := make([]workflowmodel.WorkflowExecution, 0)
	if includeScheduled {
		var err error
		if scheduledFor.IsZero() {
			scheduledFor = now
		}
		processed, err = s.processScheduledWorkflowExecutionsForTargetWindowWithKey(ctx, targetKey, limit, principal, now, scheduledFor, callbackIdempotencyKey)
		if err != nil {
			return workflowmodel.WorkflowProcessResult{}, err
		}
	}
	if len(processed) >= limit {
		return workflowmodel.WorkflowProcessResult{Processed: len(processed), Executions: processed}, nil
	}
	if s.workerRepo == nil {
		return workflowmodel.WorkflowProcessResult{Processed: len(processed), Executions: processed}, nil
	}
	var executions []workflowmodel.WorkflowExecution
	if exactExecutionID != "" {
		execution, found, err := s.workerRepo.GetExecution(ctx, principal.WorkspaceID, exactExecutionID)
		if err != nil {
			return workflowmodel.WorkflowProcessResult{}, internalError("get workflow execution for worker", err)
		}
		if found {
			executions = []workflowmodel.WorkflowExecution{execution}
		}
	} else {
		var err error
		executions, err = s.workerRepo.ListExecutions(ctx, principal.WorkspaceID, 500)
		if err != nil {
			return workflowmodel.WorkflowProcessResult{}, internalError("list workflow executions for worker", err)
		}
	}
	for _, previous := range executions {
		if len(processed) >= limit {
			break
		}
		if !workflowpolicy.WorkflowExecutionDue(previous, now) {
			continue
		}
		claimed := previous
		claimed.Status = "running"
		claimed.LeaseOwner = s.worker.WorkerID.String()
		claimed.LeaseExpiresAt = now.Add(5 * time.Minute).Format(time.RFC3339Nano)
		claimed.FencingToken = previous.FencingToken + 1
		claimed.Message = "workflow.message.retryClaimed"
		claimed.NextRunAt = ""
		claimed.UpdatedAt = now.Format(time.RFC3339)
		claimed.Result = workflowpolicy.WorkflowCloneMap(claimed.Result)
		claimed.Result["worker_claimed_at"] = claimed.UpdatedAt
		updated, err := s.updateWorkflowExecutionIfCurrent(ctx, claimed, map[string]any{"status": previous.Status, "updated_at": previous.UpdatedAt, "lease_owner": previous.LeaseOwner, "fencing_token": previous.FencingToken})
		if err != nil {
			return workflowmodel.WorkflowProcessResult{}, internalError("claim workflow execution", err)
		}
		if !updated {
			continue
		}
		workflow, ok := workflowByKey(s.registry.List(), previous.WorkflowKey)
		if !ok {
			claimed.Status = "dead_letter"
			claimed.LastError = "backend.workflow.not_found"
			claimed.Message = "workflow.message.definitionNotFound"
			claimed.NextRunAt = ""
			claimed.UpdatedAt = now.Format(time.RFC3339)
			claimed.Result["dead_lettered"] = true
			if err := s.commitClaimedWorkflowExecution(ctx, claimed); err != nil {
				return workflowmodel.WorkflowProcessResult{}, internalError("dead-letter missing workflow execution", err)
			}
			continue
		}
		nextAttempt := previous.Attempt + 1
		if previous.Status == "pending" && previous.Attempt <= 0 {
			nextAttempt = 1
		}
		maxAttempts := workflowpolicy.WorkflowMaxAttempts(workflow)
		if nextAttempt > maxAttempts {
			claimed.Status = "dead_letter"
			claimed.LastError = valueOrDefault(previous.LastError, "backend.workflow.max_attempts_reached")
			claimed.Message = "workflow.message.maxAttemptsReached"
			claimed.UpdatedAt = s.worker.Clock.Now().Format(time.RFC3339)
			claimed.Result["dead_lettered"] = true
			if err := s.commitClaimedWorkflowExecution(ctx, claimed); err != nil {
				return workflowmodel.WorkflowProcessResult{}, internalError("dead-letter workflow execution", err)
			}
			continue
		}
		if resumeProcessID := strings.TrimSpace(fmt.Sprint(previous.Result["resume_process_id"])); resumeProcessID != "" && resumeProcessID != "<nil>" {
			process, found, err := s.workerRepo.GetProcess(ctx, principal.WorkspaceID, resumeProcessID)
			if err != nil {
				return workflowmodel.WorkflowProcessResult{}, internalError("load workflow continuation process", err)
			}
			if !found {
				claimed.Status, claimed.LastError, claimed.Message = "dead_letter", "backend.workflow.process_not_found", "workflow.message.processNotFound"
				claimed.UpdatedAt = s.worker.Clock.Now().Format(time.RFC3339)
				if err := s.commitClaimedWorkflowExecution(ctx, claimed); err != nil {
					return workflowmodel.WorkflowProcessResult{}, internalError("dead-letter missing continuation process", err)
				}
				continue
			}
			nodeIDs := workflowNodeIDsFromAny(previous.Result["resume_node_ids"])
			explicitResume, _ := previous.Result["resume_explicit"].(bool)
			if len(nodeIDs) == 0 {
				if explicitResume {
					nodeIDs = []string{}
				} else {
					nodeIDs = append([]string(nil), process.CurrentNodeIDs...)
				}
			}
			workCtx, stopHeartbeat := s.workflowExecutionHeartbeat(ctx, claimed)
			executionPrincipal, principalErr := s.agentTaskContinuationPrincipal(workCtx, previous, process, principal)
			var continued workflowmodel.WorkflowProcessInstance
			var runErr error
			if principalErr != nil {
				runErr = principalErr
			} else {
				continued, runErr = s.processEngine.RunWithContext(workCtx, process, nodeIDs, nil, executionPrincipal)
			}
			if heartbeatErr := stopHeartbeat(); runErr == nil && heartbeatErr != nil {
				return workflowmodel.WorkflowProcessResult{}, internalError("workflow continuation lease lost", heartbeatErr)
			}
			claimed.Attempt = nextAttempt
			claimed.UpdatedAt = s.worker.Clock.Now().Format(time.RFC3339)
			claimed.Result = workflowpolicy.WorkflowCloneMap(claimed.Result)
			if runErr != nil {
				workflowpolicy.WorkflowMarkFailed(&claimed, workflow, runErr, s.worker.Clock.Now())
				if err := s.commitClaimedWorkflowExecution(ctx, claimed); err != nil {
					return workflowmodel.WorkflowProcessResult{}, internalError("persist workflow continuation failure", err)
				}
				processed = append(processed, claimed)
				continue
			}
			claimed.Status, claimed.Message, claimed.LastError, claimed.NextRunAt = continued.Status, "workflow.message.continuationCompleted", "", ""
			claimed.NodeID = ""
			if len(continued.CurrentNodeIDs) > 0 {
				claimed.NodeID = continued.CurrentNodeIDs[0]
			}
			claimed.Result["process_status"], claimed.Result["current_node_ids"] = continued.Status, continued.CurrentNodeIDs
			delete(claimed.Result, "resume_node_ids")
			if err := s.commitClaimedWorkflowExecution(ctx, claimed); err != nil {
				return workflowmodel.WorkflowProcessResult{}, internalError("complete workflow continuation", err)
			}
			processed = append(processed, claimed)
			continue
		}
		payload := s.workflowRetryPayload(ctx, principal.WorkspaceID, previous, principal)
		workCtx, stopHeartbeat := s.workflowExecutionHeartbeat(ctx, claimed)
		execution, err := s.executeWorkflowAttempt(workCtx, workflow, payload, principal, "worker:"+previous.ID, nextAttempt, true)
		if heartbeatErr := stopHeartbeat(); err == nil && heartbeatErr != nil {
			return workflowmodel.WorkflowProcessResult{}, internalError("workflow execution lease lost", heartbeatErr)
		}
		if err != nil {
			return workflowmodel.WorkflowProcessResult{Processed: len(processed), Executions: processed}, err
		}
		claimed.Status = "skipped"
		claimed.Message = "workflow.message.retryContinued"
		claimed.UpdatedAt = s.worker.Clock.Now().Format(time.RFC3339)
		claimed.Result["continued_execution_id"] = execution.ID
		claimed.Result["continued_status"] = execution.Status
		if err := s.commitClaimedWorkflowExecution(ctx, claimed); err != nil {
			return workflowmodel.WorkflowProcessResult{}, internalError("mark workflow retry continuation", err)
		}
		processed = append(processed, execution)
	}
	return workflowmodel.WorkflowProcessResult{Processed: len(processed), Executions: processed}, nil
}

func (s *WorkflowApplicationService) agentTaskContinuationPrincipal(ctx context.Context, execution workflowmodel.WorkflowExecution, process workflowmodel.WorkflowProcessInstance, worker principalmodel.Principal) (principalmodel.Principal, error) {
	taskRunID := strings.TrimSpace(fmt.Sprint(execution.Result["agent_task_run_id"]))
	if taskRunID == "" {
		return worker, nil
	}
	if taskRunID == "<nil>" {
		return worker, nil
	}
	executionUserID := strings.TrimSpace(fmt.Sprint(execution.Result["execution_user_id"]))
	executionRoleKey := strings.TrimSpace(fmt.Sprint(execution.Result["execution_role_key"]))
	if executionUserID == "" {
		return principalmodel.Principal{}, apperror.New(apperror.KindForbidden, "backend.workflow.execution_principal_revoked", nil, nil)
	}
	if executionUserID == "<nil>" {
		return principalmodel.Principal{}, apperror.New(apperror.KindForbidden, "backend.workflow.execution_principal_revoked", nil, nil)
	}
	if executionRoleKey == "" {
		return principalmodel.Principal{}, apperror.New(apperror.KindForbidden, "backend.workflow.execution_principal_revoked", nil, nil)
	}
	if executionRoleKey == "<nil>" {
		return principalmodel.Principal{}, apperror.New(apperror.KindForbidden, "backend.workflow.execution_principal_revoked", nil, nil)
	}
	if s.principals == nil {
		return principalmodel.Principal{}, apperror.New(apperror.KindUnavailable, "backend.workflow.execution_principal_unavailable", nil, nil)
	}
	resolution, err := s.principals.Resolve(ctx, identitysdk.PrincipalResolutionRequest{SubjectID: identitysdk.SubjectID(executionUserID), RoleKey: executionRoleKey})
	if err != nil {
		return principalmodel.Principal{}, err
	}
	resolution.Principal.AccessBundle = &resolution.AccessBundle
	principal := principalmodel.NewPrincipalFromIdentity(resolution.Principal, "")
	if !principal.Known {
		return principalmodel.Principal{}, apperror.New(apperror.KindForbidden, "backend.workflow.execution_principal_revoked", nil, nil)
	}
	if strings.TrimSpace(principal.WorkspaceID) != strings.TrimSpace(process.WorkspaceID) {
		return principalmodel.Principal{}, apperror.New(apperror.KindForbidden, "backend.workflow.execution_principal_revoked", nil, nil)
	}
	if strings.TrimSpace(principal.UserID) != executionUserID {
		return principalmodel.Principal{}, apperror.New(apperror.KindForbidden, "backend.workflow.execution_principal_revoked", nil, nil)
	}
	if strings.TrimSpace(principal.RoleKey) != executionRoleKey {
		return principalmodel.Principal{}, apperror.New(apperror.KindForbidden, "backend.workflow.execution_principal_revoked", nil, nil)
	}
	principal.RequestID, principal.CorrelationID, principal.CausationID = worker.RequestID, worker.CorrelationID, worker.CausationID
	return principal, nil
}

func (s *WorkflowApplicationService) commitClaimedWorkflowExecution(ctx context.Context, execution workflowmodel.WorkflowExecution) error {
	owner, token := execution.LeaseOwner, execution.FencingToken
	execution.LeaseOwner, execution.LeaseExpiresAt = "", ""
	updated, err := s.updateWorkflowExecutionIfCurrent(ctx, execution, map[string]any{"status": "running", "lease_owner": owner, "fencing_token": token})
	if err != nil {
		return err
	}
	if !updated {
		return fmt.Errorf("backend.workflow.execution_lease_lost")
	}
	return nil
}

func (s *WorkflowApplicationService) workflowExecutionHeartbeat(ctx context.Context, execution workflowmodel.WorkflowExecution) (context.Context, func() error) {
	interval := s.workflowHeartbeatInterval
	if interval <= 0 {
		interval = time.Minute
	}
	return s.workflowExecutionHeartbeatWithInterval(ctx, execution, interval)
}

func (s *WorkflowApplicationService) workflowExecutionHeartbeatWithInterval(ctx context.Context, execution workflowmodel.WorkflowExecution, interval time.Duration) (context.Context, func() error) {
	return workerplatform.WithHeartbeat(ctx, interval, func(heartbeatCtx context.Context) error {
		heartbeat := execution
		now := s.worker.Clock.Now()
		heartbeat.LeaseExpiresAt = now.Add(5 * time.Minute).Format(time.RFC3339Nano)
		heartbeat.UpdatedAt = now.Format(time.RFC3339Nano)
		updated, err := s.updateWorkflowExecutionIfCurrent(heartbeatCtx, heartbeat, map[string]any{"status": "running", "lease_owner": execution.LeaseOwner, "fencing_token": execution.FencingToken})
		if err != nil {
			return err
		}
		if !updated {
			return fmt.Errorf("backend.workflow.execution_lease_lost")
		}
		return nil
	})
}

func workflowByKey(workflows []definitionmodel.WorkflowSchema, key string) (definitionmodel.WorkflowSchema, bool) {
	for _, workflow := range workflows {
		if workflow.Key == key {
			return workflow, true
		}
	}
	return definitionmodel.WorkflowSchema{}, false
}

func workflowNodeIDsFromAny(value any) []string {
	result := []string{}
	switch values := value.(type) {
	case []string:
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				result = append(result, value)
			}
		}
	case []any:
		for _, value := range values {
			if text := strings.TrimSpace(fmt.Sprint(value)); text != "" && text != "<nil>" {
				result = append(result, text)
			}
		}
	}
	return uniqueSortedStrings(result)
}

func (s *WorkflowApplicationService) updateWorkflowExecutionIfCurrent(ctx context.Context, execution workflowmodel.WorkflowExecution, conditions map[string]any) (bool, error) {
	return s.workerRepo.UpdateExecutionWhere(ctx, execution.WorkspaceID, execution, conditions)
}

func (s *WorkflowApplicationService) processScheduledWorkflowExecutions(ctx context.Context, limit int, principal principalmodel.Principal, now time.Time) ([]workflowmodel.WorkflowExecution, error) {
	return s.processScheduledWorkflowExecutionsForTarget(ctx, "", limit, principal, now)
}
