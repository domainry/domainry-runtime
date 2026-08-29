package composition

import (
	automationapplication "github.com/domainry/domainry-runtime/runtime/application/automation"
	businesssystemapplication "github.com/domainry/domainry-runtime/runtime/application/businesssystem"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	metadataapplication "github.com/domainry/domainry-runtime/runtime/application/metadata"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	changeplanrepository "github.com/domainry/domainry-runtime/runtime/domain/changeplan/repository"
)

func assembleBusinessSystemApplication(
	schema *metadataapplication.MetadataSchemaApplicationService,
	metadata *metadataapplication.ApplicationSchemaService,
	workflows *workflowapplication.WorkflowApplicationService,
	automations *automationapplication.AutomationApplicationService,
	integrations *integrationapplication.IntegrationApplicationService,
	records *recordapplication.RecordApplicationService,
	scheduler *schedulerapplication.SchedulerApplicationService,
	frontend *deploymentapplication.DeploymentFrontendCapabilityApplicationService,
	runtimeStatus *deploymentapplication.DeploymentRuntimeStatusApplicationService,
	evidence changeplanrepository.ChangePlanEvidenceRepository,
) *businesssystemapplication.BusinessSystemApplicationService {
	return businesssystemapplication.NewBusinessSystemApplicationService(businesssystemapplication.BusinessSystemApplicationDependencies{
		FeaturePermissions: schema.FeaturePermissions, SchemaForPrincipal: schema.ForPrincipal,
		MetadataDefinitions: metadata.ListMetadataDefinitions, FrontendSnapshot: frontend.Snapshot, Evidence: evidence,
		Runtime: businesssystemapplication.BusinessSystemRuntimeProjectionDependencies{
			WorkflowProcesses: workflows.WorkflowProcesses, AutomationRules: automations.AutomationRules,
			AutomationExecutions: automations.AutomationExecutions, ConnectorCatalog: integrations.IntegrationConnectorCatalog,
			IntegrationConnections: integrations.ListIntegrationConnections, IntegrationOutbox: integrations.ListIntegrationOutboxMessages,
			SchedulerDefinitions: scheduler.PublishedDefinitions,
			SchemaForPrincipal:   schema.ForPrincipal, SchemaObjectMap: schema.ObjectMap, ListRecords: records.ListRecords, IdempotencyStatus: runtimeStatus.IdempotencyOperationalStatus,
		},
	})
}
