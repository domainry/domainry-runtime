package contract

import (
	"context"
	"errors"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

// WorkflowApprovalTaskReader reads the complete electorate for one node
// instance, independently of the participant task-list pagination limit.
type WorkflowApprovalTaskReader interface {
	ListApprovalTasks(context.Context, string, string, string) ([]workflowmodel.WorkflowTask, error)
}

// ErrWorkflowDecisionSnapshotChanged asks the application to recompute an
// approval decision against the latest durable process and task snapshot.
var ErrWorkflowDecisionSnapshotChanged = errors.New("workflow decision snapshot changed")

// WorkflowRouteStore owns the durable per-instance approval route. The rows
// are the electorate authority of a route-driven approval node, so a step is
// only ever configured through a compare-and-set on its current status.
type WorkflowRouteStore interface {
	InsertRouteSteps(context.Context, string, []workflowmodel.WorkflowRouteStep) error
	ListRouteSteps(context.Context, string, string) ([]workflowmodel.WorkflowRouteStep, error)
	UpdateRouteStepCAS(context.Context, string, workflowmodel.WorkflowRouteStep, string) (bool, error)
}
