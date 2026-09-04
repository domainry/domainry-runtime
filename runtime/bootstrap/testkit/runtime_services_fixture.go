package testkit

import (
	"context"
	"strings"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	reportsdk "github.com/domainry/domainry-report-sdk"
	globalcapabilityseed "github.com/domainry/domainry-runtime/runtime/application/seed/globalcapability"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	auditpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/auditmodule"
	actionpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/action"
	appschemapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema"
	automationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/automation"
	deploymentpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/deployment"
	publicationhandoffpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/publicationhandoff"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	reportpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/report"
	reportnotification "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/reportnotification"
	workflowpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workflow"
	reportmodulehost "github.com/domainry/domainry-runtime/runtime/modulehost/report"
	metadatamodulefixture "github.com/domainry/domainry-runtime/testsupport/metadatamodulefixture"
	notificationsdkfixture "github.com/domainry/domainry-runtime/testsupport/notificationsdkfixture"
)

// NewRuntimeServices expands one SQL test store into focused owner stores and
// always enters production composition through the typed dependency contract.
func NewRuntimeServices(ctx context.Context, config RuntimeServicesConfig) *composition.RuntimeServices {
	installationWorkspaceID := config.InstallationWorkspaceID
	if installationWorkspaceID == "" {
		installationWorkspaceID = "workspace-primary"
	}
	if err := principalmodel.ConfigureInstallationWorkspaceID(installationWorkspaceID); err != nil {
		panic("configure Runtime testkit installation workspace: " + err.Error())
	}
	manifest := manifestmodel.ManifestSchema{
		TemplateID: config.TemplateID, Version: config.TemplateVersion, Name: config.Name,
		Objects: config.Objects, Actions: config.Actions, Workflows: config.Workflows,
		AutomationRules: config.AutomationRules, Dictionaries: config.Dictionaries, Integrations: config.Integrations,
		Reports: config.Reports, Skills: config.Skills, Agents: config.Agents,
		IdentityProfileExtensions: config.IdentityProfileExtensions,
	}
	manifest = globalcapabilityseed.WithGeneratedSchema(manifest)
	if config.Store != nil && config.ApplicationSchemaRepository == nil {
		metadatamodulefixture.EnsureBinding(ctx, config.Store)
		if len(manifest.Objects) > 0 {
			repository := appschemapersistence.NewApplicationSchemaStore(config.Store)
			if err := repository.SyncManifestProjection(ctx, principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "prepare Runtime testkit metadata projection"), manifest); err != nil {
				panic("sync Runtime testkit metadata projection: " + err.Error())
			}
		}
	}
	if config.Store != nil && config.Store.NotificationTransactions() == nil {
		if err := notificationsdkfixture.BindTransactions(config.Store); err != nil {
			panic("bind Runtime testkit Notification transactions: " + err.Error())
		}
	}
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
	dependencies.IdentityProjection = config.IdentityProjection
	dependencies.AgentPrincipals = config.IdentityPrincipals
	dependencies.DataExchange = config.DataExchange
	services := composition.NewRuntimeServices(ctx, composition.RuntimeServicesConfig{
		Manifest:     manifest,
		Dependencies: dependencies,
	})
	if reportBinding != nil {
		if err := reportmodulehost.SynchronizeDefinitions(ctx, reportBinding, manifest); err != nil {
			panic("synchronize Report test definitions: " + err.Error())
		}
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
		ReportDatasetRows:                   reportDataset,
		ReportObjectSQL:                     reportDataset,
		ReportSnapshotSources:               reportDataset,
		ReportSnapshotNotificationCommitter: reportnotification.NewReportSnapshotNotificationCommitter(config.Store, reportBinding.Snapshots()),
		ReportNotificationCompiler:          compileTestkitReportNotification,
		Audit:                               auditpersistence.NewAuditStoreFromRuntimeStore(ctx, config.Store),
		IntegrationPublication:              publicationhandoffpersistence.NewPublicationStore(config.Store),
		IntegrationPublicationWorker:        publicationhandoffpersistence.NewWorkerStore(config.Store),
		WorkflowWorker:                      workflowpersistence.NewWorkflowWorkerStore(config.Store),
		WorkflowDefinitions:                 workflowpersistence.NewWorkflowDefinitionStore(config.Store),
		WorkflowProcesses:                   workflowpersistence.NewWorkflowProcessStore(config.Store),
		WorkflowDecisions:                   workflowpersistence.NewWorkflowDecisionStore(config.Store),
		ApplicationSchema:                   appschemapersistence.NewApplicationSchemaStore(config.Store),
		AutomationWorker:                    automationpersistence.NewAutomationWorkerStore(config.Store),
		AutomationExecutions:                automationpersistence.NewAutomationExecutionStore(config.Store),
		BusinessEvidence:                    nil,
		ActionExecutions:                    actionpersistence.NewActionBusinessExecutionStore(config.Store),
		ActionAssurance:                     actionpersistence.NewActionAssuranceStore(config.Store),
		RuntimeStatus:                       deploymentpersistence.NewRuntimeStatusStore(config.Store),
	}, reportBinding
}

func compileTestkitReportNotification(intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
	if err := intent.Validate(); err != nil {
		return notificationmodel.NotificationEvent{}, err
	}
	severity := strings.TrimSpace(intent.Severity)
	if severity == "" {
		severity = "info"
	}
	return notificationmodel.NotificationEvent{
		ID: intent.ID, WorkspaceID: intent.WorkspaceID, Source: "report", SourceEventID: intent.SourceEventID,
		EventType: intent.EventType, Category: "long_task", Severity: severity,
		RecipientUserIDs: append([]string(nil), intent.RecipientUserIDs...), SubjectType: intent.SubjectType,
		SubjectID: intent.SubjectID, SubjectVersion: intent.SubjectVersion, DedupeKey: intent.DedupeKey,
		OccurredAt: intent.OccurredAt, Status: "pending", CreatedAt: intent.OccurredAt, UpdatedAt: intent.OccurredAt,
		Snapshot: notificationmodel.NotificationInboxSnapshot{Title: intent.EventType, Body: intent.EventType},
	}, nil
}
