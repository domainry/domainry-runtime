package composition

import (
	"context"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"sync"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/audit"
	automationapplication "github.com/domainry/domainry-runtime/runtime/application/automation"
	businesssystemapplication "github.com/domainry/domainry-runtime/runtime/application/businesssystem"
	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"
	changeplanapplication "github.com/domainry/domainry-runtime/runtime/application/changeplan"
	deploymentbusiness "github.com/domainry/domainry-runtime/runtime/application/deployment"
	businessintegration "github.com/domainry/domainry-runtime/runtime/application/integration"
	lifecycleapplication "github.com/domainry/domainry-runtime/runtime/application/lifecycle"
	metadataapplication "github.com/domainry/domainry-runtime/runtime/application/metadata"
	pipelineapplication "github.com/domainry/domainry-runtime/runtime/application/pipeline"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	recordtimerapplication "github.com/domainry/domainry-runtime/runtime/application/recordtimer"
	reportapplication "github.com/domainry/domainry-runtime/runtime/application/report"
	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	surfacecontextbusiness "github.com/domainry/domainry-runtime/runtime/application/surfacecontext"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	actioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	actionruntime "github.com/domainry/domainry-runtime/runtime/domain/action/runtime"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	auditrepository "github.com/domainry/domainry-runtime/runtime/domain/audit/repository"
	automationcontract "github.com/domainry/domainry-runtime/runtime/domain/automation/contract"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationrepository "github.com/domainry/domainry-runtime/runtime/domain/automation/repository"
	changeplanrepository "github.com/domainry/domainry-runtime/runtime/domain/changeplan/repository"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	deploymentrepository "github.com/domainry/domainry-runtime/runtime/domain/deployment/repository"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	metadatarepository "github.com/domainry/domainry-runtime/runtime/domain/metadata/repository"
	metadata "github.com/domainry/domainry-runtime/runtime/domain/metadata/service"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
	resilience "github.com/domainry/domainry-runtime/runtime/platform/resilience"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

// runtimeAssembly is the private, constructor-only composition graph.
type runtimeAssembly struct {
	templateID, templateVersion, name string
	productBrandName                  string
	actionRuntimeRevision             string
	actionProjectRevision             string
	actionMetadataRevision            string
	schema                            map[string]definitionmodel.ObjectSchema
	views                             []definitionmodel.ViewSchema
	actions                           map[string]definitionmodel.ActionSchema
	workflows                         map[string]definitionmodel.WorkflowSchema
	automationRules                   map[string]automationmodel.AutomationRuleSchema
	dictionaries                      []metadatamodel.DictionarySchema
	integrations                      integrationmodel.IntegrationSchema
	reports                           []reportmodel.ReportSchema
	reportObjects                     map[string]struct{}
	entrypoints                       []definitionmodel.EntryPointSchema
	skills                            []agentmodel.SkillSchema
	agents                            []agentmodel.AgentSchema
	agentTasks                        []agentmodel.AgentTaskDefinition
	agentEntrypoints                  []agentmodel.AgentEntrypointAssignment
	agentServicePrincipals            []agentmodel.AgentServicePrincipalBinding
	identityProfileExtensions         []profilebindingmodel.Binding
	recordRepo                        recordrepository.RecordRepository
	reportDatasetRows                 reportcontract.ReportDatasetRowReader
	reportObjectSQL                   reportcontract.ReportObjectSQLExecutor
	reportSnapshots                   reportcontract.ReportSnapshotStore
	reportExportArtifacts             reportcontract.ReportExportArtifactStore
	reportSnapshotSources             reportcontract.ReportSnapshotSourceVersionReader
	auditRepo                         auditrepository.AuditRepository
	auditApplicationService           *auditapplication.AuditApplicationService
	auditExportTokenKey               []byte
	schemaService                     *metadataapplication.MetadataSchemaApplicationService
	actionService                     *actionapplication.ActionApplicationService
	runtimeStatusService              *deploymentbusiness.DeploymentRuntimeStatusApplicationService
	surfaceContextService             *surfacecontextbusiness.SurfaceContextApplicationService
	*recordservice.RecordQueryPolicyDomainService
	*RecordSchemaSnapshotProvider
	*pipelineapplication.PipelineApplicationService
	*recordservice.RecordValidationDomainService
	recordStateMachineEffects *recordapplication.RecordStateMachineEffectApplicationService
	*actionapplication.ActionPreconditionApplicationService
	recordApplicationService *recordapplication.RecordApplicationService
	*recordservice.RecordDomainService
	recordRuntimeState
	*actionruntime.ActionExecutionRuntime
	internalMutations                   *recordapplication.RecordInternalMutationApplicationService
	businessSystemService               *businesssystemapplication.BusinessSystemApplicationService
	integrationService                  *businessintegration.IntegrationApplicationService
	lifecycleService                    *lifecycleapplication.LifecycleApplicationService
	integrationWorkerRepo               integrationrepository.IntegrationWorkerRepository
	workerWakeups                       *workerplatform.WakeupBroker
	integrationConfigRepo               integrationrepository.IntegrationConfigRepository
	integrationEventRepo                integrationrepository.IntegrationEventRepository
	integrationDeliveryRepo             integrationrepository.IntegrationDeliveryRepository
	integrationNotificationCompiler     businessintegration.IntegrationNotificationCompiler
	integrationNotificationPublisher    businessintegration.IntegrationNotificationPublisher
	integrationCredentialNotifications  businessintegration.IntegrationCredentialNotificationCommitter
	integrationCredentialExpirySource   businessintegration.IntegrationCredentialExpirySource
	workflowWorkerRepo                  workflowcontract.WorkflowWorkerStore
	workflowApplicationService          *workflowapplication.WorkflowApplicationService
	metadataApplicationService          *metadataapplication.MetadataApplicationService
	automationApplicationService        *automationapplication.AutomationApplicationService
	workflowDecisionRepo                workflowcontract.WorkflowDecisionStore
	workflowDefinitionRepo              workflowcontract.WorkflowDefinitionStore
	workflowProcessRepo                 workflowcontract.WorkflowProcessStore
	workflowNotificationCompiler        func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	workflowTaskNotificationCommitter   workflowapplication.WorkflowTaskNotificationCommitter
	notificationIntentPublisher         func(context.Context, notificationmodel.NotificationIntent) error
	recordNotificationCompiler          func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	recordBatchNotificationCommitter    recordapplication.RecordBatchTerminalNotificationCommitter
	reportNotificationCompiler          func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	reportSnapshotNotificationCommitter reportapplication.ReportSnapshotNotificationCommitter
	automationNotificationCompiler      func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	automationNotificationCommitter     automationapplication.AutomationExecutionNotificationCommitter
	metadataRepo                        metadatarepository.MetadataRepository
	automationWorkerRepo                automationcontract.AutomationWorkerStore
	automationExecutionRepo             automationrepository.AutomationExecutionRepository
	businessChangePlanRepo              changeplanrepository.ChangePlanRepository
	businessEvidenceRepo                changeplanrepository.ChangePlanEvidenceRepository
	runtimeStatusRepo                   deploymentrepository.DeploymentRuntimeStatusRepository
	identityDirectory                   identitysdk.Directory
	actionAssuranceStore                actioncontract.ActionAssuranceStore
	workflowProcesses                   *workflowapplication.WorkflowProcessEngine
	connectorRegistry                   *businessintegration.ConnectorRegistry
	verifyFileClean                     func(context.Context, string, runtimeext.FileVerificationRequest) (runtimeext.FileVerificationEvidence, error)
	prepareOutboxPayload                businessintegration.OutboxPayloadPreparer
	integrationPolicyStore              resilience.Store
	apiKeyRateLimiter                   ratelimit.Limiter
	dictionaryRuntime                   *metadata.MetadataDictionaryDomainService
	frontendCapabilities                *deploymentbusiness.DeploymentFrontendCapabilityApplicationService
	authoringCapabilities               *capabilityapplication.CapabilityAuthoringApplicationService
	businessReferences                  *changeplanapplication.ChangePlanReferenceApplicationService
	businessChangePlans                 *changeplanapplication.ChangePlanApplicationService
	reportsService                      *reportapplication.ReportApplicationService
	reportExportControls                []reportmodel.ReportExportControlSchema
	schedulerService                    *schedulerapplication.SchedulerApplicationService
	recordTimerService                  *recordtimerapplication.RecordTimerApplicationService
	agentTaskRunService                 *agentapplication.AgentTaskRunApplicationService
	agentInteractiveRunService          *agentapplication.AgentInteractiveRunApplicationService
	newAgentInteractiveExecution        func(*agentapplication.AgentToolGateway) *agentapplication.AgentInteractiveExecutionApplicationService
	agentAuthorizationService           *agentapplication.AgentAuthorizationApplicationService
	agentTaskDispatchService            *agentapplication.AgentTaskDispatchApplicationService
	agentTaskWorker                     *agentapplication.AgentTaskWorker
	agentPrincipals                     identitysdk.PrincipalResolver
	mu                                  sync.RWMutex // guards concurrent schema reads vs. hot-reload writes
	workerDependencies                  workerplatform.Dependencies
}
