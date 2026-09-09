package workflow

import (
	"context"
	"strings"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowprojection "github.com/domainry/domainry-runtime/runtime/domain/workflow/projection"
)

// ParticipantWorkflowRoute reads the per-instance approval route of one
// process. Route membership is its own visibility rule: the initiator and
// every named approver may read it, and everybody else is refused even when
// they can otherwise see Workflow data.
func (s *WorkflowApplicationService) ParticipantWorkflowRoute(ctx context.Context, processID string, principal principalmodel.Principal) (workflowprojection.WorkflowRouteView, error) {
	if err := workflowAuthorizeQuery(principal); err != nil {
		return workflowprojection.WorkflowRouteView{}, err
	}
	process, steps, tasks, err := s.workflowRouteSnapshot(ctx, processID, principal)
	if err != nil {
		return workflowprojection.WorkflowRouteView{}, err
	}
	if !workflowprojection.WorkflowRouteVisibleTo(process, steps, principal.UserID) {
		return workflowprojection.WorkflowRouteView{}, forbidden("backend.workflow.process_access_denied")
	}
	return workflowprojection.WorkflowRouteForPrincipal(process, steps, tasks, principal.UserID), nil
}

func (s *WorkflowApplicationService) workflowRouteSnapshot(ctx context.Context, processID string, principal principalmodel.Principal) (workflowmodel.WorkflowProcessInstance, []workflowmodel.WorkflowRouteStep, []workflowmodel.WorkflowTask, error) {
	process, ok, err := s.processRepo.GetProcess(ctx, principal.WorkspaceID, strings.TrimSpace(processID))
	if err != nil {
		return workflowmodel.WorkflowProcessInstance{}, nil, nil, internalError("get workflow process", err)
	}
	if !ok {
		return workflowmodel.WorkflowProcessInstance{}, nil, nil, notFound("backend.workflow.process_not_found")
	}
	if s.routes == nil {
		return process, nil, nil, nil
	}
	steps, err := s.routes.ListRouteSteps(ctx, principal.WorkspaceID, process.ID)
	if err != nil {
		return workflowmodel.WorkflowProcessInstance{}, nil, nil, internalError("list workflow route steps", err)
	}
	if len(steps) == 0 {
		return process, nil, nil, nil
	}
	tasks, err := s.processRepo.ListTasks(ctx, principal.WorkspaceID, process.ID, "", "", 500)
	if err != nil {
		return workflowmodel.WorkflowProcessInstance{}, nil, nil, internalError("list workflow process tasks", err)
	}
	return process, steps, tasks, nil
}

// workflowProcessRouteView is the route embedded in a process detail. A
// process without a route contributes nothing instead of failing the read.
func (s *WorkflowApplicationService) workflowProcessRouteView(ctx context.Context, process workflowmodel.WorkflowProcessInstance, tasks []workflowmodel.WorkflowTask, principal principalmodel.Principal) *workflowprojection.WorkflowRouteView {
	if s.routes == nil {
		return nil
	}
	steps, err := s.routes.ListRouteSteps(ctx, principal.WorkspaceID, process.ID)
	if err != nil || len(steps) == 0 || !workflowprojection.WorkflowRouteVisibleTo(process, steps, principal.UserID) {
		return nil
	}
	view := workflowprojection.WorkflowRouteForPrincipal(process, steps, tasks, principal.UserID)
	return &view
}
