package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	auditsdk "github.com/domainry/domainry-audit-sdk"
	connector "github.com/domainry/domainry-connector-sdk"
	dataexchangesdk "github.com/domainry/domainry-data-exchange-sdk"
	dataexchangemodulehost "github.com/domainry/domainry-data-exchange-sdk/modulehost"
	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulehttp"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	organizationunit "github.com/domainry/domainry-identity-sdk/organizationunit"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	monitoringsdk "github.com/domainry/domainry-monitoring-sdk"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-notification-sdk/modulehost"
	reportsdk "github.com/domainry/domainry-report-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	notificationfacade "github.com/domainry/domainry-runtime/runtime/application/notificationfacade"
	publicationhandoff "github.com/domainry/domainry-runtime/runtime/application/publicationhandoff"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	transportbootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap/transport"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/blobstore"
	runtimeauditmodule "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/auditmodule"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	deploymentpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/deployment"
	notificationpublication "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/notificationpublication"
	publicationhandoffpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/publicationhandoff"
	workflowpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workflow"
	workspaceprovisionpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workspaceprovision"
	principalcache "github.com/domainry/domainry-runtime/runtime/infrastructure/principalcache"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/runtime/platform/localization"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
	schedulermodulehost "github.com/domainry/domainry-scheduler-sdk/modulehost"
	schedulersaashost "github.com/domainry/domainry-scheduler-sdk/saashost"
)

func newWithExtensionsUsingAllFactoriesAndStore(ctx context.Context, cfg config.Config, projectExtensions *runtimeext.ProjectExtensionRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, identityBinding identitysdk.Binding, foundationModules FoundationModuleFactories, notificationFactory notificationsdk.Factory, monitoringFactory monitoringsdk.Factory, schedulerFactory schedulersdk.Factory, dataExchangeFactory dataexchangesdk.Factory, agentFactory agentsdk.Factory, integrationFactory integrationsdk.Factory, reportFactory reportsdk.Factory, preparedStore *persistence.RuntimeStore, startupOptions ProjectStartupOptions, artifactEvidence ...deploymentapplication.RuntimeReleaseArtifactEvidence) *Runtime {
	if ctx == nil {
		panic("bootstrap.NewWithExtensions requires a non-nil lifecycle context")
	}
	if projectExtensions == nil || !projectExtensions.Frozen() {
		panic("bootstrap.NewWithExtensions requires a frozen project extension registry")
	}
	if connectorProviders == nil || !connectorProviders.Frozen() {
		panic("bootstrap.NewWithExtensions requires a frozen connector provider registry")
	}
	if identityBinding == nil {
		panic("bootstrap.NewWithExtensions requires an Identity SDK Binding")
	}
	mustCompleteRuntimeStartup(foundationModules.Validate())
	cfg = normalizeRuntimeConfig(cfg)
	mustCompleteRuntimeStartup(principalmodel.ConfigureInstallationWorkspaceID(cfg.IdentityWorkspaceID))
	mustCompleteRuntimeStartup(cfg.ValidateSecurity())
	if strings.TrimSpace(startupOptions.ProjectModel.ContentHash) == "" {
		panic("bootstrap Runtime requires a validated project model")
	}
	projectModel := startupOptions.ProjectModel
	projectDefinitions := projectExtensions.ProjectDefinitions()
	handlerDescriptors := projectExtensions.BusinessHandlerDescriptors()
	schemaCapabilities := ProjectSchemaCapabilities(projectModel, projectExtensions)
	schemaCapabilities.ReleaseCoordination = cfg.RuntimeReplicaCount > 1
	mustCompleteRuntimeStartup(validateSelectedCapabilityFactories(projectDefinitions, handlerDescriptors, notificationFactory, reportFactory, integrationFactory, len(connectorProviders.Descriptors()) != 0, schedulerFactory, agentFactory))
	var err error
	store := preparedStore
	if store == nil {
		store, err = prepareRuntimeStore(ctx, cfg, schemaCapabilities)
		mustCompleteRuntimeStartup(err)
	}
	if startupOptions.BlobStore == nil {
		uploadDirectory := cfg.UploadDir
		if uploadDirectory == "" {
			uploadDirectory = "../data/uploads"
		}
		startupOptions.BlobStore, err = blobstore.NewLocalStore(uploadDirectory)
		mustCompleteRuntimeStartup(err)
	}
	workerDependencies, err := newRuntimeWorkerDependencies(cfg.RuntimeInstanceID)
	mustCompleteRuntimeStartup(err)
	var releaseCohort *deploymentapplication.DeploymentRuntimeReleaseCohortApplicationService
	releaseAdmission := &deploymentapplication.RuntimeReleaseAdmission{}
	var releaseLease deploymentmodel.RuntimeReleaseCohortLease
	startupOwnsStore := preparedStore == nil
	defer func() {
		if !startupOwnsStore {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.HTTPShutdownTimeout)
		defer cancel()
		if releaseCohort != nil {
			_ = releaseCohort.Leave(cleanupCtx, releaseLease)
		}
		_ = store.Close()
	}()
	var bootstrapReleaseHeartbeat *runtimeReleaseBootstrapHeartbeat
	if schemaCapabilities.ReleaseCoordination {
		releaseCohort = deploymentapplication.NewDeploymentRuntimeReleaseCohortApplicationService(deploymentpersistence.NewRuntimeReleaseCohortStore(store))
		releaseLease, err = releaseCohort.Join(ctx, workerDependencies.WorkerID.String(), releaseIdentity, workerDependencies.Clock.Now())
		mustCompleteRuntimeStartup(err)
		bootstrapReleaseHeartbeat = startRuntimeReleaseBootstrapHeartbeat(ctx, releaseCohort.HeartbeatInterval(), releaseLease, func(heartbeatCtx context.Context, lease deploymentmodel.RuntimeReleaseCohortLease) (deploymentmodel.RuntimeReleaseCohortLease, error) {
			return releaseCohort.Heartbeat(heartbeatCtx, lease, workerDependencies.Clock.Now())
		})
	}
	defer func() {
		if bootstrapReleaseHeartbeat == nil {
			return
		}
		latestLease, _ := bootstrapReleaseHeartbeat.Stop()
		if latestLease.InstanceID != "" {
			releaseLease = latestLease
		}
	}()
	metadataBinding, err := foundationModules.Metadata.OpenModule(ctx, metadatasdk.ApplicationRef{InstallationID: projectModel.ProjectKey}, runtimeMetadataModuleHost{store: store})
	mustCompleteRuntimeStartup(err)
	mustCompleteRuntimeStartup(metadataBinding.Descriptor().Validate())
	mustCompleteRuntimeStartup(store.BindMetadata(metadataBinding))
	reportPersistenceHost := runtimeReportModuleHost{store: store}
	var reportBinding reportsdk.Binding
	if reportFactory != nil {
		reportBinding, err = reportFactory.Open(ctx, reportsdk.ApplicationRef{RuntimeID: cfg.RuntimeInstanceID}, reportPersistenceHost)
		mustCompleteRuntimeStartup(err)
		if reportBinding == nil {
			mustCompleteRuntimeStartup(errors.New("Report SDK Factory returned no Binding"))
		}
		mustCompleteRuntimeStartup(reportBinding.Descriptor().Validate())
		if reportBinding.Snapshots() == nil {
			mustCompleteRuntimeStartup(errors.New("Report Binding returned no snapshot repository"))
		}
	}
	integrationTriggers := &runtimeIntegrationTriggerRelay{}
	integrationOwner, err := openRuntimeIntegration(ctx, integrationsdk.ApplicationRef{RuntimeID: cfg.RuntimeInstanceID}, integrationFactory, runtimeIntegrationModuleHost{store: store, providers: connectorProviders, triggers: integrationTriggers})
	mustCompleteRuntimeStartup(err)
	mustCompleteRuntimeStartup(bindIdentitySecurityChallengeDelivery(identityBinding, integrationOwner.Operations))
	projectIntegrations, err := projectIntegrationSchema(ctx, integrationOwner.Catalog, projectDefinitions.IntegrationMappings)
	mustCompleteRuntimeStartup(err)
	if reportBinding != nil {
		mustCompleteRuntimeStartup(SynchronizeReportDefinitions(ctx, reportBinding, startupOptions.ProjectModel.ProjectKey, startupOptions.ProjectModel.ContentHash, projectDefinitions.Reports))
	}
	metadataStore, err := initializeRuntimeProjectModel(ctx, store, projectModel)
	mustCompleteRuntimeStartup(err)
	mustCompleteRuntimeStartup(generateDevelopmentData(ctx, cfg, store, projectModel, startupOptions.DevelopmentData))
	artifactContent := blobstore.LifecycleContentStore{Blobs: startupOptions.BlobStore}
	auditBinding, err := foundationModules.Audit.OpenModule(ctx, auditsdk.ApplicationRef{InstallationID: projectModel.ProjectKey}, runtimeauditmodule.NewHost(store, artifactContent, artifactContent))
	mustCompleteRuntimeStartup(err)
	mustCompleteRuntimeStartup(auditBinding.Descriptor().Validate())
	mustCompleteRuntimeStartup(store.BindAudit(auditBinding))
	runtimeAuditRepository := runtimeauditmodule.NewAuditStore(auditBinding)
	runtimeAudit := auditapplication.NewAuditApplicationService(runtimeAuditRepository)
	identityProjection := identityBinding.Projection()
	identityPrincipals := identityBinding.Principals()
	if identityProjection == nil || identityPrincipals == nil {
		mustCompleteRuntimeStartup(errors.New("Identity Binding returned incomplete Runtime ports"))
	}
	var identityHandlerDeliveryBinder identitysdk.HandlerDeliveryUnitOfWorkBinder
	if embedded, ok := identityBinding.(identitysdk.EmbeddedHandlerDeliveryBinding); ok {
		identityHandlerDeliveryBinder = embedded.HandlerDeliveryUnitOfWorkBinder()
	}
	var organizationUnitDeliveryBinder organizationunit.UnitOfWorkBinder
	if embedded, ok := identityBinding.(organizationunit.EmbeddedBinding); ok {
		organizationUnitDeliveryBinder = embedded.OrganizationUnitDeliveryUnitOfWorkBinder()
	}
	var storeOrganizationDeliveryBinder identitysdk.StoreOrganizationDeliveryUnitOfWorkBinder
	if embedded, ok := identityBinding.(identitysdk.EmbeddedStoreOrganizationDeliveryBinding); ok {
		storeOrganizationDeliveryBinder = embedded.StoreOrganizationDeliveryUnitOfWorkBinder()
	}
	var workspaceIdentityUsageBinder identitysdk.WorkspaceIdentityUsageUnitOfWorkBinder
	if embedded, ok := identityBinding.(identitysdk.EmbeddedWorkspaceIdentityUsageBinding); ok {
		workspaceIdentityUsageBinder = embedded.WorkspaceIdentityUsageUnitOfWorkBinder()
	}
	identityDataExchangeKey, identityDataExchangeImport, identityDataExchangeExport := identityDataExchangeProviders(identityBinding)
	expectedSchemaRevision := ""
	if releaseIdentity.Coordinated() {
		scope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "establish project Runtime schema identity")
		expectedSchemaRevision, err = metadataStore.SnapshotRevision(ctx, scope)
		mustCompleteRuntimeStartup(err)
		mustCompleteRuntimeStartup(requireRuntimeSchemaRevision(expectedSchemaRevision))
	}
	releaseIntegrity := deploymentapplication.NewRuntimeReleaseIntegrity(
		releaseIdentity, firstRuntimeReleaseArtifactEvidence(artifactEvidence), expectedSchemaRevision,
		func(checkCtx context.Context) (string, error) {
			scope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "verify project Runtime schema identity")
			return metadataStore.SnapshotRevision(checkCtx, scope)
		},
		projectExtensions, connectorProviders,
	)
	if integrationOwner.Requirements != nil {
		mustCompleteRuntimeStartup(integrationOwner.Requirements.SynchronizeConnections(ctx, nil))
		mustCompleteRuntimeStartup(integrationOwner.Requirements.SynchronizeEventMappings(ctx, projectDefinitions.IntegrationMappings))
	}
	templateID := projectModel.ProjectKey
	reportNotificationActions := &runtimeNotificationActionAuthorizerBinding{}
	automationNotificationActions := &runtimeNotificationActionAuthorizerBinding{}
	projectRecordNotificationActions := &runtimeNotificationResolvedActionAuthorizerBinding{}
	recordExportNotificationActions := &runtimeNotificationResolvedActionAuthorizerBinding{}
	var templateRenderer composition.NotificationRenderer
	var notificationHTTP *notificationfacade.NotificationApplicationService
	var notificationCompiler runtimeNotificationCompiler
	var notificationPublisher notificationIntentPublisher
	var sdkDeliveryGateway *notificationSDKDeliveryGateway
	var notificationBinding notificationsdk.Binding
	var notificationWorkers notificationsdk.LocalWorkers
	var notificationRelay *notificationpublication.Relay
	var notificationSubjectLifecycle lifecyclecontract.SubjectExecutionHandler
	var notificationRetention lifecyclecontract.OwnerLifecycleExecutor
	notificationArchives := &notificationSDKRetentionArchiveStore{}
	if notificationFactory != nil {
		runtimeNotificationEventTypes, eventTypesErr := NotificationRuntimeEventTypes(projectDefinitions.NotificationEventTypes, localization.SupportedLocales(), localization.DefaultLocale, localization.Lookup)
		mustCompleteRuntimeStartup(eventTypesErr)
		workflowNotificationTasks := workflowpersistence.NewWorkflowProcessStore(store)
		notificationActionAuthorizers := notificationfacade.NewActionAuthorizerRegistry()
		if reportBinding != nil {
			notificationActionAuthorizers.Register("report", reportNotificationActions.Authorize)
		}
		notificationActionAuthorizers.Register("automation_rule", automationNotificationActions.Authorize)
		notificationActionAuthorizers.RegisterResolved("project_record", projectRecordNotificationActions.Authorize)
		notificationActionAuthorizers.RegisterResolved("record_export", recordExportNotificationActions.Authorize)
		notificationActionAuthorizers.Register("workflow_task", newWorkflowTaskNotificationActionAuthorizer(workflowNotificationTasks.GetTask))
		notificationActionAuthorizers.Register("scheduler_job", newSchedulerNotificationActionAuthorizer(metadataBinding.Definitions()))
		notificationActionAuthorizers.Freeze()
		catalog, catalogErr := notificationSDKCatalog(valueOrDefault(startupOptions.ProjectModel.DefaultLocale, cfg.AppLocale), projectDefinitions.NotificationTemplates, runtimeNotificationEventTypes, projectDefinitions.NotificationRules)
		mustCompleteRuntimeStartup(catalogErr)
		sdkDeliveryGateway = &notificationSDKDeliveryGateway{repository: publicationhandoffpersistence.NewPublicationStore(store), productName: cfg.EffectiveProductBrandName()}
		notificationScope, scopeErr := resolveNotificationWorkspaceScope(cfg)
		mustCompleteRuntimeStartup(scopeErr)
		application := notificationScope.applicationRef()
		if moduleFactory, ok := notificationFactory.(modulehost.Factory); ok {
			host := notificationSDKModuleHost{
				store: store, identity: identityBinding, clock: workerDependencies.Clock, workerID: workerDependencies.WorkerID.String(), catalog: catalog,
				projection: identityProjection, workflow: workflowNotificationTasks.GetTask, delivery: sdkDeliveryGateway,
				metrics: notificationSDKDeliveryMetrics{operations: integrationOwner.Operations, clock: workerDependencies.Clock}, validator: modulehost.DefaultProviderTemplateValidator{}, archives: notificationArchives,
			}
			notificationBinding, err = moduleFactory.OpenModule(ctx, application, host)
		} else {
			notificationBinding, err = notificationFactory.Open(ctx, application)
		}
		mustCompleteRuntimeStartup(err)
		if notificationBinding == nil {
			mustCompleteRuntimeStartup(errors.New("Notification SDK Factory returned no Binding"))
		}
		if notificationBinding.Descriptor().Mode == notificationsdk.DeploymentModeSaaS && identityBinding.Descriptor().Mode != identitysdk.DeploymentModeSaaS {
			mustCompleteRuntimeStartup(errors.New("Notification SaaS requires Identity SaaS"))
		}
		if notificationBinding.Descriptor().Mode == notificationsdk.DeploymentModeModule {
			compiler, compilerErr := notificationfacade.NewModuleCompiler(notificationBinding)
			mustCompleteRuntimeStartup(compilerErr)
			mustCompleteRuntimeStartup(store.BindNotificationTransactions(compiler.Transactions()))
			notificationCompiler = compiler
			var found bool
			notificationWorkers, found = notificationBinding.LocalWorkers()
			if !found || notificationWorkers == nil {
				mustCompleteRuntimeStartup(errors.New("Notification Module Binding returned no local workers"))
			}
		} else if notificationBinding.Descriptor().Mode == notificationsdk.DeploymentModeSaaS {
			mustCompleteRuntimeStartup(store.BindNotificationSaaSPublications(notificationScope.publicationScope()))
			notificationCompiler = notificationfacade.SaaSCompiler{}
			notificationRelay, err = notificationpublication.NewRelay(notificationpublication.NewPublicationOutboxStore(store), notificationBinding.Publisher(), workerDependencies.WorkerID.String(), workerDependencies.Clock)
			mustCompleteRuntimeStartup(err)
		} else {
			mustCompleteRuntimeStartup(fmt.Errorf("unsupported Notification deployment mode %q", notificationBinding.Descriptor().Mode))
		}
		facade, facadeErr := notificationfacade.NewNotificationApplicationService(notificationBinding, notificationActionAuthorizers.Authorize)
		mustCompleteRuntimeStartup(facadeErr)
		notificationHTTP = facade
		notificationPublisher = facade.PublishInboxIntent
		systemTemplateBinding, ok := notificationBinding.(notificationsdk.SystemTemplateBinding)
		if !ok || systemTemplateBinding.SystemTemplates() == nil {
			mustCompleteRuntimeStartup(errors.New("Notification Binding returned no system template restoration port"))
		}
		templateCatalog := sdkNotificationTemplateCatalog{system: systemTemplateBinding.SystemTemplates()}
		err = templateCatalog.SyncPublished(ctx, principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "synchronize project notification templates"), projectDefinitions.NotificationTemplates)
		mustCompleteRuntimeStartup(err)
		systemSubjectBinding, ok := notificationBinding.(notificationsdk.SystemSubjectBinding)
		if !ok || systemSubjectBinding.SystemSubjects() == nil {
			mustCompleteRuntimeStartup(errors.New("Notification Binding returned no system subject lifecycle port"))
		}
		systemRetentionBinding, ok := notificationBinding.(notificationsdk.SystemRetentionBinding)
		if !ok || systemRetentionBinding.SystemRetention() == nil {
			mustCompleteRuntimeStartup(errors.New("Notification Binding returned no system retention port"))
		}
		notificationSubjectLifecycle = notificationSystemSubjectLifecycle{subjects: systemSubjectBinding.SystemSubjects()}
		notificationRetention = notificationSystemRetention{retention: systemRetentionBinding.SystemRetention()}
	}
	sharedRateLimiter, err := openSharedRateLimiter(ctx, cfg, store)
	mustCompleteRuntimeStartup(err)
	rateLimiterTransferred := false
	defer func() {
		if !rateLimiterTransferred {
			if closer, ok := sharedRateLimiter.(interface{ Close() error }); ok {
				_ = closer.Close()
			}
		}
	}()
	principalCache, err := principalcache.Open(ctx, cfg)
	mustCompleteRuntimeStartup(err)
	principalCacheTransferred := false
	defer func() {
		if !principalCacheTransferred {
			if closer, ok := principalCache.(interface{ Close() error }); ok {
				_ = closer.Close()
			}
		}
	}()
	workflowNotificationScope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "compile workflow notification intent")
	startupCallbacks := runtimeStartupCallbacks{records: nil, notificationManagement: notificationCompiler, workflowNotificationScope: workflowNotificationScope}
	var compileNotification func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	if notificationCompiler != nil {
		compileNotification = startupCallbacks.CompileNotification
	}
	var agentBinding agentsdk.Binding
	agentBinding, err = openProjectAgentBinding(ctx, cfg.RuntimeInstanceID, store, artifactContent, artifactContent, agentFactory, projectDefinitions)
	mustCompleteRuntimeStartup(err)
	mustCompleteRuntimeStartup(synchronizeAgentDefinitions(ctx, agentBinding, startupOptions.ProjectModel.ProjectKey, startupOptions.ProjectModel.ContentHash, projectDefinitions))
	serviceAssembly, err := assembleRuntimeServices(ctx, cfg, projectModel, projectDefinitions, projectIntegrations, templateRenderer, store, identityProjection, identityPrincipals, runtimeAudit, workerDependencies, runtimeExtensionRegistries{
		projectExtensions: projectExtensions, connectorProviders: connectorProviders,
		notificationCompiler:            compileNotification,
		taskNotificationCommitter:       workflowpersistence.NewWorkflowTaskNotificationStore(store),
		notificationPublisher:           notificationPublisher,
		integrationOwnerDelivery:        integrationOwner.Delivery,
		integrationOwnerCatalog:         integrationOwner.Catalog,
		integrationOwnerManagement:      integrationOwner.Management,
		integrationOwnerOperations:      integrationOwner.Operations,
		integrationOwnerSubjects:        integrationOwner.Subjects,
		integrationSubjectPersistence:   integrationOwner.SubjectPersistence,
		dataExchangeProviderKey:         identityDataExchangeKey,
		dataExchangeImportProvider:      identityDataExchangeImport,
		dataExchangeExportProvider:      identityDataExchangeExport,
		integrationMode:                 integrationDeploymentMode(integrationOwner.Binding),
		identitySubjectLifecycle:        identitySystemSubjectLifecycle{binding: identityBinding},
		identityBinding:                 identityBinding,
		notificationSubjectLifecycle:    notificationSubjectLifecycle,
		notificationRetention:           notificationRetention,
		notificationArchives:            notificationArchives,
		auditRepository:                 runtimeAuditRepository,
		auditSubjectLifecycle:           runtimeauditmodule.NewSubjectLifecycle(auditBinding),
		auditBinding:                    auditBinding,
		lifecycleFactory:                foundationModules.Lifecycle,
		dataExchangeFactory:             dataExchangeFactory,
		agentBinding:                    agentBinding,
		reportBinding:                   reportBinding,
		reportAnalysisTables:            startupOptions.AnalysisTableSource,
		identityHandlerDeliveryBinder:   identityHandlerDeliveryBinder,
		organizationUnitDeliveryBinder:  organizationUnitDeliveryBinder,
		storeOrganizationDeliveryBinder: storeOrganizationDeliveryBinder,
		workspaceIdentityUsageBinder:    workspaceIdentityUsageBinder,
		blobStore:                       startupOptions.BlobStore,
		fileScanner:                     startupOptions.FileScanner,
	})
	mustCompleteRuntimeStartup(err)
	records, recordRepository := serviceAssembly.services, serviceAssembly.records
	mustCompleteRuntimeStartup(records.Applications().Workflows.ConfigureWorkflowWorkloadIdentity(identityBinding, identitysdk.ApplicationScope{
		WorkspaceID: identitysdk.WorkspaceID(strings.TrimSpace(cfg.IdentityWorkspaceID)), ApplicationKey: identitysdk.ApplicationKey(strings.TrimSpace(cfg.IdentityAudience)),
	}))

	auditHostBinder, ok := auditBinding.(auditsdk.ApplicationHostBinder)
	if !ok {
		mustCompleteRuntimeStartup(errors.New("Audit Binding does not accept application host capabilities"))
	}
	mustCompleteRuntimeStartup(auditHostBinder.BindApplicationHost(newRuntimeAuditApplicationHost(
		records.Applications().Records, recordRepository, records.Schema, []byte(cfg.AuditExportTokenKey),
	)))
	auditHTTP, ok := auditBinding.(modulehttp.Provider)
	if !ok || !auditBinding.Descriptor().Capabilities.HTTPAdapter {
		mustCompleteRuntimeStartup(errors.New("Audit Binding does not declare its product HTTP adapter capability"))
	}
	auditAdapters := auditHTTP.HTTPAdapters()
	if len(auditAdapters) != 1 {
		mustCompleteRuntimeStartup(errors.New("Audit Binding must return exactly one product HTTP adapter"))
	}
	auditAdapter := auditAdapters[0]
	if auditAdapter == nil || auditAdapter.Owner() != "audit" || auditAdapter.Name() != "product" {
		mustCompleteRuntimeStartup(errors.New("Audit Binding returned an unexpected product HTTP adapter"))
	}
	mustCompleteRuntimeStartup(modulehttp.ValidateAdapter(auditAdapter))
	if reportBinding != nil {
		reportHostBinder, ok := reportBinding.(reportsdk.ApplicationHostBinder)
		if !ok {
			mustCompleteRuntimeStartup(errors.New("Report Binding does not accept application host capabilities"))
		}
		mustCompleteRuntimeStartup(reportHostBinder.BindApplicationHost(runtimeReportApplicationHost{
			runtimeReportModuleHost: reportPersistenceHost,
			ports:                   records.ReportModuleApplicationPorts(),
			cursorKey:               []byte(cfg.AuditExportTokenKey),
		}))
		mustCompleteRuntimeStartup(records.BindReportApplication(reportBinding))
	}
	// Bind Agent only after its business owner ports, including Report, are ready.
	mustCompleteRuntimeStartup(transportbootstrap.BindAgentApplicationHost(transportbootstrap.AgentApplicationHostDependencies{
		RuntimeID:   cfg.RuntimeInstanceID,
		Application: identitysdk.ApplicationScope{WorkspaceID: identitysdk.WorkspaceID(cfg.IdentityWorkspaceID), ApplicationKey: identitysdk.ApplicationKey(cfg.IdentityAudience)},
		Binding:     agentBinding, Integration: integrationOwner.Binding, Records: records, Principals: identityPrincipals,
		RateLimiter: sharedRateLimiter, IntegrationSecretKey: cfg.IntegrationSecretKey,
		IdentityIssuer:            identityBinding.Descriptor().Issuer,
		NotificationEvents:        records.NotificationEventPublisher(),
		ConversationCodeRuntime:   startupOptions.ConversationCodeRuntime,
		ConversationCodingRuntime: startupOptions.ConversationCodingRuntime,
		ConversationToolsFactory:  startupOptions.ConversationToolsFactory,
	}))
	if agentBinding != nil && agentBinding.Descriptor().HasCapability(agentsdk.CapabilityScheduledConversationTask) {
		conversations, ok := agentBinding.(agentsdk.ConversationBinding)
		if !ok || conversations.Conversations() == nil {
			mustCompleteRuntimeStartup(errors.New("Agent Binding advertises scheduled conversation tasks without a conversation binding"))
		}
		tasks, ok := conversations.Conversations().(agentsdk.ScheduledConversationTaskService)
		if !ok || tasks == nil {
			mustCompleteRuntimeStartup(errors.New("Agent Binding advertises scheduled conversation tasks without the task service"))
		}
		mustCompleteRuntimeStartup(records.BindAgentScheduledTasks(tasks))
	}
	if agentBinding != nil && agentBinding.Descriptor().HasCapability(agentsdk.CapabilityBusinessEventConversationTask) {
		conversations, ok := agentBinding.(agentsdk.ConversationBinding)
		if !ok || conversations.Conversations() == nil {
			mustCompleteRuntimeStartup(errors.New("Agent Binding advertises business-event conversation tasks without a conversation binding"))
		}
		events, ok := conversations.Conversations().(agentsdk.BusinessEventConversationTaskService)
		if !ok || events == nil {
			mustCompleteRuntimeStartup(errors.New("Agent Binding advertises business-event conversation tasks without the task service"))
		}
		mustCompleteRuntimeStartup(records.BindAgentBusinessEvents(events))
	}
	if integrationOwner.Binding != nil {
		integrationTriggers.Bind(newRuntimeIntegrationTriggerSink(records, identityPrincipals))
	}
	var monitoringBinding monitoringsdk.Binding
	if monitoringFactory != nil {
		application := monitoringsdk.ApplicationRef{RuntimeID: cfg.RuntimeInstanceID}
		monitoringBinding, err = openMonitoringBinding(ctx, monitoringFactory, application, newMonitoringModuleHost(records.Applications().RuntimeStatus, projectModel.ProjectKey, projectModel.ContentHash))
		mustCompleteRuntimeStartup(err)
	}
	var schedulerBinding schedulersdk.Binding
	if schedulerFactory != nil {
		application := schedulersdk.ApplicationRef{RuntimeID: cfg.RuntimeInstanceID}
		host := composition.NewSchedulerSDKModuleHost(records.SchedulerDefinitionSource(), records.Applications().TargetExecutions, records.Applications().PublicationHandoff, nil, store, workerDependencies.WorkerID.String(), projectDefinitions.Schedules, projectDefinitions.BusinessCalendars)
		if moduleFactory, ok := schedulerFactory.(schedulermodulehost.Factory); ok {
			schedulerBinding, err = moduleFactory.OpenModule(ctx, application, host)
		} else if saasFactory, ok := schedulerFactory.(schedulersaashost.Factory); ok {
			schedulerBinding, err = saasFactory.OpenSaaS(ctx, application, host)
		} else {
			schedulerBinding, err = schedulerFactory.Open(ctx, application)
		}
		mustCompleteRuntimeStartup(err)
		if schedulerBinding == nil {
			mustCompleteRuntimeStartup(errors.New("Scheduler SDK Factory returned no Binding"))
		}
		mustCompleteRuntimeStartup(schedulerBinding.Descriptor().Validate())
		mustCompleteRuntimeStartup(schedulerBinding.Reconcile(ctx))
	}
	moduleBindings := newRuntimeModuleBindingInventory(
		notificationBinding, integrationOwner.Binding, schedulerBinding, monitoringBinding,
		serviceAssembly.dataExchangeBinding, agentBinding, serviceAssembly.lifecycleBinding,
		auditBinding, metadataBinding, reportBinding,
	)
	authorizationModuleActions, err := moduleBindings.AuthorizationActions()
	mustCompleteRuntimeStartup(err)
	if identityActions, embedded := identityBinding.(actioncontract.Provider); embedded {
		identityAuthorizationActions, actionsErr := identityActions.AuthorizationActions()
		mustCompleteRuntimeStartup(actionsErr)
		authorizationModuleActions = append(authorizationModuleActions, identityAuthorizationActions...)
	} else if identityHTTP, embedded := identityBinding.(modulehttp.Provider); embedded {
		identityAuthorizationActions, actionsErr := modulehttp.AuthorizationActions(identityHTTP)
		mustCompleteRuntimeStartup(actionsErr)
		authorizationModuleActions = append(authorizationModuleActions, identityAuthorizationActions...)
	}
	workspaceRolePolicy := workspaceprovisionpersistence.WorkspaceBootstrapRolePolicyEvidence{}
	initialWorkspaceAdministratorRole := projectModel.InitialWorkspaceAdministratorRole
	for _, role := range projectModel.Roles {
		if !role.ProvisionToWorkspaces || identityBinding.Descriptor().Mode == identitysdk.DeploymentModeExternal {
			continue
		}
		bootstrapRoleCatalog, catalogErr := RuntimeWorkspaceBootstrapRoleCatalog(records.Schema().Objects, projectModel.Roles, initialWorkspaceAdministratorRole, cfg.IdentityAudience, handlerDescriptors...)
		mustCompleteRuntimeStartup(catalogErr)
		workspaceRolePolicy, catalogErr = workspaceprovisionpersistence.NewWorkspaceBootstrapRolePolicyEvidence(bootstrapRoleCatalog)
		mustCompleteRuntimeStartup(catalogErr)
		initialWorkspaceAdministratorRole = bootstrapRoleCatalog.InitialWorkspaceAdministratorRoleKey
		break
	}
	authorizationRegistry, err := reconcileRuntimeIdentityAuthorization(ctx, identityBinding, records.Schema(), authorizationModuleActions, nil, projectModel.Roles, cfg.IdentityWorkspaceID, cfg.IdentityAudience, cfg.IdentityRedirectURLs, schemaCapabilities)
	mustCompleteRuntimeStartup(err)
	authorizationRegistrySnapshot := &runtimeAuthorizationRegistrySnapshot{}
	authorizationRegistrySnapshot.Store(authorizationRegistry)
	if binder, embedded := identityBinding.(identitysdk.PermissionUsageProviderBinder); embedded {
		mustCompleteRuntimeStartup(binder.BindPermissionUsageProvider(authorizationRegistrySnapshot))
	}
	mustCompleteRuntimeStartup(publishRuntimeProjectRoles(ctx, identityBinding, records.Schema().Objects, projectModel.Roles, cfg.IdentityWorkspaceID, cfg.IdentityAudience, cfg.InstallationAdministratorBootstrapEnabled, handlerDescriptors...))
	mustCompleteRuntimeStartup(publishRuntimeProjectProfileExtensions(ctx, identityBinding, records.Schema().IdentityProfileExtensions))
	startupCallbacks.records = records
	notificationWakeup := func(message publicationmodel.Message) {
		records.Applications().PublicationHandoff.Wake(ctx, publicationhandoff.Locator{WorkspaceID: message.WorkspaceID, MessageID: message.ID})
	}
	if sdkDeliveryGateway != nil {
		sdkDeliveryGateway.BindWakeup(notificationWakeup)
	}
	reportNotificationActions.authorize = newReportNotificationActionAuthorizer(startupCallbacks.ReportsForPrincipal)
	automationNotificationActions.authorize = newAutomationNotificationActionAuthorizer(records.Applications().Automations.AutomationRule)
	projectRecordNotificationActions.authorize = newProjectRecordNotificationActionAuthorizer(records.Applications().Records.GetRecordForAction)
	recordExportNotificationActions.authorize = newRecordExportNotificationActionAuthorizer(serviceAssembly.dataExchangeBinding)
	mustCompleteRuntimeStartup(validateRuntimeActionReadiness(records.Applications().Actions))
	schedulerWorkloads, err := composition.SchedulerBusinessActionWorkloadBindings(projectDefinitions.Schedules)
	mustCompleteRuntimeStartup(err)
	restoreSchedulerWorkloads, err := records.Applications().Workflows.ReplaceManagedWorkloadBindings(schedulerWorkloads)
	mustCompleteRuntimeStartup(err)
	// Workflow run_as references are validated against Identity's live role
	// catalog. Publish the complete project roles and their permissions above
	// before activating workflows, including on an existing workspace upgrade.
	workflowScope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "initialize published workflow definitions")
	var workflowInitializationErr error
	if schemaCapabilities.Workflow {
		workflowInitializationErr = records.Applications().Workflows.InitializePublishedWorkflowDefinitions(ctx, projectDefinitions.Workflows, workflowScope)
	} else {
		workflowInitializationErr = records.Applications().Workflows.SynchronizeManagedWorkloadBindings(ctx)
	}
	if workflowInitializationErr != nil {
		if restoreSchedulerWorkloads != nil {
			restoreSchedulerWorkloads()
		}
		mustCompleteRuntimeStartup(fmt.Errorf("initialize workflow definitions: %w", workflowInitializationErr))
	}
	records.Applications().Actions.ReplaceDefinitions(records.Schema().Actions)
	if bootstrapReleaseHeartbeat != nil {
		releaseLease, err = bootstrapReleaseHeartbeat.Stop()
		mustCompleteRuntimeStartup(err)
	}
	runtime := constructRuntime(runtimeConstructionInput{
		config:                   cfg,
		templateID:               templateID,
		store:                    store,
		applicationServices:      records,
		authorizationActions:     authorizationRegistrySnapshot.Load,
		moduleBindings:           moduleBindings,
		identityBinding:          identityBinding,
		identityProjection:       identityProjection,
		identityPrincipals:       identityPrincipals,
		principalCache:           principalCache,
		integrationMode:          integrationDeploymentMode(integrationOwner.Binding),
		integrationBinding:       integrationOwner.Binding,
		integrationWorkers:       integrationOwner.Workers,
		dataExchangeBinding:      serviceAssembly.dataExchangeBinding,
		lifecycleBinding:         serviceAssembly.lifecycleBinding,
		fileScanProcessor:        serviceAssembly.fileScanProcessor,
		blobStore:                serviceAssembly.blobStore,
		publicResources:          serviceAssembly.publicResources,
		projectModel:             startupOptions.ProjectModel,
		schemaCapabilities:       schemaCapabilities,
		workspaceRolePolicy:      workspaceRolePolicy,
		recordRepository:         recordRepository,
		rateLimiter:              sharedRateLimiter,
		notificationHTTP:         notificationHTTP,
		notificationBinding:      notificationBinding,
		monitoringBinding:        monitoringBinding,
		schedulerBinding:         schedulerBinding,
		agentBinding:             agentBinding,
		auditBinding:             auditBinding,
		metadataBinding:          metadataBinding,
		reportBinding:            reportBinding,
		notificationWorkers:      notificationWorkers,
		notificationRelay:        notificationRelay,
		worker:                   serviceAssembly.worker,
		projectExtensions:        projectExtensions,
		projectHTTP:              startupOptions.ProjectHTTP,
		conversationToolsFactory: startupOptions.ConversationToolsFactory,
		connectorProviders:       connectorProviders,
		releaseIdentity:          releaseIdentity,
		releaseCohort:            releaseCohort,
		releaseLease:             releaseLease,
		releaseAdmission:         releaseAdmission,
		releaseIntegrity:         releaseIntegrity,
	})
	runtime.borrowedStore = preparedStore != nil
	startupOwnsStore = false
	rateLimiterTransferred = true
	principalCacheTransferred = true
	return BindHTTP(ctx, runtime)
}

type identityDataExchangeProviderBinding interface {
	IdentityDataExchangeProviders() (string, dataexchangemodulehost.ImportProvider, dataexchangemodulehost.ExportProvider)
}

func identityDataExchangeProviders(binding identitysdk.Binding) (string, dataexchangemodulehost.ImportProvider, dataexchangemodulehost.ExportProvider) {
	providerBinding, ok := binding.(identityDataExchangeProviderBinding)
	if !ok {
		return "", nil, nil
	}
	return providerBinding.IdentityDataExchangeProviders()
}

type runtimeStartupCallbacks struct {
	records                   *composition.RuntimeServices
	notificationManagement    runtimeNotificationCompiler
	workflowNotificationScope principalmodel.SystemScope
}

func (c *runtimeStartupCallbacks) CompileNotification(intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
	return c.notificationManagement.CompileInboxIntent(intent, c.workflowNotificationScope)
}

func (c *runtimeStartupCallbacks) ReportsForPrincipal(ctx context.Context, principal principalmodel.Principal) []reportmodel.ReportSchema {
	return c.records.SchemaForPrincipal(ctx, principal).Reports
}

func (c *runtimeStartupCallbacks) Objects() []definitionmodel.ObjectSchema {
	return c.records.Schema().Objects
}
func (c *runtimeStartupCallbacks) Extensions() []profilebindingmodel.Binding {
	return c.records.Schema().IdentityProfileExtensions
}

func firstRuntimeReleaseArtifactEvidence(evidence []deploymentapplication.RuntimeReleaseArtifactEvidence) deploymentapplication.RuntimeReleaseArtifactEvidence {
	if len(evidence) == 0 {
		return deploymentapplication.RuntimeReleaseArtifactEvidence{}
	}
	return evidence[0]
}

func validateRuntimeActionReadiness(actions *actionapplication.ActionApplicationService) error {
	if actions == nil {
		return fmt.Errorf("Action Application is required for Runtime readiness")
	}
	validationErrors := actions.CatalogValidationErrors()
	if len(validationErrors) == 0 {
		return nil
	}
	return fmt.Errorf("validate Action Registry/Catalog readiness: %w", errors.Join(validationErrors...))
}
