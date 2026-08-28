package composition

import (
	"context"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/audit"
	automationapplication "github.com/domainry/domainry-runtime/runtime/application/automation"
	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	reportapplication "github.com/domainry/domainry-runtime/runtime/application/report"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	agentrepository "github.com/domainry/domainry-runtime/runtime/domain/agent/repository"

	connector "github.com/domainry/domainry-connector-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	auditcontract "github.com/domainry/domainry-runtime/runtime/domain/audit/contract"
	auditrepository "github.com/domainry/domainry-runtime/runtime/domain/audit/repository"
	automationcontract "github.com/domainry/domainry-runtime/runtime/domain/automation/contract"
	automationrepository "github.com/domainry/domainry-runtime/runtime/domain/automation/repository"
	changeplanrepository "github.com/domainry/domainry-runtime/runtime/domain/changeplan/repository"
	deploymentrepository "github.com/domainry/domainry-runtime/runtime/domain/deployment/repository"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
	lifecyclerepository "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/repository"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	metadatarepository "github.com/domainry/domainry-runtime/runtime/domain/metadata/repository"
	notificationcontract "github.com/domainry/domainry-runtime/runtime/domain/notification/contract"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	partyrepository "github.com/domainry/domainry-runtime/runtime/domain/party/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
	resilience "github.com/domainry/domainry-runtime/runtime/platform/resilience"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

// RuntimeServices is the immutable facade exported by the composition root.
// Construction-only repositories, indexes, and owner services remain on the
// private runtimeAssembly and are captured only by their explicit ports.
type RuntimeServices struct {
	applications RuntimeApplications
	schema       runtimeSchemaReader
}

type runtimeSchemaReader interface {
	Schema() metadatamodel.MetadataSchemaSnapshot
	SchemaForPrincipal(context.Context, principalmodel.Principal) metadatamodel.MetadataSchemaSnapshot
}

type NotificationRenderer interface {
	Render(context.Context, notificationcontract.NotificationRenderRequest) (notificationmodel.RenderedNotification, error)
}

// RuntimeServicesDependencies declares every explicit repository and external
// port required by the Runtime composition root.
type RuntimeServicesDependencies struct {
	ProductBrandName                    string
	ActionRuntimeRevision               string
	ActionProjectRevision               string
	ActionMetadataRevision              string
	Records                             recordrepository.RecordRepository
	ReportDatasetRows                   reportcontract.ReportDatasetRowReader
	ReportObjectSQL                     reportcontract.ReportObjectSQLExecutor
	ReportSnapshots                     reportcontract.ReportSnapshotStore
	ReportExportArtifacts               reportcontract.ReportExportArtifactStore
	ReportSnapshotSources               reportcontract.ReportSnapshotSourceVersionReader
	RecordExecutions                    recordcontract.RecordMutationExecutionStore
	Audit                               auditrepository.AuditRepository
	AuditExports                        auditcontract.AuditBusinessExportStore
	AuditApplication                    *auditapplication.AuditApplicationService
	AuditExportTokenKey                 []byte
	IntegrationConfig                   integrationrepository.IntegrationConfigRepository
	IntegrationEvents                   integrationrepository.IntegrationEventRepository
	IntegrationDelivery                 integrationrepository.IntegrationDeliveryRepository
	IntegrationWorker                   integrationrepository.IntegrationWorkerRepository
	WorkerWakeups                       *workerplatform.WakeupBroker
	WorkflowWorker                      workflowcontract.WorkflowWorkerStore
	WorkflowDefinitions                 workflowcontract.WorkflowDefinitionStore
	WorkflowProcesses                   workflowcontract.WorkflowProcessStore
	WorkflowDecisions                   workflowcontract.WorkflowDecisionStore
	WorkflowNotificationCompiler        func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	WorkflowTaskNotificationCommitter   workflowapplication.WorkflowTaskNotificationCommitter
	RecordNotificationCompiler          func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	RecordBatchNotificationCommitter    recordapplication.RecordBatchTerminalNotificationCommitter
	ReportNotificationCompiler          func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	ReportSnapshotNotificationCommitter reportapplication.ReportSnapshotNotificationCommitter
	AutomationNotificationCompiler      func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	AutomationNotificationCommitter     automationapplication.AutomationExecutionNotificationCommitter
	NotificationIntentPublisher         func(context.Context, notificationmodel.NotificationIntent) error
	Metadata                            metadatarepository.MetadataRepository
	AutomationWorker                    automationcontract.AutomationWorkerStore
	AutomationExecutions                automationrepository.AutomationExecutionRepository
	BusinessChangePlans                 changeplanrepository.ChangePlanRepository
	BusinessEvidence                    changeplanrepository.ChangePlanEvidenceRepository
	ActionExecutions                    actioncontract.ActionExecutionStore
	ActionAssurance                     actioncontract.ActionAssuranceStore
	AgentTaskRuns                       agentrepository.AgentTaskRunRepository
	AgentPrincipals                     identitysdk.PrincipalResolver
	AgentTaskRunner                     agentapplication.AgentTaskRunner
	AgentInteractiveRunner              agentapplication.InteractiveAgentRunner
	AgentTaskCredentialKey              []byte
	AgentTaskWorkerConfig               agentapplication.AgentTaskWorkerConfig
	BusinessHandlers                    *runtimeext.BusinessHandlerRegistry
	VerifyFileClean                     func(context.Context, string, runtimeext.FileVerificationRequest) (runtimeext.FileVerificationEvidence, error)
	PrepareOutboxPayload                integrationapplication.OutboxPayloadPreparer
	ConnectorProviders                  *connector.Registry
	RuntimeStatus                       deploymentrepository.DeploymentRuntimeStatusRepository
	FrontendCapabilities                deploymentrepository.DeploymentFrontendCapabilityRepository
	Notifications                       NotificationRenderer
	IdentityDirectory                   identitysdk.Directory
	PartyDirectory                      partyrepository.PartyRepository
	IntegrationAPILimiter               ratelimit.Limiter
	IntegrationNotificationCompiler     integrationapplication.IntegrationNotificationCompiler
	IntegrationNotificationPublisher    integrationapplication.IntegrationNotificationPublisher
	IntegrationCredentialNotifications  integrationapplication.IntegrationCredentialNotificationCommitter
	IntegrationCredentialExpirySource   integrationapplication.IntegrationCredentialExpirySource
	IntegrationPolicyStore              resilience.Store
	Lifecycle                           lifecyclerepository.LifecycleRepository
	LifecycleExecutors                  []lifecyclecontract.OwnerLifecycleExecutor
	LifecycleSubjectResolver            lifecyclecontract.SubjectIdentityResolver
	LifecycleSubjectHandlers            []lifecyclecontract.SubjectDataHandler
	LifecycleExternalErasure            lifecyclecontract.ExternalErasureHandler
	LifecycleArtifacts                  lifecyclecontract.SubjectArtifactStore
	LifecycleUploadArtifacts            lifecyclecontract.UploadArtifactStore
	BatchJobQueueLimit                  int
	BatchJobWorkspaceQueueLimit         int
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
	return &RuntimeServices{applications: assembly.Applications(), schema: assembly.RecordSchemaSnapshotProvider}
}

func newRuntimeServicesAssembly(ctx context.Context, config RuntimeServicesConfig) *runtimeAssembly {
	if ctx == nil {
		panic("composition.NewRuntimeServices requires a non-nil construction context")
	}
	manifest, deps := config.Manifest, config.Dependencies
	services := newRuntimeServicesState(ctx, manifest, deps)
	services.reportExportControls = append([]reportmodel.ReportExportControlSchema(nil), manifest.ReportExportControls...)
	queryPolicy := initializeSchemaAndRecordFoundation(services, deps)
	initializeWorkflowAutomationAndGovernance(services, deps)
	initializeRecordApplications(services)
	services.applyManifestMetadata(manifest.TemplateID, manifest.Version, manifest.Name, manifest.Objects, manifest.Views, manifest.Actions, manifest.Workflows, manifest.AutomationRules, manifest.Dictionaries, manifest.Integrations, manifest.Reports, manifest.EntryPoints, manifest.Skills, manifest.Agents, manifest.IdentityProfileExtensions)
	services.applyManifestAgentMetadata(manifest.AgentTasks, manifest.AgentEntrypoints, manifest.AgentServicePrincipals)
	initializeIntegrationAndBusinessSystem(ctx, services, manifest, deps, queryPolicy)
	return services
}
