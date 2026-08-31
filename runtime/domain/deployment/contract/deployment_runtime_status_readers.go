package contract

import (
	"context"
	integrationsdk "github.com/domainry/domainry-integration-sdk"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type DeploymentRecordReader interface {
	ListRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error)
}

type DeploymentWorkflowExecutionReader interface {
	ListExecutions(context.Context, string, int) ([]workflowmodel.WorkflowExecution, error)
}

type DeploymentDeliveryReader interface {
	ListInvocations(context.Context, string, string, string, string, string, int) ([]integrationsdk.Invocation, error)
}
