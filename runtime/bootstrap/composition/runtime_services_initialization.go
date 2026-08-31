package composition

import (
	"context"

	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	lifecyclecore "github.com/domainry/domainry-lifecycle-sdk/application"
	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	lifecycleapplication "github.com/domainry/domainry-runtime/runtime/application/lifecycle"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	actionruntime "github.com/domainry/domainry-runtime/runtime/domain/action/runtime"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	recordruntime "github.com/domainry/domainry-runtime/runtime/domain/record/runtime"
	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
	resilience "github.com/domainry/domainry-runtime/runtime/platform/resilience"
)

// newRuntimeServicesState allocates state before ordered service initialization.
func newRuntimeServicesState(ctx context.Context, manifest manifestmodel.ManifestSchema, deps RuntimeServicesDependencies) *runtimeAssembly {
	dataExchangeProviders := deps.DataExchangeProviders
	if dataExchangeProviders == nil && deps.DataExchange != nil {
		dataExchangeProviders = recordapplication.NewDataExchangeProviders(nil)
	}
	identityDirectory := deps.IdentityDirectory
	policyStore := deps.IntegrationPolicyStore
	if policyStore == nil {
		policyStore = resilience.NewMemoryStore(resilience.DefaultMemoryCapacity)
	}
	apiLimiter := deps.IntegrationAPILimiter
	if apiLimiter == nil {
		apiLimiter = ratelimit.NewMemoryLimiter(ratelimit.DefaultMemoryCapacity)
	}
	connectorRegistry := newRuntimeConnectorCatalog(manifest.Integrations)
	auditApplicationService := deps.AuditApplication
	if auditApplicationService == nil {
		auditApplicationService = auditapplication.NewAuditApplicationService(deps.Audit)
	}
	services := &runtimeAssembly{
		schedulerDefinitions:                cloneSchedulerDefinitionMaps(manifest.SchedulerDefinitions),
		productBrandName:                    deps.ProductBrandName,
		actionRuntimeRevision:               deps.ActionRuntimeRevision,
		actionProjectRevision:               deps.ActionProjectRevision,
		actionMetadataRevision:              deps.ActionMetadataRevision,
		recordRepo:                          deps.Records,
		dataExchange:                        deps.DataExchange,
		dataExchangeProviders:               dataExchangeProviders,
		reportDatasetRows:                   deps.ReportDatasetRows,
		reportObjectSQL:                     deps.ReportObjectSQL,
		reportSnapshots:                     deps.ReportSnapshots,
		reportSnapshotSources:               deps.ReportSnapshotSources,
		auditRepo:                           deps.Audit,
		publicationRepository:               deps.IntegrationPublication,
		integrationPublicationWorkerRepo:    deps.IntegrationPublicationWorker,
		workerWakeups:                       deps.WorkerWakeups,
		integrationOwnerDelivery:            deps.IntegrationOwnerDelivery,
		integrationOwnerCatalog:             deps.IntegrationOwnerCatalog,
		workflowWorkerRepo:                  deps.WorkflowWorker,
		workflowDefinitionRepo:              deps.WorkflowDefinitions,
		workflowProcessRepo:                 deps.WorkflowProcesses,
		workflowDecisionRepo:                deps.WorkflowDecisions,
		workflowNotificationCompiler:        deps.WorkflowNotificationCompiler,
		workflowTaskNotificationCommitter:   deps.WorkflowTaskNotificationCommitter,
		notificationIntentPublisher:         deps.NotificationIntentPublisher,
		recordNotificationCompiler:          deps.RecordNotificationCompiler,
		reportNotificationCompiler:          deps.ReportNotificationCompiler,
		reportSnapshotNotificationCommitter: deps.ReportSnapshotNotificationCommitter,
		automationNotificationCompiler:      deps.AutomationNotificationCompiler,
		automationNotificationCommitter:     deps.AutomationNotificationCommitter,
		applicationSchemaRepo:               deps.ApplicationSchema,
		automationWorkerRepo:                deps.AutomationWorker,
		automationExecutionRepo:             deps.AutomationExecutions,
		businessEvidenceRepo:                deps.BusinessEvidence,
		runtimeStatusRepo:                   deps.RuntimeStatus,
		actionAssuranceStore:                deps.ActionAssurance,
		connectorRegistry:                   connectorRegistry,
		verifyFileClean:                     deps.VerifyFileClean,
		prepareOutboxPayload:                deps.PrepareOutboxPayload,
		integrationPolicyStore:              policyStore,
		apiKeyRateLimiter:                   apiLimiter,
		identityDirectory:                   identityDirectory,
		auditApplicationService:             auditApplicationService,
		auditExportTokenKey:                 append([]byte(nil), deps.AuditExportTokenKey...),
		workerDependencies:                  workerplatform.NormalizeDependencies(deps.Worker),
	}
	services.lifecycleService = lifecycleapplication.NewLifecycleApplicationService(ctx, lifecyclecore.LifecycleApplicationDependencies{
		Repository: deps.Lifecycle, Executors: deps.LifecycleExecutors, SubjectResolver: deps.LifecycleSubjectResolver,
		SubjectHandlers: deps.LifecycleSubjectHandlers, ExternalErasure: deps.LifecycleExternalErasure, Artifacts: deps.LifecycleArtifacts, UploadArtifacts: deps.LifecycleUploadArtifacts,
	})
	services.dictionaryRuntime = appschemaservice.NewApplicationSchemaDictionaryDomainService(manifest.Dictionaries)
	services.RecordSchemaSnapshotProvider = &RecordSchemaSnapshotProvider{snapshot: func() appschemamodel.ApplicationSchemaSnapshot { return recordSchemaSnapshot(services) }}
	services.ActionExecutionRuntime = actionruntime.NewActionExecutionRuntime(deps.ActionExecutions)
	services.agentTaskRunService = agentapplication.NewAgentTaskRunApplicationServiceWithAudit(deps.AgentTaskRuns, services.workerDependencies.Clock, runtimeAgentTaskTerminalCommitter{records: services}, auditApplicationService)
	if interactiveRuns, ok := deps.AgentTaskRuns.(agentpersistence.AgentInteractiveRunRepository); ok {
		services.agentInteractiveRunService = agentapplication.NewAgentInteractiveRunApplicationService(interactiveRuns, services.workerDependencies.Clock, services.workerDependencies.IDs)
	}
	agentPrincipals := deps.AgentPrincipals
	services.agentPrincipals = agentPrincipals
	services.agentAuthorizationService = agentapplication.NewAgentAuthorizationApplicationService(agentapplication.AgentAuthorizationDependencies{Principals: agentPrincipals, Schema: services.RecordSchemaSnapshotProvider, Records: runtimeAgentRecordVisibility{records: services}})
	services.agentTaskDispatchService = agentapplication.NewAgentTaskDispatchApplicationService(services.agentAuthorizationService, services.workerDependencies.Clock, services.workerDependencies.IDs)
	if deps.AgentTaskRunner != nil {
		credentials := agentapplication.NewAgentTaskCredentialApplicationService(deps.AgentTaskCredentialKey, services.workerDependencies.Clock, services.workerDependencies.IDs)
		executor := agentapplication.NewAgentTaskRunnerExecutor(agentapplication.AgentTaskRunnerExecutorDependencies{Runner: deps.AgentTaskRunner, Authorization: services.agentAuthorizationService, Credentials: credentials})
		services.agentTaskWorker = agentapplication.NewAgentTaskWorker(services.agentTaskRunService, executor, services.workerDependencies, deps.AgentTaskWorkerConfig)
	}
	agentTaskWakeup := func(locator agentapplication.AgentTaskLocator) {
		if services.agentTaskWorker != nil {
			services.agentTaskWorker.Wake(locator)
		}
	}
	agentapplication.BindAgentTaskRunWakeup(services.agentTaskRunService, agentTaskWakeup)
	if services.agentInteractiveRunService != nil {
		agentapplication.BindAgentInteractiveRunWakeup(services.agentInteractiveRunService, agentTaskWakeup)
	}
	services.RecordMutationExecutionRuntime = recordruntime.NewRecordMutationExecutionRuntime(deps.RecordExecutions)
	return services
}
