package workflow

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"context"
	"fmt"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"strings"
	"time"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

type WorkflowProcessEngine struct {
	runtime *WorkflowProcessRuntime
}

func NewWorkflowProcessEngine(dependencies WorkflowDependencies) *WorkflowProcessEngine {
	return NewWorkflowProcessRuntime(dependencies).processEngine
}

func (e *WorkflowProcessEngine) Start(ctx context.Context, workflow definitionmodel.WorkflowSchema, variables map[string]any, principal principalmodel.Principal) (workflowmodel.WorkflowProcessInstance, error) {
	if err := workflowAuthorizeCommand(principal); err != nil {
		return workflowmodel.WorkflowProcessInstance{}, err
	}
	if workflow.Graph == nil || len(workflow.Graph.Nodes) == 0 {
		return workflowmodel.WorkflowProcessInstance{}, badRequest("backend.workflow.process_graph_required")
	}
	if err := workflowpolicy.WorkflowValidateGraph(workflow.Graph); err != nil {
		return workflowmodel.WorkflowProcessInstance{}, err
	}
	if e == nil || e.runtime == nil || e.runtime.dependencies.Processes == nil {
		return workflowmodel.WorkflowProcessInstance{}, internalError("start workflow process", fmt.Errorf("workflow process store is required"))
	}
	variables = workflowpolicy.WorkflowCloneMap(variables)
	objectKey := workflowpolicy.WorkflowPayloadString(variables, "object_key")
	if objectKey == "" && workflow.TriggerContract != nil {
		objectKey = strings.TrimSpace(workflow.TriggerContract.ObjectKey)
		if objectKey != "" {
			variables["object_key"] = objectKey
		}
	}
	recordID := workflowpolicy.WorkflowPayloadString(variables, "record_id")
	if recordID != "" {
		// Keep the canonical trigger target in the durable variables used by
		// approval continuations and $record.id rendering.
		variables["record_id"] = recordID
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	process := workflowmodel.WorkflowProcessInstance{
		WorkspaceID:         principal.WorkspaceID,
		ID:                  workflowProcessID(ctx, "process"),
		WorkflowKey:         workflow.Key,
		WorkflowName:        workflow.Name,
		DefinitionVersionID: workflow.DefinitionVersionID,
		DefinitionVersion:   workflowpolicy.WorkflowPublishedVersion(workflow),
		DefinitionHash:      workflowpolicy.WorkflowDefinitionHash(workflow),
		DefinitionSnapshot:  workflow,
		ObjectKey:           objectKey,
		RecordID:            recordID,
		InitiatorID:         principal.UserID,
		InitiatorRoleKey:    principal.RoleKey,
		Status:              "running",
		Variables:           variables,
		Result:              map[string]any{},
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	if err := e.runtime.dependencies.Processes.InsertProcess(ctx, principal.WorkspaceID, process); err != nil {
		return workflowmodel.WorkflowProcessInstance{}, internalError("insert workflow process", err)
	}
	e.appendEvent(ctx, process.WorkspaceID, process.ID, "", "", "process_started", principal.UserID, "workflow.event.process.started", map[string]any{"workflow_key": workflow.Key})
	trigger := workflowpolicy.WorkflowGraphTrigger(workflow.Graph)
	triggerInstance := e.newNodeInstance(ctx, process.WorkspaceID, process.ID, trigger, "success", process.Variables, map[string]any{"triggered": true})
	if err := e.runtime.dependencies.Processes.InsertNode(ctx, principal.WorkspaceID, triggerInstance); err != nil {
		return e.failProcess(ctx, process, "backend.workflow.node_insert_failed", principal.UserID)
	}
	e.appendEvent(ctx, process.WorkspaceID, process.ID, trigger.ID, "", "node_completed", principal.UserID, trigger.Name, map[string]any{"node_type": trigger.Type})
	return e.runWithContext(ctx, process, e.nextNodeIDs(workflow.Graph, trigger.ID, "success"), nil, principal)
}

func (e *WorkflowProcessEngine) DecideTask(ctx context.Context, taskID string, req workflowmodel.WorkflowTaskDecisionRequest, principal principalmodel.Principal) (workflowmodel.WorkflowProcessInstance, error) {
	if err := ctx.Err(); err != nil {
		return workflowmodel.WorkflowProcessInstance{}, err
	}
	if err := workflowAuthorizeCommand(principal); err != nil {
		return workflowmodel.WorkflowProcessInstance{}, err
	}
	decision := strings.ToLower(strings.TrimSpace(req.Decision))
	if decision != "approved" && decision != "rejected" && decision != "returned" {
		return workflowmodel.WorkflowProcessInstance{}, badRequest("backend.workflow.task_decision_invalid")
	}
	task, ok, err := e.runtime.dependencies.Processes.GetTask(ctx, principal.WorkspaceID, strings.TrimSpace(taskID))
	if err != nil {
		return workflowmodel.WorkflowProcessInstance{}, internalError("get workflow task", err)
	}
	if !ok {
		return workflowmodel.WorkflowProcessInstance{}, notFound("backend.workflow.task_not_found")
	}
	if task.AssigneeUserID != principal.UserID {
		return workflowmodel.WorkflowProcessInstance{}, forbidden("backend.workflow.task_assignee_required")
	}
	if err := workflowAuthorizeTaskDecision(principal, decision); err != nil {
		return workflowmodel.WorkflowProcessInstance{}, err
	}
	process, ok, err := e.runtime.dependencies.Processes.GetProcess(ctx, principal.WorkspaceID, task.ProcessID)
	if err != nil || !ok {
		return workflowmodel.WorkflowProcessInstance{}, internalError("get workflow process", err)
	}
	if strings.TrimSpace(req.IdempotencyKey) != "" {
		key := workflowCommandKey("task.decision", task.ID, req.IdempotencyKey, map[string]any{"decision": decision, "comment": strings.TrimSpace(req.Comment)})
		workflowRecordCommand(&process, "task.decision:"+task.ID, key)
	}
	taskSnapshots, err := e.runtime.dependencies.Processes.ListTasks(ctx, principal.WorkspaceID, process.ID, "", "", 500)
	if err != nil {
		return workflowmodel.WorkflowProcessInstance{}, internalError("snapshot workflow tasks", err)
	}
	nodeSnapshots, err := e.runtime.dependencies.Processes.ListNodes(ctx, principal.WorkspaceID, process.ID)
	if err != nil {
		return workflowmodel.WorkflowProcessInstance{}, internalError("snapshot workflow nodes", err)
	}
	processSnapshot := process
	now := time.Now().UTC().Format(time.RFC3339Nano)
	task, decided, err := e.runtime.dependencies.Processes.DecideTask(ctx, principal.WorkspaceID, task.ID, principal.UserID, decision, strings.TrimSpace(req.Comment), now)
	if err != nil {
		return workflowmodel.WorkflowProcessInstance{}, internalError("decide workflow task", err)
	}
	if !decided {
		return workflowmodel.WorkflowProcessInstance{}, conflict("backend.workflow.task_already_decided")
	}
	e.appendEvent(ctx, process.WorkspaceID, process.ID, task.NodeID, task.ID, "task_"+decision, principal.UserID, "workflow.event.task."+decision, map[string]any{"comment": task.Comment})
	outcome, complete, err := e.aggregateApproval(ctx, process, task.NodeID, principal)
	if err != nil {
		return e.recoverWorkflowDecision(ctx, processSnapshot, nodeSnapshots, taskSnapshots, task, err, principal.UserID, nil)
	}
	if !complete {
		if strings.TrimSpace(req.IdempotencyKey) != "" {
			process.UpdatedAt = now
			if err := e.runtime.dependencies.Processes.UpdateProcess(ctx, process.WorkspaceID, process); err != nil {
				return process, internalError("persist workflow decision idempotency", err)
			}
		}
		return process, nil
	}
	if err := e.completeApprovalNode(ctx, principal.WorkspaceID, process.ID, task.NodeID, outcome, principal.UserID); err != nil {
		return e.recoverWorkflowDecision(ctx, processSnapshot, nodeSnapshots, taskSnapshots, task, err, principal.UserID, nil)
	}
	process.Variables["approval_decision"] = outcome
	process.Variables["approval_comment"] = task.Comment
	waiting := workflowpolicy.WorkflowRemoveString(process.CurrentNodeIDs, task.NodeID)
	if (outcome == "rejected" || outcome == "returned") && len(e.nextNodeIDs(process.DefinitionSnapshot.Graph, task.NodeID, outcome)) == 0 {
		process.Status = "rejected"
		process.CurrentNodeIDs = waiting
		process.CompletedAt = now
		process.UpdatedAt = now
		if err := e.runtime.dependencies.Processes.UpdateProcess(ctx, process.WorkspaceID, process); err != nil {
			return process, internalError("reject workflow process", err)
		}
		e.appendEvent(ctx, process.WorkspaceID, process.ID, task.NodeID, task.ID, "process_rejected", principal.UserID, "workflow.event.process.rejected", nil)
		return process, nil
	}
	next := e.nextNodeIDs(process.DefinitionSnapshot.Graph, task.NodeID, outcome)
	result, err := e.runWithContext(ctx, process, next, waiting, principal)
	if err != nil {
		return e.recoverWorkflowDecision(ctx, processSnapshot, nodeSnapshots, taskSnapshots, task, err, principal.UserID, nil)
	}
	return result, nil
}

func (e *WorkflowProcessEngine) runWithContext(ctx context.Context, process workflowmodel.WorkflowProcessInstance, queue []string, waiting []string, principal principalmodel.Principal) (workflowmodel.WorkflowProcessInstance, error) {
	graph := process.DefinitionSnapshot.Graph
	visited := map[string]bool{}
	for len(queue) > 0 {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return process, err
			}
		}
		nodeID := queue[0]
		queue = queue[1:]
		if visited[nodeID] {
			continue
		}
		visited[nodeID] = true
		node, ok := workflowpolicy.WorkflowGraphNode(graph, nodeID)
		if !ok {
			return e.failProcess(ctx, process, "backend.workflow.graph_node_missing", principal.UserID)
		}
		outcome, nodeWaiting, err := e.executeNode(ctx, &process, node, principal)
		if err != nil {
			return e.failProcess(ctx, process, serviceErrorCode(err), principal.UserID)
		}
		if nodeWaiting {
			waiting = appendUniqueString(waiting, node.ID)
			continue
		}
		queue = append(queue, e.nextNodeIDs(graph, node.ID, outcome)...)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	process.UpdatedAt = now
	process.CurrentNodeIDs = uniqueSortedStrings(waiting)
	if len(waiting) > 0 {
		process.Status = "waiting"
	} else {
		process.Status = "completed"
		process.CompletedAt = now
	}
	if err := e.runtime.dependencies.Processes.UpdateProcess(ctx, process.WorkspaceID, process); err != nil {
		return process, internalError("update workflow process", err)
	}
	if process.Status == "completed" {
		e.appendEvent(ctx, process.WorkspaceID, process.ID, "", "", "process_completed", principal.UserID, "workflow.event.process.completed", nil)
	}
	return process, nil
}

func (e *WorkflowProcessEngine) executeNode(ctx context.Context, process *workflowmodel.WorkflowProcessInstance, node definitionmodel.WorkflowGraphNode, principal principalmodel.Principal) (string, bool, error) {
	input := workflowpolicy.WorkflowCloneMap(process.Variables)
	switch node.Type {
	case "condition":
		matched := workflowProcessConditionMatches(ctx, node, input)
		outcome := "false"
		if matched {
			outcome = "true"
		}
		return outcome, false, e.recordCompletedNode(ctx, *process, node, input, map[string]any{"matched": matched}, principal.UserID)
	case "approval":
		nodeInstance := e.newNodeInstance(ctx, process.WorkspaceID, process.ID, node, "waiting", input, nil)
		if err := e.runtime.dependencies.Processes.InsertNode(ctx, process.WorkspaceID, nodeInstance); err != nil {
			return "", false, internalError("insert approval node", err)
		}
		taskCount, err := e.createApprovalTasks(ctx, *process, node, nodeInstance, principal)
		if err != nil {
			nodeInstance.Status = "configuration_error"
			nodeInstance.ErrorCode = serviceErrorCode(err)
			nodeInstance.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
			_ = e.runtime.dependencies.Processes.UpdateNode(ctx, process.WorkspaceID, nodeInstance)
			return "", false, err
		}
		if taskCount == 0 {
			nodeInstance.Status = "skipped"
			nodeInstance.Output = map[string]any{"reason": "assignee_not_found", "policy": "skip"}
			nodeInstance.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
			if err := e.runtime.dependencies.Processes.UpdateNode(ctx, process.WorkspaceID, nodeInstance); err != nil {
				return "", false, internalError("skip approval node", err)
			}
			e.appendEvent(ctx, process.WorkspaceID, process.ID, node.ID, "", "approval_skipped", principal.UserID, node.Name, nodeInstance.Output)
			return "skipped", false, nil
		}
		e.appendEvent(ctx, process.WorkspaceID, process.ID, node.ID, "", "approval_waiting", principal.UserID, node.Name, nil)
		return "waiting", true, nil
	case "agent_task":
		return e.executeAgentTaskNode(ctx, process, node, principal)
	case "action":
		output, err := e.executeActionNode(ctx, process, node, principal)
		if err != nil {
			e.recordFailedNode(ctx, *process, node, input, err, principal.UserID)
			switch valueOrDefault(strings.TrimSpace(workflowpolicy.WorkflowBusinessActionNodeContract(node).OnError), "fail") {
			case "continue":
				return "success", false, nil
			case "error_branch":
				return "error", false, nil
			default:
				return "", false, err
			}
		}
		return "success", false, e.recordCompletedNode(ctx, *process, node, input, output, principal.UserID)
	case "cc":
		output, err := e.executeCCNode(ctx, *process, node, principal)
		if err != nil {
			e.recordFailedNode(ctx, *process, node, input, err, principal.UserID)
			return "", false, err
		}
		return "success", false, e.recordCompletedNode(ctx, *process, node, input, output, principal.UserID)
	case "wait_until", "wait_duration", "timer":
		if e.runtime.dependencies.WaitTimers == nil {
			return "", false, internalError("schedule workflow timer", fmt.Errorf("workflow wait timer service is required"))
		}
		nodeInstance := e.newNodeInstance(ctx, process.WorkspaceID, process.ID, node, "waiting", input, nil)
		if err := e.runtime.dependencies.Processes.InsertNode(ctx, process.WorkspaceID, nodeInstance); err != nil {
			return "", false, internalError("insert timer node", err)
		}
		timerID, err := e.runtime.dependencies.WaitTimers.ScheduleWorkflowWaitTimer(ctx, WorkflowWaitTimerRequest{
			WorkspaceID: process.WorkspaceID, ProcessID: process.ID, NodeID: node.ID, ObjectKey: process.ObjectKey, RecordID: process.RecordID,
			Contract: workflowpolicy.WorkflowTimerNodeContract(node), Variables: workflowpolicy.WorkflowCloneMap(process.Variables), CreatedAt: time.Now().UTC(),
		})
		if err != nil {
			nodeInstance.Status, nodeInstance.ErrorCode, nodeInstance.CompletedAt = "configuration_error", serviceErrorCode(err), time.Now().UTC().Format(time.RFC3339Nano)
			_ = e.runtime.dependencies.Processes.UpdateNode(ctx, process.WorkspaceID, nodeInstance)
			return "", false, err
		}
		nodeInstance.Output = map[string]any{"timer_id": timerID}
		if err := e.runtime.dependencies.Processes.UpdateNode(ctx, process.WorkspaceID, nodeInstance); err != nil {
			return "", false, internalError("update timer node", err)
		}
		e.appendEvent(ctx, process.WorkspaceID, process.ID, node.ID, "", "timer_waiting", principal.UserID, node.Name, nodeInstance.Output)
		return "waiting", true, nil
	default:
		return "", false, badRequest("backend.workflow.graph_node_type_invalid", "node", node.ID)
	}
}

func workflowProcessConditionMatches(ctx context.Context, node definitionmodel.WorkflowGraphNode, variables map[string]any) bool {
	if node.Contract != nil && node.Contract.Condition != nil {
		after := mapFromAny(variables["after"])
		if len(after) == 0 {
			after = variables
		}
		return workflowpolicy.WorkflowConditionContractMatches(ctx, *node.Contract.Condition, after, mapFromAny(variables["before"]))
	}
	return workflowpolicy.WorkflowConditionExpressionMatches(ctx, strings.TrimSpace(fmt.Sprint(node.Config["expression"])), variables)
}

func (e *WorkflowProcessEngine) recordCompletedNode(ctx context.Context, process workflowmodel.WorkflowProcessInstance, node definitionmodel.WorkflowGraphNode, input, output map[string]any, actorID string) error {
	instance := e.newNodeInstance(ctx, process.WorkspaceID, process.ID, node, "success", input, output)
	if err := e.runtime.dependencies.Processes.InsertNode(ctx, process.WorkspaceID, instance); err != nil {
		return internalError("insert workflow node", err)
	}
	e.appendEvent(ctx, process.WorkspaceID, process.ID, node.ID, "", "node_completed", actorID, node.Name, map[string]any{"node_type": node.Type})
	return nil
}

func (e *WorkflowProcessEngine) newNodeInstance(ctx context.Context, workspaceID, processID string, node definitionmodel.WorkflowGraphNode, status string, input, output map[string]any) workflowmodel.WorkflowNodeInstance {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	iteration := e.nextNodeIteration(ctx, workspaceID, processID, node.ID)
	completedAt := ""
	if status != "waiting" && status != "running" {
		completedAt = now
	}
	return workflowmodel.WorkflowNodeInstance{WorkspaceID: workspaceID, ID: workflowProcessID(ctx, "node"), ProcessID: processID, NodeID: node.ID, NodeType: node.Type, Iteration: iteration, Status: status, Input: workflowpolicy.WorkflowCloneMap(input), Output: workflowpolicy.WorkflowCloneMap(output), StartedAt: now, CompletedAt: completedAt}
}

func (e *WorkflowProcessEngine) nextNodeIteration(ctx context.Context, workspaceID, processID, nodeID string) int {
	iteration := 1
	if existing, err := e.runtime.dependencies.Processes.ListNodes(ctx, workspaceID, processID); err == nil {
		for _, instance := range existing {
			if instance.NodeID == nodeID && instance.Iteration >= iteration {
				iteration = instance.Iteration + 1
			}
		}
	}
	return iteration
}

func (e *WorkflowProcessEngine) nextNodeIDs(graph *definitionmodel.WorkflowGraphSchema, sourceID, outcome string) []string {
	if graph == nil {
		return nil
	}
	out := []string{}
	for _, edge := range graph.Edges {
		if edge.Source != sourceID || !workflowpolicy.WorkflowEdgeMatches(edge, outcome) {
			continue
		}
		out = appendUniqueString(out, edge.Target)
	}
	return out
}
