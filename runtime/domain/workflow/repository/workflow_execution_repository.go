package repository

import (
	"context"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

// WorkflowExecutionRepository owns workflow execution persistence used by
// generated global capability materialization.
type WorkflowExecutionRepository interface {
	ListExecutions(context.Context, string, int) ([]workflowmodel.WorkflowExecution, error)
	InsertExecution(context.Context, string, workflowmodel.WorkflowExecution) error
}
