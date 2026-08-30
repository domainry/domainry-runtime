package composition

import (
	"context"
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	automationapplication "github.com/domainry/domainry-runtime/runtime/application/automation"
	businesssystemapplication "github.com/domainry/domainry-runtime/runtime/application/businesssystem"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	publicationhandoff "github.com/domainry/domainry-runtime/runtime/application/publicationhandoff"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	changeplanrepository "github.com/domainry/domainry-runtime/runtime/domain/changeplan/repository"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func assembleBusinessSystemApplication(
	schema *appschemaapplication.ApplicationSchemaQueryApplicationService,
	metadata *appschemaapplication.ApplicationSchemaApplicationService,
	workflows *workflowapplication.WorkflowApplicationService,
	automations *automationapplication.AutomationApplicationService,
	publications *publicationhandoff.PublicationHandoffApplicationService,
	records *recordapplication.RecordApplicationService,
	scheduler *schedulerapplication.SchedulerApplicationService,
	runtimeStatus *deploymentapplication.DeploymentRuntimeStatusApplicationService,
	evidence changeplanrepository.ChangePlanEvidenceRepository,
) *businesssystemapplication.BusinessSystemApplicationService {
	return businesssystemapplication.NewBusinessSystemApplicationService(businesssystemapplication.BusinessSystemApplicationDependencies{
		FeaturePermissions: schema.FeaturePermissions, SchemaForPrincipal: schema.ForPrincipal,
		ApplicationDefinitions: metadata.ListApplicationDefinitions, Evidence: evidence,
		Runtime: businesssystemapplication.BusinessSystemRuntimeProjectionDependencies{
			WorkflowProcesses: workflows.WorkflowProcesses, AutomationRules: automations.AutomationRules,
			AutomationExecutions: automations.AutomationExecutions,
			ConnectorCatalog: func(ctx context.Context, principal principalmodel.Principal) ([]integrationmodel.ConnectorSchema, error) {
				return schema.ForPrincipal(ctx, principal).Integrations.Connectors, nil
			},
			IntegrationConnections: func(context.Context, principalmodel.Principal) ([]integrationmodel.IntegrationConnection, error) {
				return []integrationmodel.IntegrationConnection{}, nil
			},
			IntegrationOutbox:    publications.ListIntegrationOutboxMessages,
			SchedulerDefinitions: scheduler.PublishedDefinitions,
			SchemaForPrincipal:   schema.ForPrincipal, SchemaObjectMap: schema.ObjectMap, ListRecords: records.ListRecords, IdempotencyStatus: runtimeStatus.IdempotencyOperationalStatus,
		},
	})
}
