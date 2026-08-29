package integration

import (
	"context"

	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type IntegrationEventRecordApplication interface {
	CreateRecord(context.Context, string, map[string]any, principalmodel.Principal) (recordmodel.Record, error)
	ListRecords(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error)
}

type IntegrationAgentRecordApplication interface {
	CreateRecord(context.Context, string, map[string]any, principalmodel.Principal) (recordmodel.Record, error)
	UpdateRecord(context.Context, string, string, map[string]any, principalmodel.Principal) (recordmodel.Record, error)
	DeleteRecord(context.Context, string, string, principalmodel.Principal) error
}

type IntegrationWorkflowApplication interface {
	RunIntegrationWorkflow(context.Context, string, map[string]any, principalmodel.Principal) (workflowmodel.WorkflowRunResult, error)
}

type IntegrationAutomationApplication interface {
	ValidateIntegrationOutput(automationmodel.AutomationInstructionSchema, map[string]any) error
	ExecuteOutboxMessage(context.Context, integrationmodel.IntegrationOutboxMessage) error
}

type IntegrationSchemaProvider func(context.Context, principalmodel.Principal) metadatamodel.ApplicationSchemaSnapshot
type IntegrationSchemaObjectMapProvider func(context.Context) map[string]definitionmodel.ObjectSchema
type IntegrationActionInvoker func(context.Context, actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error)
type IntegrationEventWorkflowExecutor func(context.Context, string, integrationmodel.IntegrationEntrypointWorkflowRequest, principalmodel.Principal) (IntegrationWorkflowRunResult, error)
type IntegrationEventActionExecutor func(context.Context, string, string, string, integrationmodel.IntegrationEntrypointActionRequest, principalmodel.Principal) (IntegrationActionExecutionResult, error)
type IntegrationEventIdentityResolver func(context.Context, integrationmodel.IntegrationExternalIdentityResolveRequest, principalmodel.Principal) (integrationmodel.IntegrationExternalIdentityResolveResult, principalmodel.Principal, error)

func integrationMapFromAny(value any) map[string]any {
	if result, ok := value.(map[string]any); ok && result != nil {
		return result
	}
	return map[string]any{}
}
