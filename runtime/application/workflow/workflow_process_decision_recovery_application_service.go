package workflow

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"context"
	"errors"

	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"

	"sort"
	"strings"
	"time"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

func prepareNextApprovalNodes(ctx context.Context, records *WorkflowProcessRuntime, commit *transactionmodel.WorkflowDecisionCommit, process workflowmodel.WorkflowProcessInstance, nodeIDs []string, principal principalmodel.Principal, now string) (bool, error) {
	assigneeNames := map[string]string{}
	if records.dependencies.Identity != nil {
		users, err := records.dependencies.Identity.ListUsers(ctx, identitysdk.ProjectionQuery{})
		if err != nil {
			users = nil
		}
		for _, user := range users {
			assigneeNames[user.ID] = user.Name
		}
	}
	nodes := make([]definitionmodel.WorkflowGraphNode, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		node, ok := workflowpolicy.WorkflowGraphNode(process.DefinitionSnapshot.Graph, nodeID)
		if !ok || node.Type != "approval" {
			return false, nil
		}
		nodes = append(nodes, node)
	}
	for _, node := range nodes {
		assignees, roleKey, err := records.processEngine.resolveApprovalAssignees(ctx, process, node, principal)
		if err != nil {
			return false, err
		}
		contract := workflowpolicy.WorkflowApprovalNodeContract(node)
		if len(assignees) == 0 {
			if valueOrDefault(strings.TrimSpace(contract.EmptyAssigneePolicy), "fail") == "skip" {
				return false, nil
			}
			return false, badRequest("backend.workflow.approval_assignee_not_found", "node", node.ID)
		}
		instance := records.processEngine.newNodeInstance(ctx, process.WorkspaceID, process.ID, node, "waiting", process.Variables, nil)
		commit.InsertNodes = append(commit.InsertNodes, instance)
		commit.Events = append(commit.Events, workflowDecisionEvent(ctx, process.ID, node.ID, "", "approval_waiting", principal.UserID, node.Name, nil, now))
		mode := valueOrDefault(strings.TrimSpace(contract.Mode), "any")
		for index, assignee := range assignees {
			status := "open"
			if mode == "sequential" && index > 0 {
				status = "pending"
			}
			title := valueOrDefault(strings.TrimSpace(contract.Title), node.Name)
			task := workflowmodel.WorkflowTask{ID: workflowProcessID(ctx, "task"), ProcessID: process.ID, NodeInstanceID: instance.ID, NodeID: node.ID, Title: title, AssigneeUserID: assignee, AssigneeRoleKey: roleKey, ResolverSnapshot: workflowpolicy.WorkflowOrderedApprovalResolvers(contract.Resolvers), CandidateSource: workflowpolicy.WorkflowCandidateSource(workflowpolicy.WorkflowOrderedApprovalResolvers(contract.Resolvers)), NodeDefinitionVersion: workflowpolicy.WorkflowGraphContractVersion(process.DefinitionSnapshot), Sequence: index + 1, Status: status, DueAt: workflowpolicy.WorkflowApprovalDueAt(contract, node, time.Now().UTC()), CreatedAt: now, UpdatedAt: now}
			if name, ok := assigneeNames[assignee]; ok {
				task.AssigneeName = name
			}
			commit.InsertTasks = append(commit.InsertTasks, task)
			commit.Events = append(commit.Events, workflowDecisionEvent(ctx, process.ID, node.ID, task.ID, "task_created", "system", task.Title, map[string]any{"assignee_user_id": assignee, "mode": mode, "sequence": task.Sequence}, now))
		}
	}
	return true, nil
}

// recoverWorkflowDecision restores the durable approval cursor when a
// downstream domain action fails. Business mutations are committed through
// their own atomic batch; restoring these snapshots keeps the task retryable
// instead of leaving an approved task attached to a failed process.
func (e *WorkflowProcessEngine) recoverWorkflowDecision(
	ctx context.Context,
	process workflowmodel.WorkflowProcessInstance,
	nodes []workflowmodel.WorkflowNodeInstance,
	tasks []workflowmodel.WorkflowTask,
	decidedTask workflowmodel.WorkflowTask,
	decisionErr error,
	actorID string,
	recoveryExecution *workflowmodel.WorkflowExecution,
) (workflowmodel.WorkflowProcessInstance, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for index := range tasks {
		tasks[index].UpdatedAt = now
	}
	process.UpdatedAt = now
	stateStore, ok := e.runtime.dependencies.Decisions.(workflowcontract.WorkflowStateStore)
	if !ok {
		return workflowmodel.WorkflowProcessInstance{}, internalError("restore workflow decision state", errors.New("workflow state store unavailable"))
	}
	event := workflowDecisionEvent(ctx, process.ID, decidedTask.NodeID, decidedTask.ID, "task_decision_rolled_back", actorID, serviceErrorCode(decisionErr), nil, now)
	if err := stateStore.CommitWorkflowState(ctx, transactionmodel.WorkflowStateCommit{WorkspaceID: process.WorkspaceID, Process: &process, UpdateNodes: nodes, UpdateTasks: tasks, Events: []workflowmodel.WorkflowProcessEvent{event}, WorkflowExecution: recoveryExecution}); err != nil {
		return workflowmodel.WorkflowProcessInstance{}, internalError("restore workflow decision state", err)
	}
	return process, decisionErr
}

func decideTerminalWorkflowTaskWithContext(ctx context.Context, records *WorkflowProcessRuntime, taskID string, req workflowmodel.WorkflowTaskDecisionRequest, principal principalmodel.Principal) (workflowmodel.WorkflowProcessInstance, bool, error) {
	decision := strings.ToLower(strings.TrimSpace(req.Decision))
	if decision != "approved" && decision != "rejected" && decision != "returned" {
		return workflowmodel.WorkflowProcessInstance{}, true, badRequest("backend.workflow.task_decision_invalid")
	}
	if records.dependencies.Decisions == nil {
		return workflowmodel.WorkflowProcessInstance{}, false, nil
	}
	if !principal.Known {
		return workflowmodel.WorkflowProcessInstance{}, true, forbidden("backend.role.unknown")
	}
	task, ok, err := records.dependencies.Processes.GetTask(ctx, principal.WorkspaceID, taskID)
	if err != nil {
		return workflowmodel.WorkflowProcessInstance{}, true, internalError("get workflow task", err)
	}
	if !ok {
		return workflowmodel.WorkflowProcessInstance{}, true, notFound("backend.workflow.task_not_found")
	}
	if task.AssigneeUserID != principal.UserID {
		return workflowmodel.WorkflowProcessInstance{}, true, forbidden("backend.workflow.task_assignee_required")
	}
	if err := workflowAuthorizeTaskDecision(principal, decision); err != nil {
		return workflowmodel.WorkflowProcessInstance{}, true, err
	}
	if task.Status != "open" {
		if req.IdempotencyKey != "" {
			if replay, found := workflowDecisionReplay(ctx, principal.WorkspaceID, records.dependencies.Processes, task, req); found {
				return replay, true, nil
			}
		}
		return workflowmodel.WorkflowProcessInstance{}, true, conflict("backend.workflow.task_already_decided")
	}
	process, ok, err := records.dependencies.Processes.GetProcess(ctx, principal.WorkspaceID, task.ProcessID)
	if err != nil || !ok {
		return workflowmodel.WorkflowProcessInstance{}, true, internalError("get workflow process", err)
	}
	if strings.TrimSpace(req.IdempotencyKey) != "" {
		key := workflowCommandKey("task.decision", task.ID, req.IdempotencyKey, workflowTaskDecisionCommandPayload(decision, req))
		workflowRecordCommand(&process, "task.decision:"+task.ID, key)
	}
	node, _ := workflowpolicy.WorkflowGraphNode(process.DefinitionSnapshot.Graph, task.NodeID)
	route, routed := workflowpolicy.WorkflowApprovalRoute(node)
	if !routed && req.NextStep != nil {
		return workflowmodel.WorkflowProcessInstance{}, true, badRequest("backend.workflow.next_step_not_configurable", "node", task.NodeID)
	}
	tasks, err := workflowApprovalDecisionTasks(ctx, records.dependencies.Processes, process, task)
	if err != nil {
		return workflowmodel.WorkflowProcessInstance{}, true, internalError("list workflow tasks", err)
	}
	nodes, err := records.dependencies.Processes.ListNodes(ctx, principal.WorkspaceID, process.ID)
	if err != nil {
		return workflowmodel.WorkflowProcessInstance{}, true, internalError("list workflow nodes", err)
	}
	processSnapshot, taskSnapshot := process, task
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if now == process.UpdatedAt {
		at, _ := time.Parse(time.RFC3339Nano, now)
		now = at.Add(time.Nanosecond).Format(time.RFC3339Nano)
	}
	// Every next-step rule is evaluated before the vote is written, so a
	// rejected configuration leaves the task open and the route untouched.
	plan := workflowRouteDecisionPlan{}
	if routed {
		plan, err = planRouteDecision(ctx, records, process, node, route, task, req, decision, principal, now)
		if err != nil {
			return workflowmodel.WorkflowProcessInstance{}, true, err
		}
	}
	task.Status, task.Decision, task.Comment = decision, decision, strings.TrimSpace(req.Comment)
	task.CompletedBy, task.CompletedAt, task.UpdatedAt = principal.UserID, now, now
	commit := workflowApprovalDecisionCommit(process, task)
	// Every vote shares the process revision with withdrawal, including partial
	// votes in any/all/sequential routes. A stale vote must be recomputed.
	commit.ExpectedProcessUpdatedAt = processSnapshot.UpdatedAt
	if req.NextStep != nil {
		// A configuration and a vote share one durable revision, so two
		// approvers cannot both believe they configured the next step.
		commit.ExpectedProcessUpdatedAt = processSnapshot.UpdatedAt
	}
	if commit.ExpectedProcessUpdatedAt != "" {
		process.UpdatedAt = now
		commit.Process = &process
	}
	outcome, complete := workflowpolicy.WorkflowTerminalApprovalOutcome(process, tasks, task)
	if routed {
		outcome, complete = workflowpolicy.WorkflowTerminalRouteApprovalOutcome(plan.current, tasks, task)
	}
	if !complete {
		if strings.TrimSpace(req.IdempotencyKey) != "" {
			commit.Process = &process
		}
		commit.Events = append(commit.Events, workflowDecisionEvent(ctx, process.ID, task.NodeID, task.ID, "task_"+decision, principal.UserID, "workflow.event.task."+decision, map[string]any{"comment": task.Comment}, now))
		if routed {
			if _, err := applyRouteDecision(ctx, records, &commit, &process, node, plan, outcome, false, principal, now); err != nil {
				return workflowmodel.WorkflowProcessInstance{}, true, err
			}
		}
		if !routed && valueOrDefault(strings.TrimSpace(workflowpolicy.WorkflowApprovalNodeContract(node).Mode), "any") == "sequential" {
			pending := []workflowmodel.WorkflowTask{}
			for _, candidate := range tasks {
				if candidate.NodeID == task.NodeID && candidate.Status == "pending" {
					pending = append(pending, candidate)
				}
			}
			sort.Slice(pending, func(i, j int) bool { return pending[i].Sequence < pending[j].Sequence })
			for _, nextTask := range pending {
				nextTask.Status, nextTask.UpdatedAt = "open", now
				commit.UpdateTasks = append(commit.UpdateTasks, nextTask)
				commit.Events = append(commit.Events, workflowDecisionEvent(ctx, process.ID, task.NodeID, nextTask.ID, "task_opened", "system", nextTask.Title, nil, now))
				break
			}
		}
		if err := prepareWorkflowDecisionNotifications(records, &commit, process, now); err != nil {
			return workflowmodel.WorkflowProcessInstance{}, true, err
		}
		committed, err := records.dependencies.Decisions.CommitWorkflowDecision(ctx, commit)
		if err != nil {
			return workflowmodel.WorkflowProcessInstance{}, true, internalError("commit workflow task decision", err)
		}
		if !committed {
			if replay, found := workflowDecisionReplay(ctx, principal.WorkspaceID, records.dependencies.Processes, task, req); found {
				return replay, true, nil
			}
			return workflowmodel.WorkflowProcessInstance{}, true, conflict("backend.workflow.task_already_decided")
		}
		return process, true, nil
	}
	nextNodeIDs := records.processEngine.nextNodeIDs(process.DefinitionSnapshot.Graph, task.NodeID, outcome)
	commit.Events = append(commit.Events, workflowDecisionEvent(ctx, process.ID, task.NodeID, task.ID, "task_"+decision, principal.UserID, "workflow.event.task."+decision, map[string]any{"comment": task.Comment}, now))
	for _, sibling := range tasks {
		if sibling.ID == task.ID || sibling.NodeID != task.NodeID || sibling.NodeInstanceID != task.NodeInstanceID || (sibling.Status != "open" && sibling.Status != "pending") {
			continue
		}
		sibling.Status, sibling.UpdatedAt = "cancelled", now
		commit.UpdateTasks = append(commit.UpdateTasks, sibling)
		commit.Events = append(commit.Events, workflowDecisionEvent(ctx, process.ID, task.NodeID, sibling.ID, "task_cancelled", principal.UserID, sibling.Title, nil, now))
	}
	nodeFound := false
	for _, node := range nodes {
		if node.NodeID != task.NodeID || node.Status != "waiting" || (task.NodeInstanceID != "" && node.ID != task.NodeInstanceID) {
			continue
		}
		nodeFound = true
		node.Status, node.Output, node.CompletedAt = outcome, map[string]any{"decision": outcome}, now
		commit.UpdateNodes = append(commit.UpdateNodes, node)
		commit.Events = append(commit.Events, workflowDecisionEvent(ctx, process.ID, task.NodeID, "", "approval_"+outcome, principal.UserID, "workflow.event.approval."+outcome, nil, now))
		break
	}
	if !nodeFound {
		return workflowmodel.WorkflowProcessInstance{}, true, notFound("backend.workflow.approval_node_instance_not_found")
	}
	process.Variables = workflowpolicy.WorkflowCloneMap(process.Variables)
	process.Variables["approval_decision"], process.Variables["approval_comment"] = outcome, task.Comment
	if routed {
		activated, routeErr := applyRouteDecision(ctx, records, &commit, &process, node, plan, outcome, true, principal, now)
		if routeErr != nil {
			return workflowmodel.WorkflowProcessInstance{}, true, routeErr
		}
		if activated {
			commit.Process = &process
			return commitRouteApprovalContinuation(ctx, records, commit, process, task, req, principal, now)
		}
		if outcome == "approved" && plan.hasNext && plan.next.Status == "configurable" && plan.configured == nil {
			routeDecisionConfigurationError(&process, plan.next, now)
			commit.Process = &process
			commit.Events = append(commit.Events, workflowDecisionEvent(ctx, process.ID, task.NodeID, task.ID, "process_failed", principal.UserID, process.ErrorCode, map[string]any{"step_key": plan.next.StepKey}, now))
			return commitRouteApprovalContinuation(ctx, records, commit, process, task, req, principal, now)
		}
	}
	if len(nextNodeIDs) > 0 {
		preparedApprovals, prepareErr := prepareNextApprovalNodes(ctx, records, &commit, process, nextNodeIDs, principal, now)
		if prepareErr != nil {
			return workflowmodel.WorkflowProcessInstance{}, true, prepareErr
		}
		if preparedApprovals {
			process.Status, process.CurrentNodeIDs, process.CompletedAt, process.UpdatedAt = "waiting", uniqueSortedStrings(nextNodeIDs), "", now
			commit.Process = &process
			execution, found := workflowmodel.WorkflowExecution{}, false
			if records.dependencies.Workers != nil {
				var loadErr error
				execution, found, loadErr = records.dependencies.Workers.GetExecution(ctx, principal.WorkspaceID, process.ID)
				if loadErr != nil {
					return workflowmodel.WorkflowProcessInstance{}, true, internalError("get workflow execution", loadErr)
				}
			}
			if !found {
				execution = workflowDecisionExecution(process, principal, now)
			}
			execution.Status, execution.UpdatedAt, execution.Message = "waiting", now, "workflow.message.approvalWaiting"
			execution.Result = workflowpolicy.WorkflowCloneMap(execution.Result)
			execution.Result["process_id"], execution.Result["current_node_ids"], execution.Result["process_status"] = process.ID, process.CurrentNodeIDs, process.Status
			if found {
				commit.WorkflowExecution = &execution
			} else {
				commit.InsertExecutions = append(commit.InsertExecutions, execution)
			}
			if err := prepareWorkflowDecisionNotifications(records, &commit, process, now); err != nil {
				return workflowmodel.WorkflowProcessInstance{}, true, err
			}
			committed, err := records.dependencies.Decisions.CommitWorkflowDecision(ctx, commit)
			if err != nil {
				return workflowmodel.WorkflowProcessInstance{}, true, internalError("commit workflow task decision", err)
			}
			if !committed {
				if replay, found := workflowDecisionReplay(ctx, principal.WorkspaceID, records.dependencies.Processes, task, req); found {
					return replay, true, nil
				}
				return workflowmodel.WorkflowProcessInstance{}, true, conflict("backend.workflow.task_already_decided")
			}
			return process, true, nil
		}
		process.Status, process.CurrentNodeIDs, process.CompletedAt, process.UpdatedAt = "running", uniqueSortedStrings(nextNodeIDs), "", now
		commit.Process = &process
		commit.Events = append(commit.Events, workflowDecisionEvent(ctx, process.ID, task.NodeID, task.ID, "workflow_continuation_queued", principal.UserID, "workflow.event.continuation.queued", map[string]any{"node_ids": nextNodeIDs}, now))
		execution, found := workflowmodel.WorkflowExecution{}, false
		if records.dependencies.Workers != nil {
			var loadErr error
			execution, found, loadErr = records.dependencies.Workers.GetExecution(ctx, principal.WorkspaceID, process.ID)
			if loadErr != nil {
				return workflowmodel.WorkflowProcessInstance{}, true, internalError("get workflow execution", loadErr)
			}
		}
		if !found {
			execution = workflowDecisionExecution(process, principal, now)
		}
		execution.Status, execution.Attempt, execution.UpdatedAt = "pending", 0, now
		execution.Message, execution.LastError, execution.NextRunAt = "workflow.message.continuationQueued", "", ""
		execution.Result = workflowpolicy.WorkflowCloneMap(execution.Result)
		execution.Result["transactional_continuation"], execution.Result["resume_process_id"], execution.Result["resume_node_ids"] = true, process.ID, nextNodeIDs
		if found {
			commit.WorkflowExecution = &execution
		} else {
			commit.InsertExecutions = append(commit.InsertExecutions, execution)
		}
		if err := prepareWorkflowDecisionNotifications(records, &commit, process, now); err != nil {
			return workflowmodel.WorkflowProcessInstance{}, true, err
		}
		committed, err := records.dependencies.Decisions.CommitWorkflowDecision(ctx, commit)
		if err != nil {
			return workflowmodel.WorkflowProcessInstance{}, true, internalError("commit workflow task decision", err)
		}
		if !committed {
			if replay, found := workflowDecisionReplay(ctx, principal.WorkspaceID, records.dependencies.Processes, task, req); found {
				return replay, true, nil
			}
			return workflowmodel.WorkflowProcessInstance{}, true, conflict("backend.workflow.task_already_decided")
		}
		if records.dependencies.WakeWorkflowContinuation != nil {
			records.dependencies.WakeWorkflowContinuation(execution.WorkspaceID, execution.ID)
		}
		if records.dependencies.Workers == nil {
			return process, true, nil
		}
		if continued, claimed, resumeErr := runCommittedWorkflowContinuation(ctx, records, execution, principal, processSnapshot, nodes, tasks, taskSnapshot); resumeErr != nil {
			return process, true, resumeErr
		} else if claimed {
			return continued, true, nil
		}
		return process, true, nil
	}
	process.Status = "rejected"
	processEvent, processSummary := "process_rejected", "workflow.event.process.rejected"
	if outcome == "approved" {
		process.Status, processEvent, processSummary = "completed", "process_completed", "workflow.event.process.completed"
	}
	process.CurrentNodeIDs, process.CompletedAt, process.UpdatedAt = workflowpolicy.WorkflowRemoveString(process.CurrentNodeIDs, task.NodeID), now, now
	commit.Process = &process
	commit.Events = append(commit.Events, workflowDecisionEvent(ctx, process.ID, task.NodeID, task.ID, processEvent, principal.UserID, processSummary, nil, now))
	if records.dependencies.Workers != nil {
		if execution, found, loadErr := records.dependencies.Workers.GetExecution(ctx, principal.WorkspaceID, process.ID); loadErr != nil {
			return workflowmodel.WorkflowProcessInstance{}, true, internalError("get workflow execution", loadErr)
		} else if found {
			execution.ProcessID, execution.Status, execution.UpdatedAt = process.ID, process.Status, process.UpdatedAt
			execution.Result = workflowpolicy.WorkflowCloneMap(execution.Result)
			execution.Result["process_id"], execution.Result["current_node_ids"], execution.Result["process_status"] = process.ID, process.CurrentNodeIDs, process.Status
			execution.NodeID, execution.LastError, execution.NextRunAt = "", "", ""
			delete(execution.Result, "node_id")
			commit.WorkflowExecution = &execution
		}
	}
	if err := prepareWorkflowDecisionNotifications(records, &commit, process, now); err != nil {
		return workflowmodel.WorkflowProcessInstance{}, true, err
	}
	committed, err := records.dependencies.Decisions.CommitWorkflowDecision(ctx, commit)
	if err != nil {
		return workflowmodel.WorkflowProcessInstance{}, true, internalError("commit workflow task decision", err)
	}
	if !committed {
		if replay, found := workflowDecisionReplay(ctx, principal.WorkspaceID, records.dependencies.Processes, task, req); found {
			return replay, true, nil
		}
		return workflowmodel.WorkflowProcessInstance{}, true, conflict("backend.workflow.task_already_decided")
	}
	return process, true, nil
}

func (r *WorkflowProcessRuntime) DecideTerminalTask(ctx context.Context, taskID string, req workflowmodel.WorkflowTaskDecisionRequest, principal principalmodel.Principal) (workflowmodel.WorkflowProcessInstance, bool, error) {
	if err := workflowAuthorizeCommand(principal); err != nil {
		return workflowmodel.WorkflowProcessInstance{}, true, err
	}
	for attempt := 0; attempt < 5; attempt++ {
		if err := ctx.Err(); err != nil {
			return workflowmodel.WorkflowProcessInstance{}, true, err
		}
		process, handled, err := decideTerminalWorkflowTaskWithContext(ctx, r, taskID, req, principal)
		if !errors.Is(err, workflowcontract.ErrWorkflowDecisionSnapshotChanged) {
			return process, handled, err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return workflowmodel.WorkflowProcessInstance{}, true, ctx.Err()
		case <-timer.C:
		}
	}
	return workflowmodel.WorkflowProcessInstance{}, true, conflict("backend.workflow.task_decision_conflict")
}

// workflowTaskDecisionCommandPayload is the exact request identity a decision
// idempotency key covers. The next-step configuration is part of it, so the
// same key with the same configuration replays and the same key with a
// different configuration conflicts.
func workflowTaskDecisionCommandPayload(decision string, req workflowmodel.WorkflowTaskDecisionRequest) map[string]any {
	payload := map[string]any{"decision": decision, "comment": strings.TrimSpace(req.Comment)}
	if req.NextStep != nil {
		payload["next_step"] = map[string]any{
			"step_key":           strings.TrimSpace(req.NextStep.StepKey),
			"assignee_user_ids":  uniqueSortedStrings(req.NextStep.AssigneeUserIDs),
			"required_approvals": req.NextStep.RequiredApprovals,
		}
	}
	return payload
}

// commitRouteApprovalContinuation persists a route decision that keeps the
// process on the same approval node, refreshing the durable execution so a
// crash resumes the process instead of the workflow payload.
func commitRouteApprovalContinuation(ctx context.Context, records *WorkflowProcessRuntime, commit transactionmodel.WorkflowDecisionCommit, process workflowmodel.WorkflowProcessInstance, task workflowmodel.WorkflowTask, req workflowmodel.WorkflowTaskDecisionRequest, principal principalmodel.Principal, now string) (workflowmodel.WorkflowProcessInstance, bool, error) {
	execution, found := workflowmodel.WorkflowExecution{}, false
	if records.dependencies.Workers != nil {
		var loadErr error
		execution, found, loadErr = records.dependencies.Workers.GetExecution(ctx, principal.WorkspaceID, process.ID)
		if loadErr != nil {
			return workflowmodel.WorkflowProcessInstance{}, true, internalError("get workflow execution", loadErr)
		}
	}
	if !found {
		execution = workflowDecisionExecution(process, principal, now)
	}
	execution.Status, execution.UpdatedAt, execution.Message = process.Status, now, "workflow.message.approvalWaiting"
	execution.Result = workflowpolicy.WorkflowCloneMap(execution.Result)
	execution.Result["process_id"], execution.Result["current_node_ids"], execution.Result["process_status"] = process.ID, process.CurrentNodeIDs, process.Status
	if found {
		commit.WorkflowExecution = &execution
	} else {
		commit.InsertExecutions = append(commit.InsertExecutions, execution)
	}
	if err := prepareWorkflowDecisionNotifications(records, &commit, process, now); err != nil {
		return workflowmodel.WorkflowProcessInstance{}, true, err
	}
	committed, err := records.dependencies.Decisions.CommitWorkflowDecision(ctx, commit)
	if err != nil {
		return workflowmodel.WorkflowProcessInstance{}, true, internalError("commit workflow task decision", err)
	}
	if !committed {
		if replay, found := workflowDecisionReplay(ctx, principal.WorkspaceID, records.dependencies.Processes, task, req); found {
			return replay, true, nil
		}
		return workflowmodel.WorkflowProcessInstance{}, true, conflict("backend.workflow.task_already_decided")
	}
	return process, true, nil
}
