package testkit

import (
	"context"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	actionpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/action"
	auditpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/audit"
	automationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/automation"
	changeplanpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/changeplan"
	deploymentpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/deployment"
	frontendcapabilitypersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/frontendcapability"
	integrationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/integration"
	lifecyclepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/lifecycle"
	metadatapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/metadata"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	reportpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/report"
	workflowpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workflow"
)

// NewRuntimeServices expands one SQL test store into focused owner stores and
// always enters production composition through the typed dependency contract.
func NewRuntimeServices(ctx context.Context, config RuntimeServicesConfig) *composition.RuntimeServices {
	dependencies := focusedPersistenceDependencies(config)
	if config.MetadataRepository != nil {
		dependencies.Metadata = config.MetadataRepository
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
	return composition.NewRuntimeServices(ctx, composition.RuntimeServicesConfig{
		Manifest: manifestmodel.ManifestSchema{
			TemplateID: config.TemplateID, Version: config.TemplateVersion, Name: config.Name,
			Objects: config.Objects, Views: config.Views, Actions: config.Actions, Workflows: config.Workflows,
			AutomationRules: config.AutomationRules, Dictionaries: config.Dictionaries, Integrations: config.Integrations,
			Reports: config.Reports, EntryPoints: config.Entrypoints, Skills: config.Skills, Agents: config.Agents,
			IdentityProfileExtensions: config.IdentityProfileExtensions,
		},
		Dependencies: dependencies,
	})
}

func focusedPersistenceDependencies(config RuntimeServicesConfig) composition.RuntimeServicesDependencies {
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
		ReportExportArtifacts: reportpersistence.NewReportExportArtifactStore(config.Store),
		ReportSnapshotSources: reportDataset,
		Audit:                 auditpersistence.NewAuditStore(config.Store),
		IntegrationConfig:     integrationpersistence.NewIntegrationConfigStore(config.Store),
		IntegrationEvents:     integrationpersistence.NewIntegrationEventStore(config.Store),
		IntegrationDelivery:   integrationpersistence.NewIntegrationDeliveryStore(config.Store),
		IntegrationWorker:     integrationpersistence.NewIntegrationWorkerStore(config.Store),
		WorkflowWorker:        workflowpersistence.NewWorkflowWorkerStore(config.Store),
		WorkflowDefinitions:   workflowpersistence.NewWorkflowDefinitionStore(config.Store),
		WorkflowProcesses:     workflowpersistence.NewWorkflowProcessStore(config.Store),
		WorkflowDecisions:     workflowpersistence.NewWorkflowDecisionStore(config.Store),
		Metadata:              metadatapersistence.NewMetadataStore(config.Store),
		AutomationWorker:      automationpersistence.NewAutomationWorkerStore(config.Store),
		AutomationExecutions:  automationpersistence.NewAutomationExecutionStore(config.Store),
		BusinessChangePlans:   changeplanpersistence.NewBusinessChangePlanStore(config.Store),
		BusinessEvidence:      changeplanpersistence.NewBusinessEvidenceStore(config.Store),
		ActionExecutions:      actionpersistence.NewActionBusinessExecutionStore(config.Store),
		ActionAssurance:       actionpersistence.NewActionAssuranceStore(config.Store),
		RuntimeStatus:         deploymentpersistence.NewRuntimeStatusStore(config.Store),
		FrontendCapabilities:  frontendcapabilitypersistence.NewFrontendCapabilityStore(config.Store),
		Lifecycle:             lifecyclepersistence.NewLifecycleStore(config.Store),
	}
}
