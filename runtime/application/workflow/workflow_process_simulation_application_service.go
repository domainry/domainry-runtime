package workflow

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"context"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"strings"

	"github.com/domainry/domainry-foundation/requestcontext"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

func (e *WorkflowProcessEngine) Simulate(ctx context.Context, workflow definitionmodel.WorkflowSchema, variables map[string]any, principal principalmodel.Principal) ([]workflowmodel.WorkflowSimulationNode, error) {
	if err := workflowAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	ctx = requestcontext.WithWorkspaceID(ctx, principal.WorkspaceID)
	if err := workflowpolicy.WorkflowValidateGraph(workflow.Graph); err != nil {
		return nil, err
	}
	process := workflowmodel.WorkflowProcessInstance{
		WorkflowKey: workflow.Key, WorkflowName: workflow.Name, DefinitionVersionID: workflow.DefinitionVersionID, DefinitionVersion: workflowpolicy.WorkflowPublishedVersion(workflow),
		DefinitionSnapshot: workflow, ObjectKey: workflowpolicy.WorkflowPayloadString(variables, "object_key"), RecordID: workflowpolicy.WorkflowPayloadString(variables, "record_id"),
		InitiatorID: principal.UserID, InitiatorRoleKey: principal.RoleKey, Variables: workflowpolicy.WorkflowCloneMap(variables),
	}
	trigger := workflowpolicy.WorkflowGraphTrigger(workflow.Graph)
	queue := []string{trigger.ID}
	visited := map[string]bool{}
	nodes := []workflowmodel.WorkflowSimulationNode{}
	for len(queue) > 0 {
		nodeID := queue[0]
		queue = queue[1:]
		if visited[nodeID] {
			continue
		}
		visited[nodeID] = true
		// WorkflowValidateGraph above guarantees that the trigger and every edge
		// target resolve in this immutable graph snapshot.
		node, _ := workflowpolicy.WorkflowGraphNode(workflow.Graph, nodeID)
		preview, outcomes, err := e.simulateNode(ctx, process, node, principal)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, preview)
		for _, outcome := range outcomes {
			queue = append(queue, e.nextNodeIDs(workflow.Graph, node.ID, outcome)...)
		}
	}
	return nodes, nil
}

func (e *WorkflowProcessEngine) simulateNode(ctx context.Context, process workflowmodel.WorkflowProcessInstance, node definitionmodel.WorkflowGraphNode, principal principalmodel.Principal) (workflowmodel.WorkflowSimulationNode, []string, error) {
	preview := workflowmodel.WorkflowSimulationNode{NodeID: node.ID, NodeType: node.Type, Status: "simulated"}
	switch node.Type {
	case "trigger":
		preview.Outcome = "success"
		return preview, []string{"success"}, nil
	case "condition":
		matched := workflowProcessConditionMatches(ctx, node, process.Variables)
		preview.Outcome = "false"
		if matched {
			preview.Outcome = "true"
		}
		preview.Details = map[string]any{"matched": matched}
		return preview, []string{preview.Outcome}, nil
	case "approval":
		assignees, roleKey, err := e.resolveApprovalAssignees(ctx, process, node, principal)
		if err != nil {
			return preview, nil, err
		}
		preview.ResolvedAssignees = assignees
		preview.Details = map[string]any{"resolver_mode": workflowpolicy.WorkflowApprovalNodeContract(node).ResolverMode, "role_key": roleKey, "empty_assignee_policy": workflowpolicy.WorkflowApprovalNodeContract(node).EmptyAssigneePolicy}
		return preview, []string{"approved", "rejected"}, nil
	case "action":
		contract := workflowpolicy.WorkflowBusinessActionNodeContract(node)
		preview.ActionKey = contract.ActionKey
		// action existence is provided by the composition root
		exists := e.runtime.dependencies.ActionExists(ctx, strings.TrimSpace(contract.ActionKey))

		if !exists {
			return preview, nil, badRequest("backend.workflow.action_not_found", "action", contract.ActionKey)
		}
		outcomes := []string{"success"}
		if contract.OnError == "error_branch" {
			outcomes = append(outcomes, "error")
		}
		return preview, outcomes, nil
	case "agent_task":
		contract := node.Contract.AgentTask
		preview.Outcome = "waiting"
		preview.Details = map[string]any{
			"task_key": contract.TaskKey, "task_version": contract.TaskVersion, "identity_mode": contract.Identity.Mode,
			"service_principal_key": contract.Identity.PrincipalKey, "output_variable": contract.OutputVariable,
			"timeout_seconds": contract.TimeoutSeconds, "max_attempts": contract.Retry.MaxAttempts,
			"model_invoked": false, "durable_task_created": false, "business_side_effects": false,
		}
		return preview, append([]string(nil), contract.AllowedOutcomes...), nil
	case "cc":
		contract := workflowpolicy.WorkflowCCNodeContract(node)
		preview.ActionKey = contract.NotificationActionKey
		assignees, err := e.resolveWorkflowRecipients(ctx, process, contract.Resolvers, principal)
		if err != nil {
			return preview, nil, err
		}
		preview.ResolvedAssignees = assignees
		return preview, []string{"success"}, nil
	case "wait_until", "wait_duration", "timer":
		contract := workflowpolicy.WorkflowTimerNodeContract(node)
		preview.Outcome = "scheduled"
		preview.Details = map[string]any{"timer_key": contract.TimerKey, "purpose": contract.Purpose, "at": contract.At, "duration_seconds": contract.DurationSeconds, "source_field": contract.SourceField, "offset_seconds": contract.OffsetSeconds, "timezone": contract.Timezone, "business_calendar_key": contract.BusinessCalendarKey}
		return preview, []string{"success"}, nil
	default:
		return preview, nil, badRequest("backend.workflow.graph_node_type_invalid", "node", node.ID)
	}
}
