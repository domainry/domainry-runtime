package composition

import (
	"context"
	publicationrepository "github.com/domainry/domainry-runtime/runtime/domain/publication/repository"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	organizationunit "github.com/domainry/domainry-identity/organizationunit"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	automationapplication "github.com/domainry/domainry-runtime/runtime/application/automation"
	publicationhandoff "github.com/domainry/domainry-runtime/runtime/application/publicationhandoff"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
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
	changeplanrepository "github.com/domainry/domainry-runtime/runtime/domain/changeplan/repository"
	deploymentrepository "github.com/domainry/domainry-runtime/runtime/domain/deployment/repository"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
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
	applications              RuntimeApplications
	schema                    runtimeSchemaReader
	reportModule              ReportModuleApplicationPorts
	schedulerDefinitionSource SchedulerDefinitionSource
}

type ReportModuleApplicationPorts struct {
	Subjects       reportmodulehost.SubjectResolver
	ObjectSQL      reportmodulehost.ObjectSQLExecutor
	SourceVersions reportmodulehost.SourceVersionReader
	Audit          reportmodulehost.ExecutionAudit
	Authorization  reportmodulehost.ExportAuthorization
	Terminals      reportmodulehost.SnapshotTerminalCommitter
	Exports        reportmodulehost.ExportGateway
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
	ApplicationSchema                   appschemarepository.ApplicationSchemaRepository
	MetadataDefinitions                 metadatasdk.Definitions
	MetadataLocalization                metadatasdk.Localization
	AutomationWorker                    automationcontract.AutomationWorkerStore
	AutomationExecutions                automationrepository.AutomationExecutionRepository
	BusinessEvidence                    changeplanrepository.ChangePlanEvidenceRepository
	ActionExecutions                    actioncontract.ActionExecutionStore
	ActionAssurance                     actioncontract.ActionAssuranceStore
	AgentPrincipals                     identitysdk.PrincipalResolver
	AgentTaskRunner                     agentsdk.TaskRunner
	BusinessHandlers                    *runtimeext.BusinessHandlerRegistry
	VerifyFileClean                     func(context.Context, string, runtimeext.FileVerificationRequest) (runtimeext.FileVerificationEvidence, error)
	WorkspaceAggregateCatalog           workspaceaggregatecontract.Catalog
	WorkspaceActiveResolver             workspaceaggregatecontract.ActiveResolver
	WorkspaceUsageResolver              workspaceaggregatecontract.UsageResolver
	WorkspaceAggregateRepository        workspaceaggregatecontract.Repository
	PrepareOutboxPayload                publicationhandoff.PayloadPreparer
	RuntimeStatus                       deploymentrepository.DeploymentRuntimeStatusRepository
	Notifications                       NotificationRenderer
	IdentityProjection                  identitysdk.Projection
	IdentityHandlerDeliveryBinder       identitysdk.HandlerDeliveryUnitOfWorkBinder
	OrganizationUnitDeliveryBinder      organizationunit.UnitOfWorkBinder
	StoreOrganizationDeliveryBinder     identitysdk.StoreOrganizationDeliveryUnitOfWorkBinder
	WorkspaceIdentityUsageBinder        identitysdk.WorkspaceIdentityUsageUnitOfWorkBinder
	WorkspaceIdentityUsageCursor        actionapplication.WorkspaceIdentityUsageCursorCodec
	WorkspaceCommercialConfiguration    actionapplication.WorkspaceCommercialConfigurationLocker
	Worker                              workerplatform.Dependencies
}

// RuntimeServicesConfig keeps the mutable Runtime schema and its concrete
// infrastructure dependencies visible as two independently reviewable inputs.
// The Manifest value is copied into Runtime-owned indexes during construction.
type RuntimeServicesConfig struct {
	Manifest     manifestmodel.ManifestSchema
	Dependencies RuntimeServicesDependencies
}

func NewRuntimeServices(ctx context.Context, config RuntimeServicesConfig) *RuntimeServices {
	assembly := newRuntimeServicesAssembly(ctx, config)
	return &RuntimeServices{
		applications: assembly.Applications(), schema: assembly.RecordSchemaSnapshotProvider,
		schedulerDefinitionSource: assembly.schedulerDefinitionSource,
		reportModule: ReportModuleApplicationPorts{
			Subjects:  assembly.reportModuleQueryHost,
			ObjectSQL: assembly.reportModuleQueryHost, SourceVersions: assembly.reportModuleQueryHost, Audit: assembly.reportModuleQueryHost,
			Authorization: assembly.reportModuleQueryHost,
			Terminals:     assembly.reportModuleSnapshotHost,
			Exports:       assembly.reportModuleExportHost,
		},
	}
}

func newRuntimeServicesAssembly(ctx context.Context, config RuntimeServicesConfig) *runtimeAssembly {
	if ctx == nil {
		panic("composition.NewRuntimeServices requires a non-nil construction context")
	}
	manifest, deps := config.Manifest, config.Dependencies
	services := newRuntimeServicesState(ctx, manifest, deps)
	queryPolicy := initializeSchemaAndRecordFoundation(services, deps)
	initializeWorkflowAutomationAndGovernance(services, deps)
	initializeRecordApplications(services)
	services.applyManifestMetadata(manifest.TemplateID, manifest.Version, manifest.Name, manifest.EffectiveTimeZone(), manifest.Objects, manifest.Actions, manifest.Workflows, manifest.AutomationRules, manifest.Dictionaries, manifest.Integrations, manifest.Reports, manifest.Skills, manifest.Agents, manifest.IdentityProfileExtensions)
	services.applyManifestAgentMetadata(manifest.AgentTasks, manifest.AgentEntrypoints, manifest.AgentServicePrincipals)
	initializeIntegrationAndBusinessSystem(ctx, services, manifest, deps, queryPolicy)
	return services
}
