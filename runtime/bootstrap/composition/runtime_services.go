package composition

import (
	"context"
	"fmt"
	publicationrepository "github.com/domainry/domainry-runtime/runtime/domain/publication/repository"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	organizationunit "github.com/domainry/domainry-identity-sdk/organizationunit"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	automationapplication "github.com/domainry/domainry-runtime/runtime/application/automation"
	publicationhandoff "github.com/domainry/domainry-runtime/runtime/application/publicationhandoff"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	uploadapplication "github.com/domainry/domainry-runtime/runtime/application/upload"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	reportmodulehost "github.com/domainry/domainry-report-sdk/modulehost"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	auditrepository "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	actioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	automationcontract "github.com/domainry/domainry-runtime/runtime/domain/automation/contract"
	automationrepository "github.com/domainry/domainry-runtime/runtime/domain/automation/repository"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	deploymentrepository "github.com/domainry/domainry-runtime/runtime/domain/deployment/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	projectmodel "github.com/domainry/domainry-runtime/runtime/domain/project/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	workspaceaggregatecontract "github.com/domainry/domainry-runtime/runtime/domain/workspaceaggregate/contract"
)

// RuntimeServices is the immutable facade exported by the composition root.
// Construction-only repositories, indexes, and owner services remain on the
// private runtimeAssembly and are captured only by their explicit ports.
type RuntimeServices struct {
	applications               RuntimeApplications
	schema                     runtimeSchemaReader
	reportModule               ReportModuleApplicationPorts
	schedulerDefinitionSource  SchedulerDefinitionSource
	notificationEventPublisher func(context.Context, notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, bool, error)
	agentTaskAttachmentFiles   *uploadapplication.AgentTaskAttachmentFileService
	assembly                   *runtimeAssembly
}

type ReportModuleApplicationPorts struct {
	Subjects       reportmodulehost.SubjectResolver
	ObjectSQL      reportmodulehost.ObjectSQLExecutor
	SourceVersions reportmodulehost.SourceVersionReader
	Audit          reportmodulehost.ExecutionAudit
	Authorization  reportmodulehost.ExportAuthorization
	Terminals      reportmodulehost.SnapshotTerminalCommitter
	Exports        reportmodulehost.ExportGateway
	Tables         reportmodulehost.AnalysisTableSource
}

func (s *RuntimeServices) ReportModuleApplicationPorts() ReportModuleApplicationPorts {
	if s == nil {
		return ReportModuleApplicationPorts{}
	}
	return s.reportModule
}

type runtimeSchemaReader interface {
	Schema() appschemamodel.ApplicationSchemaSnapshot
	SchemaForPrincipal(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot
}

type NotificationRenderer interface {
	Render(context.Context, NotificationRenderRequest) (notificationmodel.RenderedNotification, error)
}

// NotificationRenderRequest is a Runtime source-owner input projected into
// the Notification SDK renderer. It carries no Notification domain behavior.
type NotificationRenderRequest struct {
	TemplateKey string
	Locale      string
	Recipients  []string
	Variables   map[string]any
	Metadata    map[string]any
}

// RuntimeServicesDependencies declares every explicit repository and external
// port required by the Runtime composition root.
type RuntimeServicesDependencies struct {
	ProductBrandName                    string
	ActionRuntimeRevision               string
	ActionProjectRevision               string
	ActionMetadataRevision              string
	Records                             recordrepository.RecordRepository
	ReportObjectSQL                     reportcontract.ReportObjectSQLExecutor
	ReportSnapshotSources               reportcontract.ReportSnapshotSourceVersionReader
	ReportAnalysisTables                reportmodulehost.AnalysisTableSource
	ReportExportPrepareReceipts         reportcontract.ReportExportPrepareReceiptStore
	RecordExecutions                    recordcontract.RecordMutationExecutionStore
	DataExchange                        dataexchange.Binding
	DataExchangeProviders               *recordapplication.DataExchangeProviders
	Audit                               auditrepository.AuditRepository
	AuditApplication                    *auditapplication.AuditApplicationService
	AuditExportTokenKey                 []byte
	IntegrationPublication              publicationrepository.Repository
	IntegrationPublicationWorker        publicationrepository.WorkerRepository
	IntegrationOwnerDelivery            integrationsdk.Delivery
	IntegrationOwnerCatalog             integrationsdk.Catalog
	IntegrationOwnerManagement          integrationsdk.Management
	IntegrationOwnerOperations          integrationsdk.Operations
	WorkerWakeups                       *workerplatform.WakeupBroker
	WorkflowWorker                      workflowcontract.WorkflowWorkerStore
	WorkflowDefinitions                 workflowcontract.WorkflowDefinitionStore
	WorkflowProcesses                   workflowcontract.WorkflowProcessStore
	WorkflowDecisions                   workflowcontract.WorkflowDecisionStore
	WorkflowRoutes                      workflowcontract.WorkflowRouteStore
	WorkflowNotificationCompiler        func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	WorkflowTaskNotificationCommitter   workflowapplication.WorkflowTaskNotificationCommitter
	RecordNotificationCompiler          func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	ReportNotificationCompiler          func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	ReportSnapshotNotificationCommitter ReportSnapshotNotificationCommitter
	AutomationNotificationCompiler      func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	AutomationNotificationCommitter     automationapplication.AutomationExecutionNotificationCommitter
	NotificationIntentPublisher         func(context.Context, notificationmodel.NotificationIntent) error
	NotificationEventPublisher          func(context.Context, notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, bool, error)
	ApplicationSchema                   appschemarepository.ApplicationSchemaRepository
	MetadataDefinitions                 metadatasdk.Definitions
	MetadataLocalization                metadatasdk.Localization
	AutomationWorker                    automationcontract.AutomationWorkerStore
	AutomationExecutions                automationrepository.AutomationExecutionRepository
	ActionExecutions                    actioncontract.ActionExecutionStore
	ActionAssurance                     actioncontract.ActionAssuranceStore
	IdentityPrincipals                  identitysdk.PrincipalResolver
	AgentTaskRunner                     agentsdk.TaskRunner
	AgentScheduledTasks                 agentsdk.ScheduledConversationTaskService
	AgentBusinessEvents                 agentsdk.BusinessEventConversationTaskService
	AgentTaskAttachmentFiles            *uploadapplication.AgentTaskAttachmentFileService
	ProjectExtensions                   *runtimeext.ProjectExtensionRegistry
	VerifyFileClean                     func(context.Context, string, runtimeext.FileVerificationRequest) (runtimeext.FileVerificationEvidence, error)
	OpenVerifiedFile                    func(context.Context, string, runtimeext.VerifiedFileRequest) (runtimeext.VerifiedFile, error)
	IssueFileDownload                   func(context.Context, string, runtimeext.Principal, runtimeext.FileDownloadRequest) (runtimeext.FileDownloadTicket, error)
	CreateDerivedFile                   func(context.Context, string, runtimeext.DerivedFileRequest) (runtimeext.DerivedFileEvidence, error)
	ValidateFileReferences              func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal) error
	WorkspaceAggregateCatalog           workspaceaggregatecontract.Catalog
	WorkspaceActiveResolver             workspaceaggregatecontract.ActiveResolver
	WorkspaceUsageResolver              workspaceaggregatecontract.UsageResolver
	WorkspaceAggregateRepository        workspaceaggregatecontract.Repository
	PrepareOutboxPayload                publicationhandoff.PayloadPreparer
	RuntimeStatus                       deploymentrepository.DeploymentRuntimeStatusRepository
	Notifications                       NotificationRenderer
	IdentityProjection                  identitysdk.Projection
	IdentityHandlerDeliveryBinder       identitysdk.HandlerDeliveryUnitOfWorkBinder
	AccountErasures                     lifecyclecontract.AccountErasures
	OrganizationUnitDeliveryBinder      organizationunit.UnitOfWorkBinder
	StoreOrganizationDeliveryBinder     identitysdk.StoreOrganizationDeliveryUnitOfWorkBinder
	WorkspaceIdentityUsageBinder        identitysdk.WorkspaceIdentityUsageUnitOfWorkBinder
	WorkspaceIdentityUsageCursor        actionapplication.WorkspaceIdentityUsageCursorCodec
	WorkspaceCommercialConfiguration    actionapplication.WorkspaceCommercialConfigurationLocker
	Worker                              workerplatform.Dependencies
}

// RuntimeServicesConfig keeps storage/security facts separate from executable
// code definitions. Neither input is a serialized all-product manifest.
type RuntimeServicesConfig struct {
	ProjectModel       projectmodel.RuntimeModel
	ProjectDefinitions runtimeext.ProjectDefinitions
	Actions            []definitionmodel.ActionSchema
	Integrations       appschemamodel.IntegrationSchema
	Dependencies       RuntimeServicesDependencies
}

func NewRuntimeServices(ctx context.Context, config RuntimeServicesConfig) *RuntimeServices {
	assembly := newRuntimeServicesAssembly(ctx, config)
	return &RuntimeServices{
		applications: assembly.Applications(), schema: assembly.RecordSchemaSnapshotProvider,
		schedulerDefinitionSource:  assembly.schedulerDefinitionSource,
		notificationEventPublisher: assembly.notificationEventPublisher,
		agentTaskAttachmentFiles:   config.Dependencies.AgentTaskAttachmentFiles,
		assembly:                   assembly,
		reportModule: ReportModuleApplicationPorts{
			Subjects:  assembly.reportModuleQueryHost,
			ObjectSQL: assembly.reportModuleQueryHost, SourceVersions: assembly.reportModuleQueryHost, Audit: assembly.reportModuleQueryHost,
			Authorization: assembly.reportModuleQueryHost,
			Terminals:     assembly.reportModuleSnapshotHost,
			Exports:       assembly.reportModuleExportHost,
			Tables:        config.Dependencies.ReportAnalysisTables,
		},
	}
}

// BindAgentScheduledTasks completes the startup-only circular composition:
// Agent needs Runtime authorization ports before it can publish conversation
// capabilities, while Runtime needs that published SDK service for Scheduler
// dispatch. Callers bind once before serving HTTP.
func (s *RuntimeServices) BindAgentScheduledTasks(tasks agentsdk.ScheduledConversationTaskService) error {
	if s == nil || s.assembly == nil || s.assembly.targetExecutionService == nil || tasks == nil {
		return fmt.Errorf("scheduled Agent task binding is incomplete")
	}
	if s.assembly.agentScheduledTasks != nil && s.assembly.agentScheduledTasks != tasks {
		return fmt.Errorf("scheduled Agent task binding already completed")
	}
	s.assembly.agentScheduledTasks = tasks
	s.assembly.targetExecutionService.UseAgentTargetRuntime(scheduledAgentTargetRuntimeAdapter{runtime: s.assembly, principals: s.assembly.identityPrincipals, tasks: tasks})
	return nil
}

// BindAgentBusinessEvents completes the same startup cycle for verified
// Integration events. Integration remains the durable webhook/event owner;
// Runtime only maps the current Identity principal into Agent's trusted port.
func (s *RuntimeServices) BindAgentBusinessEvents(events agentsdk.BusinessEventConversationTaskService) error {
	if s == nil || s.assembly == nil || events == nil {
		return fmt.Errorf("business-event Agent task binding is incomplete")
	}
	if s.assembly.agentBusinessEvents != nil && s.assembly.agentBusinessEvents != events {
		return fmt.Errorf("business-event Agent task binding already completed")
	}
	s.assembly.agentBusinessEvents = events
	return nil
}

func (s *RuntimeServices) AgentBusinessEvents() agentsdk.BusinessEventConversationTaskService {
	if s == nil || s.assembly == nil {
		return nil
	}
	return s.assembly.agentBusinessEvents
}

func newRuntimeServicesAssembly(ctx context.Context, config RuntimeServicesConfig) *runtimeAssembly {
	if ctx == nil {
		panic("composition.NewRuntimeServices requires a non-nil construction context")
	}
	model, definitions, deps := config.ProjectModel, config.ProjectDefinitions, config.Dependencies
	services := newRuntimeServicesState(ctx, definitions, config.Integrations, deps)
	queryPolicy := initializeSchemaAndRecordFoundation(services, deps)
	initializeWorkflowAutomationAndGovernance(services, deps)
	initializeRecordApplications(services)
	if services.dataExchangeProviders != nil {
		services.dataExchangeProviders.Freeze()
	}
	services.applyProjectMetadata(model, config.Actions, definitions, config.Integrations)
	services.targetExecutionService.UseAgentTargetRuntime(scheduledAgentTargetRuntimeAdapter{runtime: services, principals: services.identityPrincipals, tasks: services.agentScheduledTasks})
	services.targetExecutionService.UseNotificationTargetRuntime(scheduledNotificationTargetRuntimeAdapter{runtime: services, principals: services.identityPrincipals, publish: services.notificationEventPublisher})
	initializeActionsAndPublication(ctx, services, deps, queryPolicy)
	// The Scheduler adapter captures the Action Application by interface value,
	// so bind it only after Action composition has completed.
	services.targetExecutionService.UseBusinessActionTargetRuntime(newSchedulerBusinessActionRuntime(services))
	return services
}
