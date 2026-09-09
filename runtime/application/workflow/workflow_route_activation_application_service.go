package workflow

import (
	"context"
	"fmt"
	"strings"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

// WorkflowRouteStepEvents are the durable route events. They are enumerated
// here so every producer and every projection agrees on the vocabulary.
const (
	WorkflowRouteStepConfiguredEvent  = "route_step_configured"
	WorkflowRouteStepActivatedEvent   = "route_step_activated"
	WorkflowRouteStepRevalidatedEvent = "route_step_revalidated"
)

// activateStartingProcess turns a process staged by an Action into a running
// one. The trigger node was never executed inside the Action transaction, so
// the committed intent replays exactly the work Start would have done. It is
// idempotent: a process that already left "starting" is returned untouched.
func (e *WorkflowProcessEngine) activateStartingProcess(ctx context.Context, process workflowmodel.WorkflowProcessInstance, principal principalmodel.Principal) (workflowmodel.WorkflowProcessInstance, error) {
	if strings.TrimSpace(process.Status) != "starting" {
		return process, nil
	}
	graph := process.DefinitionSnapshot.Graph
	if graph == nil || len(graph.Nodes) == 0 {
		return e.failProcess(ctx, process, "backend.workflow.process_graph_required", principal.UserID)
	}
	e.appendEvent(ctx, process.WorkspaceID, process.ID, "", "", "process_started", process.InitiatorID, "workflow.event.process.started", map[string]any{"workflow_key": process.WorkflowKey})
	trigger := workflowpolicy.WorkflowGraphTrigger(graph)
	triggerInstance := e.newNodeInstance(ctx, process.WorkspaceID, process.ID, trigger, "success", process.Variables, map[string]any{"triggered": true})
	if err := e.runtime.dependencies.Processes.InsertNode(ctx, process.WorkspaceID, triggerInstance); err != nil {
		return e.failProcess(ctx, process, "backend.workflow.node_insert_failed", principal.UserID)
	}
	e.appendEvent(ctx, process.WorkspaceID, process.ID, trigger.ID, "", "node_completed", principal.UserID, trigger.Name, map[string]any{"node_type": trigger.Type})
	process.Status = "running"
	return e.runWithContext(ctx, process, e.nextNodeIDs(graph, trigger.ID, "success"), nil, principal)
}

// ActivateStartingProcess is the worker-visible entry point of the staged
// start activation.
func (e *WorkflowProcessEngine) ActivateStartingProcess(ctx context.Context, process workflowmodel.WorkflowProcessInstance, principal principalmodel.Principal) (workflowmodel.WorkflowProcessInstance, error) {
	if err := workflowAuthorizeCommand(principal); err != nil {
		return workflowmodel.WorkflowProcessInstance{}, err
	}
	return e.activateStartingProcess(ctx, process, principal)
}

// activateRouteApprovalStep opens the first durable route step of a
// route-driven approval node. The electorate comes from the frozen assignee
// snapshot, never from the node resolvers, and is revalidated against the live
// Identity projection according to the route's activation policy.
func (e *WorkflowProcessEngine) activateRouteApprovalStep(ctx context.Context, process *workflowmodel.WorkflowProcessInstance, node definitionmodel.WorkflowGraphNode, route definitionmodel.WorkflowApprovalRouteContract, principal principalmodel.Principal) (string, bool, error) {
	if e.runtime.dependencies.Routes == nil {
		return "", false, internalError("activate workflow route step", fmt.Errorf("workflow route store is required"))
	}
	steps, err := e.runtime.dependencies.Routes.ListRouteSteps(ctx, process.WorkspaceID, process.ID)
	if err != nil {
		return "", false, internalError("list workflow route steps", err)
	}
	step, found := workflowpolicy.WorkflowRouteStepAfter(steps, 0)
	if !found || step.NodeID != node.ID {
		return "", false, badRequest("backend.workflow.route_steps_missing", "node", node.ID)
	}
	if step.Status != "pending" {
		return "", false, badRequest("backend.workflow.route_step_not_activatable", "node", node.ID, "step_key", step.StepKey)
	}
	assignees, outcome, err := e.revalidateRouteStepAssignees(ctx, *process, node, route, step, principal)
	if err != nil {
		return "", false, err
	}
	if len(assignees) == 0 {
		if outcome == "skip" {
			return "skipped", false, nil
		}
		return "", false, badRequest("backend.workflow.approval_assignee_not_found", "node", node.ID)
	}
	step.AssigneeSnapshot = assignees
	nodeInstance := e.newNodeInstance(ctx, process.WorkspaceID, process.ID, node, "waiting", workflowpolicy.WorkflowCloneMap(process.Variables), nil)
	nodeInstance.Iteration = step.StepNo
	if err := e.runtime.dependencies.Processes.InsertNode(ctx, process.WorkspaceID, nodeInstance); err != nil {
		return "", false, internalError("insert route approval node", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	contract := workflowpolicy.WorkflowApprovalNodeContract(node)
	for index, task := range WorkflowRouteStepTasks(ctx, *process, node, contract, step, nodeInstance.ID, now) {
		if err := e.insertApprovalTaskWithNotification(ctx, *process, task, ""); err != nil {
			return "", false, internalError("insert workflow route task", err)
		}
		e.appendEvent(ctx, process.WorkspaceID, process.ID, node.ID, task.ID, "task_created", "system", task.Title, map[string]any{"assignee_user_id": task.AssigneeUserID, "mode": step.Mode, "sequence": index + 1, "step_key": step.StepKey})
	}
	step.Status, step.NodeInstanceID, step.UpdatedAt = "active", nodeInstance.ID, now
	activated, err := e.runtime.dependencies.Routes.UpdateRouteStepCAS(ctx, process.WorkspaceID, step, "pending")
	if err != nil {
		return "", false, internalError("activate workflow route step", err)
	}
	if !activated {
		return "", false, conflict("backend.workflow.route_step_not_activatable", "step_key", step.StepKey)
	}
	e.appendEvent(ctx, process.WorkspaceID, process.ID, node.ID, "", WorkflowRouteStepActivatedEvent, principal.UserID, step.Title, map[string]any{"step_key": step.StepKey, "step_no": step.StepNo, "assignee_count": len(assignees)})
	e.appendEvent(ctx, process.WorkspaceID, process.ID, node.ID, "", "approval_waiting", principal.UserID, node.Name, nil)
	return "waiting", true, nil
}

// WorkflowRouteStepTasks projects one durable route step onto the approval
// tasks that make its assignees the electorate of a node instance.
func WorkflowRouteStepTasks(ctx context.Context, process workflowmodel.WorkflowProcessInstance, node definitionmodel.WorkflowGraphNode, contract definitionmodel.WorkflowApprovalNodeContract, step workflowmodel.WorkflowRouteStep, nodeInstanceID, now string) []workflowmodel.WorkflowTask {
	createdAt, err := time.Parse(time.RFC3339Nano, now)
	if err != nil {
		createdAt = time.Now().UTC()
	}
	title := valueOrDefault(strings.TrimSpace(step.Title), valueOrDefault(strings.TrimSpace(contract.Title), node.Name))
	tasks := make([]workflowmodel.WorkflowTask, 0, len(step.AssigneeSnapshot))
	for index, assignee := range step.AssigneeSnapshot {
		tasks = append(tasks, workflowmodel.WorkflowTask{
			WorkspaceID: process.WorkspaceID, ID: workflowProcessID(ctx, "task"), ProcessID: process.ID, NodeInstanceID: nodeInstanceID,
			NodeID: node.ID, Title: title, AssigneeUserID: assignee.UserID, AssigneeName: assignee.DisplayName, AssigneeRoleKey: assignee.RoleKey,
			CandidateSource: "route", NodeDefinitionVersion: workflowpolicy.WorkflowGraphContractVersion(process.DefinitionSnapshot),
			Sequence: index + 1, Status: "open", DueAt: workflowpolicy.WorkflowApprovalDueAt(contract, node, createdAt),
			CreatedAt: now, UpdatedAt: now,
		})
	}
	return tasks
}

// revalidateRouteStepAssignees applies the route's activation policy to a
// snapshot that was frozen earlier: fail refuses to open a step whose approver
// is gone, skip_invalid drops them and falls back to the node's empty-assignee
// policy, and admin routes the step to the workspace administrators.
func (e *WorkflowProcessEngine) revalidateRouteStepAssignees(ctx context.Context, process workflowmodel.WorkflowProcessInstance, node definitionmodel.WorkflowGraphNode, route definitionmodel.WorkflowApprovalRouteContract, step workflowmodel.WorkflowRouteStep, principal principalmodel.Principal) ([]workflowmodel.WorkflowRouteAssignee, string, error) {
	electorate, err := workflowRouteEligibleAssignees(ctx, e.runtime.dependencies.Identity, process.DefinitionSnapshot, route)
	if err != nil {
		return nil, "", err
	}
	kept := make([]workflowmodel.WorkflowRouteAssignee, 0, len(step.AssigneeSnapshot))
	invalid := []string{}
	for _, assignee := range step.AssigneeSnapshot {
		if _, ok := electorate.Eligible[strings.TrimSpace(assignee.UserID)]; ok {
			kept = append(kept, assignee)
			continue
		}
		invalid = append(invalid, assignee.UserID)
	}
	if len(invalid) == 0 {
		return kept, "", nil
	}
	metadata := map[string]any{"step_key": step.StepKey, "step_no": step.StepNo, "invalid_user_ids": invalid, "policy": route.RevalidateOnActivation}
	e.appendEvent(ctx, process.WorkspaceID, process.ID, node.ID, "", WorkflowRouteStepRevalidatedEvent, principal.UserID, step.Title, metadata)
	switch route.RevalidateOnActivation {
	case "skip_invalid":
		if len(kept) > 0 {
			return kept, "", nil
		}
		return nil, valueOrDefault(strings.TrimSpace(workflowpolicy.WorkflowApprovalNodeContract(node).EmptyAssigneePolicy), "fail"), nil
	case "admin":
		admins, err := e.usersForApprovalRole(ctx, "admin")
		if err != nil {
			return nil, "", err
		}
		return workflowRouteAdminAssignees(ctx, e.runtime.dependencies.Identity, admins), "", nil
	default:
		return nil, "", badRequest("backend.workflow.route_step_revalidation_failed", "node", node.ID, "step_key", step.StepKey, "user_ids", strings.Join(invalid, ","))
	}
}

func workflowRouteAdminAssignees(ctx context.Context, identity identitysdk.Projection, userIDs []string) []workflowmodel.WorkflowRouteAssignee {
	assignees := make([]workflowmodel.WorkflowRouteAssignee, 0, len(userIDs))
	for _, userID := range userIDs {
		assignee := workflowmodel.WorkflowRouteAssignee{UserID: userID, RoleKey: "admin"}
		if identity != nil {
			if user, ok, err := identity.FindUser(ctx, identitysdk.UserLookup{UserID: identitysdk.SubjectID(userID)}); err == nil && ok {
				assignee.DisplayName = user.Name
			}
		}
		assignees = append(assignees, assignee)
	}
	return assignees
}
