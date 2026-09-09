package policy

import (
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

// WorkflowApprovalRouteSourceInstance is the only supported route authority.
// The route rows are staged by the initiating Action and are the durable
// electorate of the approval node for that one process instance.
const WorkflowApprovalRouteSourceInstance = "instance"

// WorkflowApprovalRoute returns the effective route contract of an approval
// node with every optional field defaulted, or false when the node has none.
func WorkflowApprovalRoute(node definitionmodel.WorkflowGraphNode) (definitionmodel.WorkflowApprovalRouteContract, bool) {
	contract := WorkflowApprovalNodeContract(node)
	if contract.Route == nil {
		return definitionmodel.WorkflowApprovalRouteContract{}, false
	}
	return WorkflowApprovalRouteDefaults(*contract.Route), true
}

// WorkflowApprovalRouteDefaults applies the documented defaults so callers
// never branch on an empty optional value.
func WorkflowApprovalRouteDefaults(route definitionmodel.WorkflowApprovalRouteContract) definitionmodel.WorkflowApprovalRouteContract {
	route.Source = valueOrDefault(strings.TrimSpace(route.Source), WorkflowApprovalRouteSourceInstance)
	route.DeferredSteps = valueOrDefault(strings.TrimSpace(route.DeferredSteps), "deny")
	route.DeferredConfigurer = valueOrDefault(strings.TrimSpace(route.DeferredConfigurer), "previous_step_approver")
	route.RevalidateOnActivation = valueOrDefault(strings.TrimSpace(route.RevalidateOnActivation), "fail")
	if route.MinSteps < 1 {
		route.MinSteps = 1
	}
	if route.MaxSteps < 1 {
		route.MaxSteps = route.MinSteps
	}
	if route.MaxAssigneesPerStep < 1 {
		route.MaxAssigneesPerStep = 1
	}
	eligible := make([]string, 0, len(route.EligibleRoles))
	for _, raw := range route.EligibleRoles {
		if value := strings.TrimSpace(raw); value != "" {
			eligible = append(eligible, value)
		}
	}
	route.EligibleRoles = eligible
	return route
}

// WorkflowValidateApprovalRoute rejects a definition whose route ranges cannot
// describe any acceptable instance route.
func WorkflowValidateApprovalRoute(nodeID string, route *definitionmodel.WorkflowApprovalRouteContract) error {
	if route == nil {
		return nil
	}
	if strings.TrimSpace(route.Source) != WorkflowApprovalRouteSourceInstance {
		return badRequest("backend.workflow.route_contract_invalid", "node", nodeID, "field", "source")
	}
	if route.MinSteps < 1 || route.MaxSteps < route.MinSteps {
		return badRequest("backend.workflow.route_contract_invalid", "node", nodeID, "field", "steps")
	}
	if route.MaxAssigneesPerStep < 1 {
		return badRequest("backend.workflow.route_contract_invalid", "node", nodeID, "field", "max_assignees_per_step")
	}
	for _, value := range route.EligibleRoles {
		if strings.TrimSpace(value) == "" {
			return badRequest("backend.workflow.route_contract_invalid", "node", nodeID, "field", "eligible_roles")
		}
	}
	switch valueOrDefault(strings.TrimSpace(route.DeferredSteps), "deny") {
	case "allow", "deny":
	default:
		return badRequest("backend.workflow.route_contract_invalid", "node", nodeID, "field", "deferred_steps")
	}
	switch valueOrDefault(strings.TrimSpace(route.DeferredConfigurer), "previous_step_approver") {
	case "previous_step_approver", "initiator":
	default:
		return badRequest("backend.workflow.route_contract_invalid", "node", nodeID, "field", "deferred_configurer")
	}
	switch valueOrDefault(strings.TrimSpace(route.RevalidateOnActivation), "fail") {
	case "fail", "skip_invalid", "admin":
	default:
		return badRequest("backend.workflow.route_contract_invalid", "node", nodeID, "field", "revalidate_on_activation")
	}
	return nil
}

// WorkflowRouteStepMode normalizes a route step mode; an unset mode approves
// as soon as one assignee approves, matching the node default.
func WorkflowRouteStepMode(mode string) string {
	return valueOrDefault(strings.TrimSpace(mode), "any")
}

// WorkflowRouteStepThreshold is the number of approvals that completes one
// route step. It is derived from the durable step row, never from the node
// contract, because every step carries its own mode.
func WorkflowRouteStepThreshold(step workflowmodel.WorkflowRouteStep) int {
	assignees := len(step.AssigneeSnapshot)
	switch WorkflowRouteStepMode(step.Mode) {
	case "all":
		return assignees
	case "quorum":
		if step.RequiredApprovals > 0 && step.RequiredApprovals <= assignees {
			return step.RequiredApprovals
		}
		return assignees
	default:
		return 1
	}
}

// WorkflowRouteStepAssigneeIDs projects the durable assignee snapshot.
func WorkflowRouteStepAssigneeIDs(step workflowmodel.WorkflowRouteStep) []string {
	out := make([]string, 0, len(step.AssigneeSnapshot))
	for _, assignee := range step.AssigneeSnapshot {
		if userID := strings.TrimSpace(assignee.UserID); userID != "" {
			out = append(out, userID)
		}
	}
	return out
}

// WorkflowRouteStepAfter returns the step that follows stepNo in step order.
func WorkflowRouteStepAfter(steps []workflowmodel.WorkflowRouteStep, stepNo int) (workflowmodel.WorkflowRouteStep, bool) {
	best := workflowmodel.WorkflowRouteStep{}
	found := false
	for _, step := range steps {
		if step.StepNo <= stepNo {
			continue
		}
		if !found || step.StepNo < best.StepNo {
			best, found = step, true
		}
	}
	return best, found
}

// WorkflowProcessStatuses is the complete Runtime-owned process status set.
// starting is the durable status of a process whose route and trigger were
// staged inside the initiating Action transaction but whose first node has not
// been activated by the committed intent yet.
func WorkflowProcessStatuses() []string {
	return []string{"starting", "running", "waiting", "completed", "rejected", "cancelled", "failed", "configuration_error", "resolved"}
}

// WorkflowProcessStatusActive reports whether a process still occupies runtime
// state that a Change Plan or an operator view must account for.
func WorkflowProcessStatusActive(status string) bool {
	switch strings.TrimSpace(status) {
	case "starting", "running", "waiting", "configuration_error":
		return true
	default:
		return false
	}
}
