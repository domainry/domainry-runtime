package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
	workflowprojection "github.com/domainry/domainry-runtime/runtime/domain/workflow/projection"
)

// workflowRouteDecisionPlan is everything a route-driven decision needs that
// the durable route rows own: the step being decided, the step that follows it
// and the configuration this decision contributes to that next step.
type workflowRouteDecisionPlan struct {
	route         definitionmodel.WorkflowApprovalRouteContract
	steps         []workflowmodel.WorkflowRouteStep
	currentIndex  int
	current       workflowmodel.WorkflowRouteStep
	next          workflowmodel.WorkflowRouteStep
	hasNext       bool
	configured    *workflowmodel.WorkflowRouteStep
	nextStepGiven bool
}

func workflowRouteDecisionError(kind apperror.ErrorKind, code, fieldPath string, parameters map[string]string) error {
	params := map[string]string{"field_path": fieldPath}
	for key, value := range parameters {
		params[key] = value
	}
	return apperror.New(kind, code, nil, params)
}

// planRouteDecision evaluates every next-step rule before any vote is written.
// A rejected configuration must leave the task open, the process waiting and
// the route rows untouched, so nothing here mutates durable state.
func planRouteDecision(ctx context.Context, records *WorkflowProcessRuntime, process workflowmodel.WorkflowProcessInstance, node definitionmodel.WorkflowGraphNode, route definitionmodel.WorkflowApprovalRouteContract, task workflowmodel.WorkflowTask, req workflowmodel.WorkflowTaskDecisionRequest, decision string, principal principalmodel.Principal, now string) (workflowRouteDecisionPlan, error) {
	plan := workflowRouteDecisionPlan{route: route, currentIndex: -1, nextStepGiven: req.NextStep != nil}
	if records.dependencies.Routes == nil {
		return plan, internalError("read workflow route", fmt.Errorf("workflow route store is required"))
	}
	steps, err := records.dependencies.Routes.ListRouteSteps(ctx, process.WorkspaceID, process.ID)
	if err != nil {
		return plan, internalError("list workflow route steps", err)
	}
	plan.steps = steps
	for index, step := range steps {
		if step.NodeInstanceID != "" && step.NodeInstanceID == task.NodeInstanceID {
			plan.currentIndex, plan.current = index, step
			break
		}
	}
	if plan.currentIndex < 0 {
		return plan, badRequest("backend.workflow.route_steps_missing", "node", node.ID)
	}
	plan.next, plan.hasNext = workflowpolicy.WorkflowRouteStepAfter(steps, plan.current.StepNo)
	if decision != "approved" {
		if plan.nextStepGiven {
			return plan, workflowRouteDecisionError(apperror.KindBadRequest, "backend.workflow.next_step_not_applicable", "next_step", map[string]string{"decision": decision})
		}
		return plan, nil
	}
	if !plan.hasNext {
		if plan.nextStepGiven {
			return plan, workflowRouteDecisionError(apperror.KindBadRequest, "backend.workflow.next_step_not_configurable", "next_step", map[string]string{"step_key": plan.current.StepKey})
		}
		return plan, nil
	}
	if plan.next.Status != "configurable" {
		// A template-fixed step already carries its approvers; a configuration
		// for it would silently replace the route the initiator authored.
		if plan.nextStepGiven && strings.TrimSpace(plan.next.ConfiguredAt) == "" {
			return plan, workflowRouteDecisionError(apperror.KindBadRequest, "backend.workflow.next_step_not_configurable", "next_step", map[string]string{"step_key": plan.next.StepKey})
		}
		if plan.nextStepGiven && !workflowpolicy.WorkflowRouteStepConfigurationMatches(plan.next, req.NextStep.AssigneeUserIDs, req.NextStep.RequiredApprovals) {
			return plan, workflowRouteDecisionError(apperror.KindConflict, "backend.workflow.next_step_already_configured", "next_step", map[string]string{
				"step_key": plan.next.StepKey, "configured_by": plan.next.ConfiguredBy, "configured_at": plan.next.ConfiguredAt,
				"assignee_user_ids":  strings.Join(workflowpolicy.WorkflowRouteStepAssigneeIDs(plan.next), ","),
				"required_approvals": fmt.Sprint(plan.next.RequiredApprovals),
			})
		}
		return plan, nil
	}
	if !plan.nextStepGiven {
		return plan, workflowRouteDecisionError(apperror.KindBadRequest, "backend.workflow.next_step_required", "next_step", map[string]string{"step_key": plan.next.StepKey})
	}
	if !workflowprojection.WorkflowRouteConfigurerAllows(route, steps, plan.currentIndex+1, process, principal.UserID) {
		return plan, workflowRouteDecisionError(apperror.KindForbidden, "backend.workflow.next_step_configurer_denied", "next_step", map[string]string{"step_key": plan.next.StepKey, "configurer": route.DeferredConfigurer})
	}
	if requested := strings.TrimSpace(req.NextStep.StepKey); requested != "" && requested != plan.next.StepKey {
		return plan, workflowRouteDecisionError(apperror.KindBadRequest, "backend.workflow.next_step_not_configurable", "next_step.step_key", map[string]string{"step_key": requested, "expected": plan.next.StepKey})
	}
	electorate, err := workflowRouteEligibleAssignees(ctx, records.dependencies.Identity, process.DefinitionSnapshot, route)
	if err != nil {
		return plan, err
	}
	assignees, err := WorkflowValidateRouteStepAssignees(req.NextStep.AssigneeUserIDs, route, electorate, "next_step.assignee_user_ids")
	if err != nil {
		return plan, workflowRouteBusinessError(err)
	}
	if len(assignees) == 0 {
		return plan, workflowRouteDecisionError(apperror.KindBadRequest, "backend.workflow.route_step_count_invalid", "next_step.assignee_user_ids", map[string]string{"min": "1", "actual": "0"})
	}
	required, err := WorkflowRouteRequiredApprovals(plan.next.Mode, req.NextStep.RequiredApprovals, len(assignees), "next_step.required_approvals")
	if err != nil {
		return plan, workflowRouteBusinessError(err)
	}
	configured := plan.next
	configured.Status, configured.AssigneeSnapshot, configured.RequiredApprovals = "pending", assignees, required
	configured.ConfiguredBy, configured.ConfiguredAt, configured.ConfigureSource, configured.UpdatedAt = principal.UserID, now, "decision", now
	plan.configured = &configured
	return plan, nil
}

// workflowRouteBusinessError maps the shared route validation failures — which
// are project-facing BusinessErrors on the Action surface — onto the transport
// error the decision endpoints speak, keeping code and field_path intact.
func workflowRouteBusinessError(err error) error {
	var business *runtimeext.BusinessError
	if errors.As(err, &business) && business.Valid() {
		return apperror.New(apperror.KindBadRequest, business.Code, nil, business.ErrorParams())
	}
	return err
}

// applyRouteDecision writes the decided step, the configuration this decision
// contributed and — when the step completed — the activation of the next one,
// all inside the same commit as the vote.
func applyRouteDecision(ctx context.Context, records *WorkflowProcessRuntime, commit *transactionmodel.WorkflowDecisionCommit, process *workflowmodel.WorkflowProcessInstance, node definitionmodel.WorkflowGraphNode, plan workflowRouteDecisionPlan, outcome string, complete bool, principal principalmodel.Principal, now string) (bool, error) {
	if plan.configured != nil {
		commit.UpdateRouteSteps = append(commit.UpdateRouteSteps, *plan.configured)
		commit.Events = append(commit.Events, workflowDecisionEvent(ctx, process.ID, node.ID, "", WorkflowRouteStepConfiguredEvent, principal.UserID, plan.configured.Title, map[string]any{
			"step_key": plan.configured.StepKey, "step_no": plan.configured.StepNo, "assignee_count": len(plan.configured.AssigneeSnapshot), "required_approvals": plan.configured.RequiredApprovals,
		}, now))
	}
	if !complete {
		return false, nil
	}
	current := plan.current
	current.Status, current.UpdatedAt = outcome, now
	commit.UpdateRouteSteps = append(commit.UpdateRouteSteps, current)
	if outcome != "approved" || !plan.hasNext {
		return false, nil
	}
	next := plan.next
	if plan.configured != nil {
		next = *plan.configured
	}
	if next.Status == "configurable" {
		return false, nil
	}
	assignees, policy, err := records.processEngine.revalidateRouteStepAssignees(ctx, *process, node, plan.route, next, principal)
	if err != nil {
		return false, err
	}
	if len(assignees) == 0 {
		switch policy {
		case "skip":
			return false, nil
		case "fail":
			routeProcessConfigurationError(process, WorkflowRouteStepRevalidationFailedCode, now)
			return true, nil
		default:
			return false, badRequest("backend.workflow.approval_assignee_not_found", "node", node.ID)
		}
	}
	next.AssigneeSnapshot = assignees
	instance := records.processEngine.newNodeInstance(ctx, process.WorkspaceID, process.ID, node, "waiting", process.Variables, nil)
	instance.Iteration = next.StepNo
	commit.InsertNodes = append(commit.InsertNodes, instance)
	contract := workflowpolicy.WorkflowApprovalNodeContract(node)
	for _, task := range WorkflowRouteStepTasks(ctx, *process, node, contract, next, instance.ID, now) {
		commit.InsertTasks = append(commit.InsertTasks, task)
		commit.Events = append(commit.Events, workflowDecisionEvent(ctx, process.ID, node.ID, task.ID, "task_created", "system", task.Title, map[string]any{"assignee_user_id": task.AssigneeUserID, "assignee_role_key": task.AssigneeRoleKey, "resolver_key": task.AssigneeResolverKey, "assignee_evidence": task.AssigneeEvidence, "mode": next.Mode, "sequence": task.Sequence, "step_key": next.StepKey}, now))
	}
	next.Status, next.NodeInstanceID, next.UpdatedAt = "active", instance.ID, now
	commit.UpdateRouteSteps = append(commit.UpdateRouteSteps, next)
	commit.Events = append(commit.Events,
		workflowDecisionEvent(ctx, process.ID, node.ID, "", WorkflowRouteStepActivatedEvent, principal.UserID, next.Title, map[string]any{"step_key": next.StepKey, "step_no": next.StepNo, "assignee_count": len(assignees)}, now),
		workflowDecisionEvent(ctx, process.ID, node.ID, "", "approval_waiting", principal.UserID, node.Name, nil, now),
	)
	process.Status, process.CurrentNodeIDs, process.CompletedAt, process.UpdatedAt = "waiting", []string{node.ID}, "", now
	return true, nil
}

// routeDecisionConfigurationError marks the process as needing operator repair
// when an approved step has a next step nobody configured. The required rule
// on the decision guards against it; this is the durable fallback.
func routeDecisionConfigurationError(process *workflowmodel.WorkflowProcessInstance, step workflowmodel.WorkflowRouteStep, now string) {
	_ = step
	routeProcessConfigurationError(process, "backend.workflow.next_step_required", now)
}

func routeProcessConfigurationError(process *workflowmodel.WorkflowProcessInstance, code, now string) {
	process.Status, process.ErrorCode = "configuration_error", code
	process.CurrentNodeIDs, process.CompletedAt, process.UpdatedAt = nil, now, now
}
