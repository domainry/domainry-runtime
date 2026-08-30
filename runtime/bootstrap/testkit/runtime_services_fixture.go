package testkit

import (
	"context"
	lifecyclepersistence "github.com/domainry/domainry-lifecycle/persistence"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	auditpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/auditmodule"
	actionpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/action"
	appschemapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema"
	automationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/automation"
	deploymentpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/deployment"
	frontendcapabilitypersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/frontendcapability"
	integrationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/integration"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	reportpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/report"
	workflowpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workflow"
	lifecyclemodule "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/lifecyclemodule"
)

// NewRuntimeServices expands one SQL test store into focused owner stores and
// always enters production composition through the typed dependency contract.
func NewRuntimeServices(ctx context.Context, config RuntimeServicesConfig) *composition.RuntimeServices {
	dependencies := focusedPersistenceDependencies(ctx, config)
	if config.ApplicationSchemaRepository != nil {
		dependencies.ApplicationSchema = config.ApplicationSchemaRepository
	}
	if config.WorkflowWorker != nil {
		dependencies.WorkflowWorker = config.WorkflowWorker
	}
	if config.WorkflowProcesses != nil {
		dependencies.WorkflowProcesses = config.WorkflowProcesses
	}
	if config.WorkflowDefinitions != nil {
		dependencies.WorkflowDefinitions = config.WorkflowDefinitions
	}
	if config.BusinessEvidence != nil {
		dependencies.BusinessEvidence = config.BusinessEvidence
	}
	dependencies.WorkflowDecisions = config.WorkflowDecisions
	dependencies.IdentityDirectory = config.IdentityDirectory
	dependencies.AgentPrincipals = config.IdentityPrincipals
	dependencies.ConnectorProviders = config.ConnectorProviders
	dependencies.DataExchange = config.DataExchange
	return composition.NewRuntimeServices(ctx, composition.RuntimeServicesConfig{
		Manifest: manifestmodel.ManifestSchema{
			TemplateID: config.TemplateID, Version: config.TemplateVersion, Name: config.Name,
			Objects: config.Objects, Actions: config.Actions, Workflows: config.Workflows,
			AutomationRules: config.AutomationRules, Dictionaries: config.Dictionaries, Integrations: config.Integrations,
			Reports: config.Reports, Skills: config.Skills, Agents: config.Agents,
			IdentityProfileExtensions: config.IdentityProfileExtensions,
		},
		Dependencies: dependencies,
	})
}

func focusedPersistenceDependencies(ctx context.Context, config RuntimeServicesConfig) composition.RuntimeServicesDependencies {
	if config.Store == nil {
		return composition.RuntimeServicesDependencies{}
	}
	records := recordpersistence.NewRecordStore(config.Store)
	reportDataset := reportpersistence.NewReportDatasetStore(config.Store)
	return composition.RuntimeServicesDependencies{
		Records: records, RecordExecutions: records,
		ReportDatasetRows:     reportDataset,
		ReportObjectSQL:       reportDataset,
		ReportSnapshots:       reportpersistence.NewReportSnapshotStore(config.Store),
		ReportSnapshotSources: reportDataset,
		Audit:                 auditpersistence.NewAuditStoreFromRuntimeStore(ctx, config.Store),
		IntegrationConfig:     integrationpersistence.NewIntegrationConfigStore(config.Store),
		IntegrationEvents:     integrationpersistence.NewIntegrationEventStore(config.Store),
		IntegrationDelivery:   integrationpersistence.NewIntegrationDeliveryStore(config.Store),
		IntegrationWorker:     integrationpersistence.NewIntegrationWorkerStore(config.Store),
		WorkflowWorker:        workflowpersistence.NewWorkflowWorkerStore(config.Store),
		WorkflowDefinitions:   workflowpersistence.NewWorkflowDefinitionStore(config.Store),
		WorkflowProcesses:     workflowpersistence.NewWorkflowProcessStore(config.Store),
		WorkflowDecisions:     workflowpersistence.NewWorkflowDecisionStore(config.Store),
		ApplicationSchema:     appschemapersistence.NewApplicationSchemaStore(config.Store),
		AutomationWorker:      automationpersistence.NewAutomationWorkerStore(config.Store),
		AutomationExecutions:  automationpersistence.NewAutomationExecutionStore(config.Store),
		BusinessEvidence:      nil,
		ActionExecutions:      actionpersistence.NewActionBusinessExecutionStore(config.Store),
		ActionAssurance:       actionpersistence.NewActionAssuranceStore(config.Store),
		RuntimeStatus:         deploymentpersistence.NewRuntimeStatusStore(config.Store),
		FrontendCapabilities:  frontendcapabilitypersistence.NewFrontendCapabilityStore(config.Store),
		Lifecycle:             lifecyclepersistence.NewLifecycleStore(lifecyclemodule.NewHost(config.Store)),
	}
}
