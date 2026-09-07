package composition

import (
	"context"
	"time"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func newScheduledWorkflowRuntimeAdapter(s *runtimeAssembly) scheduledWorkflowRuntimeAdapter {
	return scheduledWorkflowRuntimeAdapter{
		processExecutions: func(ctx context.Context, limit int, principal principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
			return s.workflowApplicationService.ProcessDueWorkflowExecutions(ctx, limit, principal)
		},
		processTargetedExecutions: func(ctx context.Context, targetKey string, limit int, principal principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
			return s.workflowApplicationService.ProcessDueWorkflowExecutionsForTarget(ctx, targetKey, limit, principal)
		},
		processTargetedExecutionsForWindow: func(ctx context.Context, targetKey string, scheduledFor time.Time, limit int, principal principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
			return s.workflowApplicationService.ProcessDueWorkflowExecutionsForScheduledWindow(ctx, targetKey, scheduledFor, limit, principal)
		},
		processTargetedWindowWithKey: func(ctx context.Context, targetKey string, scheduledFor time.Time, limit int, principal principalmodel.Principal, idempotencyKey string) (workflowmodel.WorkflowProcessResult, error) {
			return s.workflowApplicationService.ProcessDueWorkflowExecutionsForScheduledWindowWithKey(ctx, targetKey, scheduledFor, limit, principal, idempotencyKey)
		},
	}
}
