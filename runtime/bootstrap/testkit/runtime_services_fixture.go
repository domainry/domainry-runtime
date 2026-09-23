package testkit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	reportsdk "github.com/domainry/domainry-report-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	projectmodel "github.com/domainry/domainry-runtime/runtime/domain/project/model"
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
	auditmodulefixture "github.com/domainry/domainry-runtime/testsupport/auditmodulefixture"
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
	modelHash := sha256.Sum256(mustJSON(config.Objects))
	model := projectmodel.RuntimeModel{
		SchemaVersion: "1", ProjectKey: config.ProjectKey, ProjectName: config.Name,
		TimeZone: "UTC", ContentHash: hex.EncodeToString(modelHash[:]),
		Objects: config.Objects, IdentityProfiles: config.IdentityProfileExtensions,
	}
	definitions := runtimeext.ProjectDefinitions{
		Workflows: config.Workflows, AutomationRules: config.AutomationRules,
		AgentSkills: config.Skills, Agents: config.Agents,
	}
	for _, report := range config.Reports {
		definitions.Reports = append(definitions.Reports, runtimeext.ReportDefinition{Report: report})
	}
	if config.Store != nil {
		auditmodulefixture.EnsureBinding(ctx, config.Store)
		metadatamodulefixture.EnsureBinding(ctx, config.Store)
	}
	if config.Store != nil && config.ApplicationSchemaRepository == nil {
		if len(model.Objects) > 0 {
			repository := appschemapersistence.NewApplicationSchemaStore(config.Store)
			if err := repository.InitializeProjectModel(ctx, principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "prepare Runtime testkit project model"), model); err != nil {
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
	dependencies.WorkflowDecisions = config.WorkflowDecisions
	dependencies.IdentityProjection = config.IdentityProjection
	dependencies.IdentityPrincipals = config.IdentityPrincipals
	dependencies.DataExchange = config.DataExchange
	services := composition.NewRuntimeServices(ctx, composition.RuntimeServicesConfig{
		ProjectModel: model, ProjectDefinitions: definitions,
		Actions: config.Actions, Integrations: config.Integrations, Dependencies: dependencies,
	})
	if reportBinding != nil {
		if err := reportmodulehost.SynchronizeDefinitions(ctx, reportBinding, model.ProjectKey, model.ContentHash, definitions.Reports); err != nil {
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

func mustJSON(value any) []byte {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return payload
}

func focusedPersistenceDependencies(ctx context.Context, config RuntimeServicesConfig) (composition.RuntimeServicesDependencies, reportsdk.Binding) {
	if config.Store == nil {
		return composition.RuntimeServicesDependencies{}, nil
	}
	records := recordpersistence.NewRecordStore(config.Store)
	reportSQL := reportpersistence.NewReportSQLStore(config.Store)
	reportBinding, err := openTestkitReportBinding(ctx, config.Store)
	if err != nil {
		panic("open Report test module: " + err.Error())
	}
	return composition.RuntimeServicesDependencies{
		Records: records, RecordExecutions: records,
		ReportObjectSQL:                     reportSQL,
		ReportSnapshotSources:               reportSQL,
		ReportSnapshotNotificationCommitter: reportnotification.NewReportSnapshotNotificationCommitter(config.Store, reportBinding.Snapshots()),
		ReportNotificationCompiler:          compileTestkitReportNotification,
		Audit:                               auditpersistence.NewAuditStoreFromRuntimeStore(config.Store),
		IntegrationPublication:              publicationhandoffpersistence.NewPublicationStore(config.Store),
		IntegrationPublicationWorker:        publicationhandoffpersistence.NewWorkerStore(config.Store),
		WorkflowWorker:                      workflowpersistence.NewWorkflowWorkerStore(config.Store),
		WorkflowDefinitions:                 workflowpersistence.NewWorkflowDefinitionStore(config.Store),
		WorkflowProcesses:                   workflowpersistence.NewWorkflowProcessStore(config.Store),
		WorkflowDecisions:                   workflowpersistence.NewWorkflowDecisionStore(config.Store),
		ApplicationSchema:                   appschemapersistence.NewApplicationSchemaStore(config.Store),
		AutomationWorker:                    automationpersistence.NewAutomationWorkerStore(config.Store),
		AutomationExecutions:                automationpersistence.NewAutomationExecutionStore(config.Store),
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
