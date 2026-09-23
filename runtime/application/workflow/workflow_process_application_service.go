package workflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"

	"strings"
	"time"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
	workflowprojection "github.com/domainry/domainry-runtime/runtime/domain/workflow/projection"
)

type WorkflowProcessDetail struct {
	Process workflowmodel.WorkflowProcessInstance `json:"process"`
	Nodes   []workflowmodel.WorkflowNodeInstance  `json:"nodes"`
	Tasks   []workflowmodel.WorkflowTask          `json:"tasks"`
	Events  []workflowmodel.WorkflowProcessEvent  `json:"events"`
	Route   *workflowprojection.WorkflowRouteView `json:"route,omitempty"`
}

func (s *WorkflowApplicationService) enrichWorkflowProcessSummary(ctx context.Context, process workflowmodel.WorkflowProcessInstance) workflowmodel.WorkflowProcessInstance {
	var nodes []workflowmodel.WorkflowNodeInstance
	if process.Status == "failed" || process.Status == "configuration_error" {
		nodes, _ = s.processRepo.ListNodes(ctx, process.WorkspaceID, process.ID)
	}
	return enrichWorkflowProcessSummaryWithNodes(process, nodes)
}

func enrichWorkflowProcessSummaryWithNodes(process workflowmodel.WorkflowProcessInstance, nodes []workflowmodel.WorkflowNodeInstance) workflowmodel.WorkflowProcessInstance {
	nodeNames := map[string]string{}
	if process.DefinitionSnapshot.Graph != nil {
		for _, node := range process.DefinitionSnapshot.Graph.Nodes {
			nodeNames[node.ID] = node.Name
		}
	}
	for _, nodeID := range process.CurrentNodeIDs {
		if name := strings.TrimSpace(nodeNames[nodeID]); name != "" {
			process.CurrentNodeNames = append(process.CurrentNodeNames, name)
		}
	}
	if process.Status == "waiting" {
		if updatedAt, err := time.Parse(time.RFC3339Nano, process.UpdatedAt); err == nil {
			process.WaitingSeconds = int64(time.Since(updatedAt).Seconds())
		}
	}
	process.WorkflowRetryCount = workflowpolicy.WorkflowRetryCount(process.Result["retry_count"])
	if outcome, ok := process.Variables["approval_decision"]; ok {
		process.BusinessOutcome = strings.TrimSpace(fmt.Sprint(outcome))
	}
	if process.Status == "failed" || process.Status == "configuration_error" {
		for _, node := range nodes {
			if node.Status != "failed" && node.Status != "configuration_error" {
				continue
			}
			process.FailedNodeNames = appendUniqueString(process.FailedNodeNames, valueOrDefault(nodeNames[node.NodeID], node.NodeID))
		}
	}
	return process
}

func (s *WorkflowApplicationService) workflowTasksWithAssigneeNames(ctx context.Context, tasks []workflowmodel.WorkflowTask) []workflowmodel.WorkflowTask {
	if s.identity == nil || len(tasks) == 0 {
		return tasks
	}
	users, err := s.identity.ListUsers(ctx, identitysdk.ProjectionQuery{})
	if err != nil {
		return tasks
	}
	names := make(map[string]string, len(users))
	for _, user := range users {
		names[user.ID] = user.Name
	}
	for index := range tasks {
		if name, ok := names[tasks[index].AssigneeUserID]; ok {
			tasks[index].AssigneeName = name
		}
	}
	return tasks
}

func (s *WorkflowApplicationService) workflowProcessVisible(ctx context.Context, process workflowmodel.WorkflowProcessInstance, principal principalmodel.Principal) bool {
	if process.InitiatorID == principal.UserID {
		return true
	}
	tasks, err := s.processRepo.ListTasks(ctx, principal.WorkspaceID, process.ID, principal.UserID, "", 1)
	if err == nil && len(tasks) > 0 {
		return true
	}
	return false
}

func (s *WorkflowApplicationService) WorkflowProcesses(ctx context.Context, principal principalmodel.Principal, filter workflowmodel.WorkflowProcessFilter) ([]workflowmodel.WorkflowProcessInstance, error) {
	return s.workflowProcesses(ctx, principal, filter, true, false)
}

func (s *WorkflowApplicationService) workflowProcesses(ctx context.Context, principal principalmodel.Principal, filter workflowmodel.WorkflowProcessFilter, restrictToPrincipal, advanced bool) ([]workflowmodel.WorkflowProcessInstance, error) {
	if err := workflowAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	filter.WorkflowKey = strings.TrimSpace(filter.WorkflowKey)
	filter.ProcessID = strings.TrimSpace(filter.ProcessID)
	filter.ObjectKey = strings.TrimSpace(filter.ObjectKey)
	filter.RecordID = strings.TrimSpace(filter.RecordID)
	filter.Status = strings.TrimSpace(filter.Status)
	filter.Statuses = normalizeWorkflowProcessStatuses(filter.Statuses)
	filter.InitiatorID = strings.TrimSpace(filter.InitiatorID)
	filter.ApproverID = strings.TrimSpace(filter.ApproverID)
	if restrictToPrincipal {
		filter.VisibleToUserID = principal.UserID
	}
	processes, err := s.processRepo.ListProcesses(ctx, principal.WorkspaceID, filter)
	if err != nil {
		return nil, internalError("list workflow processes", err)
	}
	visible := make([]workflowmodel.WorkflowProcessInstance, 0, len(processes))
	actions := s.workflowProjectionActions(ctx, principal)
	nodesByProcess, batchLoaded := s.loadWorkflowProcessSummaryNodes(ctx, principal.WorkspaceID, processes)
	for _, process := range processes {
		if batchLoaded {
			process = enrichWorkflowProcessSummaryWithNodes(process, nodesByProcess[process.ID])
		} else {
			process = s.enrichWorkflowProcessSummary(ctx, process)
		}
		visible = append(visible, workflowprojection.WorkflowProcessForPrincipal(process, advanced, actions))
	}
	return visible, nil
}

func normalizeWorkflowProcessStatuses(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, rawValue := range values {
		for _, candidate := range strings.Split(rawValue, ",") {
			status := strings.TrimSpace(candidate)
			if status == "" {
				continue
			}
			if _, exists := seen[status]; exists {
				continue
			}
			seen[status] = struct{}{}
			result = append(result, status)
		}
	}
	return result
}

func (s *WorkflowApplicationService) MyWorkflowTasks(ctx context.Context, principal principalmodel.Principal, status string, limit int) ([]workflowmodel.WorkflowTask, error) {
	if err := workflowAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	status = strings.TrimSpace(status)
	if status == "" {
		status = "open"
	}
	tasks, err := s.processRepo.ListTasks(ctx, principal.WorkspaceID, "", principal.UserID, status, limit)
	if err != nil {
		return nil, internalError("list my workflow tasks", err)
	}
	tasks = s.workflowTasksWithAssigneeNames(ctx, tasks)
	for index := range tasks {
		tasks[index] = workflowprojection.WorkflowTaskForPrincipal(tasks[index], false)
	}
	return tasks, nil
}

func (s *WorkflowApplicationService) WorkflowProcess(ctx context.Context, processID string, principal principalmodel.Principal) (WorkflowProcessDetail, error) {
	return s.workflowProcess(ctx, processID, principal, true, false)
}

func (s *WorkflowApplicationService) workflowProcess(ctx context.Context, processID string, principal principalmodel.Principal, requireVisible, advanced bool) (WorkflowProcessDetail, error) {
	if err := workflowAuthorizeQuery(principal); err != nil {
		return WorkflowProcessDetail{}, err
	}
	process, ok, err := s.processRepo.GetProcess(ctx, principal.WorkspaceID, strings.TrimSpace(processID))
	if err != nil {
		return WorkflowProcessDetail{}, internalError("get workflow process", err)
	}
	if !ok {
		return WorkflowProcessDetail{}, notFound("backend.workflow.process_not_found")
	}
	if requireVisible && !s.workflowProcessVisible(ctx, process, principal) {
		return WorkflowProcessDetail{}, forbidden("backend.workflow.process_access_denied")
	}
	nodes, err := s.processRepo.ListNodes(ctx, principal.WorkspaceID, process.ID)
	if err != nil {
		return WorkflowProcessDetail{}, internalError("list workflow process nodes", err)
	}
	tasks, err := s.processRepo.ListTasks(ctx, principal.WorkspaceID, process.ID, "", "", 500)
	if err != nil {
		return WorkflowProcessDetail{}, internalError("list workflow process tasks", err)
	}
	events, err := s.processRepo.ListEvents(ctx, principal.WorkspaceID, process.ID, 1000)
	if err != nil {
		return WorkflowProcessDetail{}, internalError("list workflow process events", err)
	}
	tasks = s.workflowTasksWithAssigneeNames(ctx, tasks)
	for index := range nodes {
		nodes[index] = workflowprojection.WorkflowNodeForPrincipal(nodes[index], advanced)
	}
	for index := range tasks {
		tasks[index] = workflowprojection.WorkflowTaskForPrincipal(tasks[index], advanced)
	}
	for index := range events {
		events[index] = workflowprojection.WorkflowEventForPrincipal(events[index], advanced)
	}
	return WorkflowProcessDetail{
		Process: workflowprojection.WorkflowProcessForPrincipal(process, advanced, s.workflowProjectionActions(ctx, principal)),
		Nodes:   nodes, Tasks: tasks, Events: events, Route: s.workflowProcessRouteView(ctx, process, tasks, principal),
	}, nil
}

func (s *WorkflowApplicationService) CancelWorkflowProcess(ctx context.Context, processID string, principal principalmodel.Principal) (workflowmodel.WorkflowProcessInstance, error) {
	return s.CancelWorkflowProcessWithKey(ctx, processID, "system:"+strings.TrimSpace(processID)+":cancel", principal)
}

func (s *WorkflowApplicationService) CancelWorkflowProcessWithKey(ctx context.Context, processID, callerKey string, principal principalmodel.Principal) (workflowmodel.WorkflowProcessInstance, error) {
	if err := workflowAuthorizeCommand(principal); err != nil {
		return workflowmodel.WorkflowProcessInstance{}, err
	}
	process, ok, err := s.processRepo.GetProcess(ctx, principal.WorkspaceID, strings.TrimSpace(processID))
	if err != nil {
		return workflowmodel.WorkflowProcessInstance{}, internalError("get workflow process", err)
	}
	if !ok {
		return workflowmodel.WorkflowProcessInstance{}, notFound("backend.workflow.process_not_found")
	}
	if process.InitiatorID != principal.UserID {
		return workflowmodel.WorkflowProcessInstance{}, forbidden("backend.workflow.process_cancel_denied")
	}
	commandKey := workflowCommandKey("process.cancel", process.ID, callerKey, map[string]any{"command": "cancel"})
	if workflowProcessHasCommand(process, "process.cancel", commandKey) {
		return workflowprojection.WorkflowProcessForPrincipal(process, false, s.workflowProjectionActions(ctx, principal)), nil
	}
	// Record-bound withdrawals must enter through the business Action so its
	// receipt and released occupancy cannot be bypassed by the generic route.
	if process.ObjectKey != "" && process.RecordID != "" {
		return workflowmodel.WorkflowProcessInstance{}, conflict("backend.workflow.business_withdrawal_required")
	}
	commit, err := prepareWorkflowWithdrawal(process, callerKey, principal)
	if err != nil {
		return workflowmodel.WorkflowProcessInstance{}, err
	}
	store, ok := s.processRepo.(workflowcontract.WorkflowWithdrawalStore)
	if !ok {
		return workflowmodel.WorkflowProcessInstance{}, internalError("atomic workflow withdrawal unavailable", nil)
	}
	if err := store.CommitWorkflowWithdrawal(ctx, commit); err != nil {
		return workflowmodel.WorkflowProcessInstance{}, err
	}
	process = commit.Process

	return workflowprojection.WorkflowProcessForPrincipal(process, false, s.workflowProjectionActions(ctx, principal)), nil
}

func (s *WorkflowApplicationService) RetryWorkflowProcess(ctx context.Context, processID string, principal principalmodel.Principal) (workflowmodel.WorkflowProcessInstance, error) {
	return s.RetryWorkflowProcessWithKey(ctx, processID, "system:"+strings.TrimSpace(processID)+":retry", principal)
}

func (s *WorkflowApplicationService) RetryWorkflowProcessWithKey(ctx context.Context, processID, callerKey string, principal principalmodel.Principal) (workflowmodel.WorkflowProcessInstance, error) {
	if err := ctx.Err(); err != nil {
		return workflowmodel.WorkflowProcessInstance{}, err
	}
	if err := workflowAuthorizeCommand(principal); err != nil {
		return workflowmodel.WorkflowProcessInstance{}, err
	}
	process, ok, err := s.processRepo.GetProcess(ctx, principal.WorkspaceID, strings.TrimSpace(processID))
	if err != nil {
		return process, internalError("get retry workflow process", err)
	}
	if !ok {
		return process, notFound("backend.workflow.process_not_found")
	}
	commandKey := workflowCommandKey("process.retry", process.ID, callerKey, map[string]any{"command": "retry"})
	if workflowProcessHasCommand(process, "process.retry", commandKey) {
		return workflowprojection.WorkflowProcessForPrincipal(process, false, s.workflowProjectionActions(ctx, principal)), nil
	}
	if process.Status != "configuration_error" && process.Status != "failed" {
		return process, conflict("backend.workflow.process_not_retryable")
	}
	nodes, err := s.processRepo.ListNodes(ctx, principal.WorkspaceID, process.ID)
	if err != nil {
		return process, internalError("list retry workflow nodes", err)
	}
	failedNodeID := ""
	for index := len(nodes) - 1; index >= 0; index-- {
		if nodes[index].Status == "failed" || nodes[index].Status == "configuration_error" {
			failedNodeID = nodes[index].NodeID
			break
		}
	}
	if failedNodeID == "" {
		return process, conflict("backend.workflow.process_failed_node_missing")
	}
	if process.Result == nil {
		process.Result = map[string]any{}
	}
	process.Result["retry_count"] = workflowpolicy.WorkflowRetryCount(process.Result["retry_count"]) + 1
	workflowRecordCommand(&process, "process.retry", commandKey)
	process.OperationID = requestcontext.OwnerExecutionID(ctx)
	process.Status, process.ErrorCode, process.CompletedAt = "running", "", ""
	process.CurrentNodeIDs = []string{failedNodeID}
	process.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.processRepo.UpdateProcess(ctx, principal.WorkspaceID, process); err != nil {
		return process, internalError("prepare workflow process retry", err)
	}
	s.processEngine.appendEvent(ctx, process.WorkspaceID, process.ID, failedNodeID, "", "process_retry_started", principal.UserID, "workflow.event.process.retryStarted", map[string]any{"retry_count": process.Result["retry_count"]})
	retried, retryErr := s.processEngine.RunWithContext(ctx, process, []string{failedNodeID}, nil, principal)
	return workflowprojection.WorkflowProcessForPrincipal(retried, false, s.workflowProjectionActions(ctx, principal)), retryErr
}

func (s *WorkflowApplicationService) ResolveWorkflowProcessFailure(ctx context.Context, processID, note string, principal principalmodel.Principal) (workflowmodel.WorkflowProcessInstance, error) {
	if err := workflowAuthorizeCommand(principal); err != nil {
		return workflowmodel.WorkflowProcessInstance{}, err
	}
	if strings.TrimSpace(note) == "" {
		return workflowmodel.WorkflowProcessInstance{}, badRequest("backend.workflow.process_resolution_note_required")
	}
	process, ok, err := s.processRepo.GetProcess(ctx, principal.WorkspaceID, strings.TrimSpace(processID))
	if err != nil {
		return process, internalError("get workflow process resolution", err)
	}
	if !ok {
		return process, notFound("backend.workflow.process_not_found")
	}
	if process.Status != "configuration_error" && process.Status != "failed" {
		return process, conflict("backend.workflow.process_not_resolvable")
	}
	process.Status = "resolved"
	process.OperationID = requestcontext.OwnerExecutionID(ctx)
	process.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if process.CompletedAt == "" {
		process.CompletedAt = process.UpdatedAt
	}
	stateStore, ok := s.processEngine.runtime.dependencies.Decisions.(workflowcontract.WorkflowStateStore)
	if !ok {
		return process, internalError("resolve workflow process", errors.New("workflow state store unavailable"))
	}
	event := workflowDecisionEvent(ctx, process.ID, "", "", "process_failure_resolved", principal.UserID, "workflow.event.process.failureResolved", map[string]any{"note": strings.TrimSpace(note), "error_code": process.ErrorCode}, process.UpdatedAt)
	if err := stateStore.CommitWorkflowState(ctx, transactionmodel.WorkflowStateCommit{WorkspaceID: principal.WorkspaceID, Process: &process, Events: []workflowmodel.WorkflowProcessEvent{event}}); err != nil {
		return process, internalError("resolve workflow process", err)
	}
	return workflowprojection.WorkflowProcessForPrincipal(process, false, s.workflowProjectionActions(ctx, principal)), nil
}

func (s *WorkflowApplicationService) DecideTask(ctx context.Context, taskID string, req workflowmodel.WorkflowTaskDecisionRequest, principal principalmodel.Principal) (workflowmodel.WorkflowProcessInstance, error) {
	if err := ctx.Err(); err != nil {
		return workflowmodel.WorkflowProcessInstance{}, err
	}
	if err := workflowAuthorizeCommand(principal); err != nil {
		return workflowmodel.WorkflowProcessInstance{}, err
	}
	if strings.TrimSpace(req.IdempotencyKey) != "" {
		task, found, err := s.processRepo.GetTask(ctx, principal.WorkspaceID, strings.TrimSpace(taskID))
		if err != nil {
			return workflowmodel.WorkflowProcessInstance{}, internalError("get workflow task idempotency state", err)
		}
		if found {
			if task.AssigneeUserID != principal.UserID {
				return workflowmodel.WorkflowProcessInstance{}, forbidden("backend.workflow.task_assignee_required")
			}
			if err := workflowAuthorizeTaskDecision(principal, req.Decision); err != nil {
				return workflowmodel.WorkflowProcessInstance{}, err
			}
			process, processFound, getErr := s.processRepo.GetProcess(ctx, principal.WorkspaceID, task.ProcessID)
			if getErr != nil {
				return workflowmodel.WorkflowProcessInstance{}, internalError("get workflow decision idempotency state", getErr)
			}
			key := workflowCommandKey("task.decision", task.ID, req.IdempotencyKey, map[string]any{"decision": strings.ToLower(strings.TrimSpace(req.Decision)), "comment": strings.TrimSpace(req.Comment)})
			if processFound && workflowProcessHasCommand(process, "task.decision:"+task.ID, key) {
				return workflowprojection.WorkflowProcessForPrincipal(process, false, s.workflowProjectionActions(ctx, principal)), nil
			}
		}
	}
	if process, handled, err := s.decisions.DecideTerminalTask(ctx, strings.TrimSpace(taskID), req, principal); handled {
		return workflowprojection.WorkflowProcessForPrincipal(process, false, s.workflowProjectionActions(ctx, principal)), err
	}
	return workflowmodel.WorkflowProcessInstance{}, internalError("commit workflow task decision", errors.New("transactional workflow decision store unavailable"))
}
