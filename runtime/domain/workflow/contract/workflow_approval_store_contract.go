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
