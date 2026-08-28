package composition

import (
	"context"

	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func workflowActionInvocationResult(result actionmodel.ActionInvocationResult) workflowapplication.WorkflowBusinessActionInvocationResult {
	converted := workflowapplication.WorkflowBusinessActionInvocationResult{InvocationID: result.InvocationID, Status: result.Status, Output: result.Output, ErrorCode: result.ErrorCode, Retryable: result.Retryable, OutboxIDs: result.OutboxIDs}
	if result.Record != nil {
		converted.Record = &workflowapplication.WorkflowBusinessActionRecordResult{RecordID: result.Record.RecordID}
	}
	return converted
}

func executeCommittedWorkflowIntents(ctx context.Context, records *runtimeAssembly, intents []workflowmodel.WorkflowExecution, principal principalmodel.Principal) {
	records.Applications().Workflows.ExecuteCommittedWorkflowIntents(ctx, intents, principal)
}
