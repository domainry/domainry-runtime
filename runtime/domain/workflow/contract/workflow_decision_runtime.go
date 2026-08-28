package contract

import (
	"context"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type WorkflowDecisionRuntime interface {
	DecideTerminalTask(context.Context, string, workflowmodel.WorkflowTaskDecisionRequest, principalmodel.Principal) (workflowmodel.WorkflowProcessInstance, bool, error)
}
