package testkit

import (
	"context"

	reportsdk "github.com/domainry/domainry-report-sdk"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	auditpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/auditmodule"
	actionpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/action"
	appschemapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema"
	automationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/automation"
	deploymentpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/deployment"
	publicationhandoffpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/publicationhandoff"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	reportpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/report"
	workflowpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workflow"
)

// NewRuntimeServices expands one SQL test store into focused owner stores and
// always enters production composition through the typed dependency contract.
func NewRuntimeServices(ctx context.Context, config RuntimeServicesConfig) *composition.RuntimeServices {
	dependencies, reportBinding := focusedPersistenceDependencies(ctx, config)
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
	dependencies.DataExchange = config.DataExchange
	services := composition.NewRuntimeServices(ctx, composition.RuntimeServicesConfig{
		Manifest: manifestmodel.ManifestSchema{
			TemplateID: config.TemplateID, Version: config.TemplateVersion, Name: config.Name,
			Objects: config.Objects, Actions: config.Actions, Workflows: config.Workflows,
			AutomationRules: config.AutomationRules, Dictionaries: config.Dictionaries, Integrations: config.Integrations,
			Reports: config.Reports, Skills: config.Skills, Agents: config.Agents,
			IdentityProfileExtensions: config.IdentityProfileExtensions,
		},
		Dependencies: dependencies,
	})
	if reportBinding != nil {
		binder, ok := reportBinding.(reportsdk.ApplicationHostBinder)
		if !ok {
			panic("Report test module does not accept application host capabilities")
		}
		if err := binder.BindApplicationHost(testkitReportApplicationHost{testkitReportHost: testkitReportHost{store: config.Store}, ports: services.ReportModuleApplicationPorts(), cursorKey: []byte("runtime-testkit-report-cursor")}); err != nil {
			panic("bind Report test application host: " + err.Error())
		}
		if err := services.BindReportApplication(reportBinding); err != nil {
			panic("bind Report test application: " + err.Error())
		}
	}
	return services
}

func focusedPersistenceDependencies(ctx context.Context, config RuntimeServicesConfig) (composition.RuntimeServicesDependencies, reportsdk.Binding) {
	if config.Store == nil {
		return composition.RuntimeServicesDependencies{}, nil
	}
	records := recordpersistence.NewRecordStore(config.Store)
	reportDataset := reportpersistence.NewReportDatasetStore(config.Store)
	reportBinding, err := openTestkitReportBinding(ctx, config.Store)
	if err != nil {
		panic("open Report test module: " + err.Error())
	}
	return composition.RuntimeServicesDependencies{
		Records: records, RecordExecutions: records,
		ReportDatasetRows:            reportDataset,
		ReportObjectSQL:              reportDataset,
		ReportSnapshotSources:        reportDataset,
		Audit:                        auditpersistence.NewAuditStoreFromRuntimeStore(ctx, config.Store),
		IntegrationPublication:       publicationhandoffpersistence.NewPublicationStore(config.Store),
		IntegrationPublicationWorker: publicationhandoffpersistence.NewWorkerStore(config.Store),
		WorkflowWorker:               workflowpersistence.NewWorkflowWorkerStore(config.Store),
		WorkflowDefinitions:          workflowpersistence.NewWorkflowDefinitionStore(config.Store),
		WorkflowProcesses:            workflowpersistence.NewWorkflowProcessStore(config.Store),
		WorkflowDecisions:            workflowpersistence.NewWorkflowDecisionStore(config.Store),
		ApplicationSchema:            appschemapersistence.NewApplicationSchemaStore(config.Store),
		AutomationWorker:             automationpersistence.NewAutomationWorkerStore(config.Store),
		AutomationExecutions:         automationpersistence.NewAutomationExecutionStore(config.Store),
		BusinessEvidence:             nil,
		ActionExecutions:             actionpersistence.NewActionBusinessExecutionStore(config.Store),
		ActionAssurance:              actionpersistence.NewActionAssuranceStore(config.Store),
		RuntimeStatus:                deploymentpersistence.NewRuntimeStatusStore(config.Store),
	}, reportBinding
}
