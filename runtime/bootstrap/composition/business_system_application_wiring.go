package composition

import (
	"context"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	automationapplication "github.com/domainry/domainry-runtime/runtime/application/automation"
	businesssystemapplication "github.com/domainry/domainry-runtime/runtime/application/businesssystem"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	publicationhandoff "github.com/domainry/domainry-runtime/runtime/application/publicationhandoff"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	changeplanrepository "github.com/domainry/domainry-runtime/runtime/domain/changeplan/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func assembleBusinessSystemApplication(
	schema *appschemaapplication.ApplicationSchemaQueryApplicationService,
	metadata metadatasdk.Definitions,
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
		Definitions: metadata, Evidence: evidence,
		Runtime: businesssystemapplication.BusinessSystemRuntimeProjectionDependencies{
			WorkflowProcesses: workflows.WorkflowProcesses, AutomationRules: automations.AutomationRules,
			AutomationExecutions: automations.AutomationExecutions,
			ConnectorCatalog: func(ctx context.Context, principal principalmodel.Principal) ([]connectormodel.ConnectorSchema, error) {
				return schema.ForPrincipal(ctx, principal).Integrations.Connectors, nil
			},
			IntegrationConnections: func(context.Context, principalmodel.Principal) ([]integrationsdk.Connection, error) {
				return []integrationsdk.Connection{}, nil
			},
			PublicationHandoff: publications.ListPublicationMessages,
			SchedulerDefinitions: func(ctx context.Context, principal principalmodel.Principal) ([]recordmodel.Record, error) {
				definitions, err := scheduler.PublishedDefinitions(ctx, principal)
				return schedulerPublishedDefinitionRecords(definitions), err
			},
			SchemaForPrincipal: schema.ForPrincipal, ListRecords: records.ListRecords, IdempotencyStatus: runtimeStatus.IdempotencyOperationalStatus,
		},
	})
}
