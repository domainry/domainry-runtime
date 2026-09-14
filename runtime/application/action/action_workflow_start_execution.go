package action

import (
	"context"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

var _ runtimeext.WorkflowStartExecution = (*businessActionExecution)(nil)

// StageWorkflowStart validates one Workflow start synchronously and stages its
// durable rows on the Action Unit of Work. The Workflow only exists once the
// Action commits, so a rejected or rolled back Action leaves no process, no
// approval route and no intent behind.
func (e *businessActionExecution) StageWorkflowStart(ctx context.Context, start runtimeext.WorkflowStart) (runtimeext.WorkflowStartReceipt, error) {
	if e == nil {
		return runtimeext.WorkflowStartReceipt{}, apperror.New(apperror.KindInternal, "backend.action.workflow_start_unavailable", nil, nil)
	}
	if e.dependencies.StageWorkflowStart == nil {
		return runtimeext.WorkflowStartReceipt{}, apperror.New(apperror.KindInternal, "backend.action.workflow_start_unavailable", nil, nil)
	}
	phase := e.unitOfWork.phases.current()
	if phase != runtimeext.ExecutionPhasePrewrite && phase != runtimeext.ExecutionPhaseWriting {
		return runtimeext.WorkflowStartReceipt{}, apperror.New(apperror.KindConflict, "backend.action.execution_phase_invalid", nil, map[string]string{"phase": string(phase)})
	}
	workflowKey := strings.TrimSpace(start.WorkflowKey)
	if !e.hasWorkflowOperationGrant(workflowKey, runtimeext.WorkflowStartOperation) {
		return runtimeext.WorkflowStartReceipt{}, apperror.New(apperror.KindForbidden, runtimeext.WorkflowGrantDeniedErrorCode, nil, map[string]string{"workflow": workflowKey, "field_path": "workflow_key"})
	}
	request := workflowmodel.WorkflowRouteStartRequest{
		WorkflowKey: workflowKey, ObjectKey: strings.TrimSpace(start.ObjectKey), RecordID: strings.TrimSpace(start.RecordID),
		Variables: actionCloneMap(start.Variables), GrantedWorkflowKeys: e.grantedWorkflowKeys(),
		ActionKey: e.action.Key, ExecutionID: e.identity.ExecutionID, StageIndex: len(e.workflowStarts),
	}
	for _, step := range start.Route {
		request.Steps = append(request.Steps, workflowmodel.WorkflowRouteStartStep{
			StepKey: strings.TrimSpace(step.StepKey), Title: strings.TrimSpace(step.Title), Mode: strings.TrimSpace(step.Mode),
			RequiredApprovals: step.RequiredApprovals, AssigneeUserIDs: append([]string(nil), step.AssigneeUserIDs...), Deferred: step.Deferred,
		})
	}
	commit, err := e.dependencies.StageWorkflowStart(e.unitOfWork.executionContext(ctx), request, e.invocation.Principal)
	if err != nil {
		return runtimeext.WorkflowStartReceipt{}, err
	}
	e.workflowStarts = append(e.workflowStarts, commit)
	receipt := runtimeext.WorkflowStartReceipt{ProcessID: commit.Process.ID}
	for _, step := range commit.RouteSteps {
		receipt.StepKeys = append(receipt.StepKeys, step.StepKey)
	}
	return receipt, nil
}

func (e *businessActionExecution) hasWorkflowOperationGrant(workflowKey, requestedOperation string) bool {
	if strings.TrimSpace(workflowKey) == "" {
		return false
	}
	for _, grant := range e.workflowGrants {
		if strings.TrimSpace(grant.Key) != workflowKey {
			continue
		}
		for _, operation := range grant.Operations {
			if strings.TrimSpace(operation) == requestedOperation {
				return true
			}
		}
	}
	return false
}

func (e *businessActionExecution) grantedWorkflowKeys() []string {
	keys := make([]string, 0, len(e.workflowGrants))
	for _, grant := range e.workflowGrants {
		for _, operation := range grant.Operations {
			if strings.TrimSpace(operation) == runtimeext.WorkflowStartOperation {
				keys = append(keys, strings.TrimSpace(grant.Key))
				break
			}
		}
	}
	return keys
}

// attachWorkflowStarts binds every staged Workflow start to the last canonical
// record mutation so the process, its route and its intent share exactly the
// Action's atomic commit.
func attachWorkflowStarts(commits []transactionmodel.RecordMutationCommit, starts []transactionmodel.WorkflowStartCommit) ([]transactionmodel.RecordMutationCommit, error) {
	if len(starts) == 0 {
		return commits, nil
	}
	if len(commits) == 0 {
		return nil, apperror.New(apperror.KindBadRequest, "backend.action.workflow_start_requires_business_mutation", nil, nil)
	}
	commits[len(commits)-1].WorkflowStarts = append(commits[len(commits)-1].WorkflowStarts, starts...)
	return commits, nil
}
