package composition

import (
	"context"
	publicationrepository "github.com/domainry/domainry-runtime/runtime/domain/publication/repository"

	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	partysdk "github.com/domainry/domainry-party-sdk"
	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	automationapplication "github.com/domainry/domainry-runtime/runtime/application/automation"
	publicationhandoff "github.com/domainry/domainry-runtime/runtime/application/publicationhandoff"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	reportsnapshot "github.com/domainry/domainry-runtime/runtime/application/report/snapshot"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	connector "github.com/domainry/domainry-connector-sdk"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	lifecyclepersistence "github.com/domainry/domainry-lifecycle-sdk/persistence"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
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
	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
	resilience "github.com/domainry/domainry-runtime/runtime/platform/resilience"
)

// RuntimeServices is the immutable facade exported by the composition root.
// Construction-only repositories, indexes, and owner services remain on the
// private runtimeAssembly and are captured only by their explicit ports.
type RuntimeServices struct {
	applications RuntimeApplications
	schema       runtimeSchemaReader
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
	ReportDatasetRows                   reportcontract.ReportDatasetRowReader
	ReportObjectSQL                     reportcontract.ReportObjectSQLExecutor
	ReportSnapshots                     reportcontract.ReportSnapshotReader
	ReportSnapshotSources               reportcontract.ReportSnapshotSourceVersionReader
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
	WorkerWakeups                       *workerplatform.WakeupBroker
	WorkflowWorker                      workflowcontract.WorkflowWorkerStore
	WorkflowDefinitions                 workflowcontract.WorkflowDefinitionStore
	WorkflowProcesses                   workflowcontract.WorkflowProcessStore
	WorkflowDecisions                   workflowcontract.WorkflowDecisionStore
	WorkflowNotificationCompiler        func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	WorkflowTaskNotificationCommitter   workflowapplication.WorkflowTaskNotificationCommitter
	RecordNotificationCompiler          func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	ReportNotificationCompiler          func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	ReportSnapshotNotificationCommitter reportsnapshot.ReportSnapshotNotificationCommitter
	AutomationNotificationCompiler      func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	AutomationNotificationCommitter     automationapplication.AutomationExecutionNotificationCommitter
	NotificationIntentPublisher         func(context.Context, notificationmodel.NotificationIntent) error
	IntegrationOwnerManagement          integrationsdk.Management
	IntegrationOwnerOperations          integrationsdk.Operations
	ApplicationSchema                   appschemarepository.ApplicationSchemaRepository
	AutomationWorker                    automationcontract.AutomationWorkerStore
	AutomationExecutions                automationrepository.AutomationExecutionRepository
	BusinessEvidence                    changeplanrepository.ChangePlanEvidenceRepository
	ActionExecutions                    actioncontract.ActionExecutionStore
	ActionAssurance                     actioncontract.ActionAssuranceStore
	AgentTaskRuns                       agentpersistence.AgentTaskRunRepository
	AgentPrincipals                     identitysdk.PrincipalResolver
	AgentTaskRunner                     agentsdk.TaskRunner
	AgentInteractiveRunner              agentsdk.InteractiveRunner
	AgentTaskCredentialKey              []byte
	AgentTaskWorkerConfig               agentapplication.AgentTaskWorkerConfig
	BusinessHandlers                    *runtimeext.BusinessHandlerRegistry
	VerifyFileClean                     func(context.Context, string, runtimeext.FileVerificationRequest) (runtimeext.FileVerificationEvidence, error)
	PrepareOutboxPayload                publicationhandoff.PayloadPreparer
	ConnectorProviders                  *connector.Registry
	RuntimeStatus                       deploymentrepository.DeploymentRuntimeStatusRepository
	Notifications                       NotificationRenderer
	IdentityDirectory                   identitysdk.Directory
	PartyDirectory                      partysdk.Directory
	IntegrationAPILimiter               ratelimit.Limiter
	IntegrationPolicyStore              resilience.Store
	Lifecycle                           lifecyclepersistence.LifecycleRepository
	LifecycleExecutors                  []lifecyclecontract.OwnerLifecycleExecutor
	LifecycleSubjectResolver            lifecyclecontract.SubjectIdentityResolver
	LifecycleSubjectHandlers            []lifecyclecontract.SubjectDataHandler
	LifecycleExternalErasure            lifecyclecontract.ExternalErasureHandler
	LifecycleArtifacts                  lifecyclecontract.SubjectArtifactStore
	LifecycleUploadArtifacts            lifecyclecontract.UploadArtifactStore
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
	services.applyManifestMetadata(manifest.TemplateID, manifest.Version, manifest.Name, manifest.Objects, manifest.Actions, manifest.Workflows, manifest.AutomationRules, manifest.Dictionaries, manifest.Integrations, manifest.Reports, manifest.Skills, manifest.Agents, manifest.IdentityProfileExtensions)
	services.applyManifestAgentMetadata(manifest.AgentTasks, manifest.AgentEntrypoints, manifest.AgentServicePrincipals)
	initializeIntegrationAndBusinessSystem(ctx, services, manifest, deps, queryPolicy)
	return services
}
