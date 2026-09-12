package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	auditsdk "github.com/domainry/domainry-audit-sdk"
	auditmoduleimpl "github.com/domainry/domainry-audit/module"
	connector "github.com/domainry/domainry-connector-sdk"
	dataexchangesdk "github.com/domainry/domainry-data-exchange-sdk"
	dataexchangemodulehost "github.com/domainry/domainry-data-exchange-sdk/modulehost"
	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulehttp"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	organizationunit "github.com/domainry/domainry-identity-sdk/organizationunit"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	metadatamodule "github.com/domainry/domainry-metadata/module"
	monitoringsdk "github.com/domainry/domainry-monitoring-sdk"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-notification-sdk/modulehost"
	reportsdk "github.com/domainry/domainry-report-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportmodule "github.com/domainry/domainry-report/module"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	notificationfacade "github.com/domainry/domainry-runtime/runtime/application/notificationfacade"
	publicationhandoff "github.com/domainry/domainry-runtime/runtime/application/publicationhandoff"
	manifestseed "github.com/domainry/domainry-runtime/runtime/application/seed/globalcapability"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	transportbootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap/transport"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	manifestvalidation "github.com/domainry/domainry-runtime/runtime/domain/manifest/validation"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
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

func New(ctx context.Context, cfg config.Config, identityBinding identitysdk.Binding, notificationFactory notificationsdk.Factory, dataExchangeFactory dataexchangesdk.Factory, integrationFactory integrationsdk.Factory) *Runtime {
	businessHandlers := runtimeext.NewBusinessHandlerRegistry()
	businessHandlers.Freeze()
	connectorProviders := emptyConnectorProviderRegistry()
	return NewWithExtensions(ctx, cfg, businessHandlers, connectorProviders, identityBinding, notificationFactory, dataExchangeFactory, integrationFactory)
}

func NewWithScheduler(ctx context.Context, cfg config.Config, identityBinding identitysdk.Binding, notificationFactory notificationsdk.Factory, schedulerFactory schedulersdk.Factory, dataExchangeFactory dataexchangesdk.Factory, integrationFactory integrationsdk.Factory, agent ...agentsdk.Factory) *Runtime {
	businessHandlers := runtimeext.NewBusinessHandlerRegistry()
	businessHandlers.Freeze()
	var agentFactory agentsdk.Factory
	if len(agent) > 0 {
		agentFactory = agent[0]
	}
	return newWithExtensionsUsingAllFactoriesAndStore(ctx, cfg, businessHandlers, emptyConnectorProviderRegistry(), runtimehttp.RuntimeReleaseIdentity{}, identityBinding, notificationFactory, nil, schedulerFactory, dataExchangeFactory, agentFactory, integrationFactory, reportmodule.NewFactory(), nil, ProjectStartupOptions{})
}

func NewWithBusinessHandlers(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, identityBinding identitysdk.Binding, notificationFactory notificationsdk.Factory, dataExchangeFactory dataexchangesdk.Factory, integrationFactory integrationsdk.Factory) *Runtime {
	connectorProviders := emptyConnectorProviderRegistry()
	return NewWithExtensions(ctx, cfg, businessHandlers, connectorProviders, identityBinding, notificationFactory, dataExchangeFactory, integrationFactory)
}

func NewWithBusinessHandlersAndScheduler(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, identityBinding identitysdk.Binding, notificationFactory notificationsdk.Factory, schedulerFactory schedulersdk.Factory, dataExchangeFactory dataexchangesdk.Factory, integrationFactory integrationsdk.Factory, agent ...agentsdk.Factory) *Runtime {
	var agentFactory agentsdk.Factory
	if len(agent) > 0 {
		agentFactory = agent[0]
	}
	return newWithExtensionsUsingAllFactoriesAndStore(ctx, cfg, businessHandlers, emptyConnectorProviderRegistry(), runtimehttp.RuntimeReleaseIdentity{}, identityBinding, notificationFactory, nil, schedulerFactory, dataExchangeFactory, agentFactory, integrationFactory, reportmodule.NewFactory(), nil, ProjectStartupOptions{})
}

func emptyConnectorProviderRegistry() *connector.Registry {
	registry := connector.NewRegistry()
	registry.Freeze()
	return registry
}

func NewWithExtensions(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, identityBinding identitysdk.Binding, notificationFactory notificationsdk.Factory, dataExchangeFactory dataexchangesdk.Factory, integrationFactory integrationsdk.Factory) *Runtime {
	return newWithExtensions(ctx, cfg, businessHandlers, connectorProviders, runtimehttp.RuntimeReleaseIdentity{}, identityBinding, notificationFactory, dataExchangeFactory, integrationFactory)
}

// NewProjectWithIdentity assembles Runtime against one already-opened SDK
// Binding. The generated project host owns Module/SaaS selection, Identity
// lifecycle, and optional Identity HTTP adapters.
func NewProjectWithIdentity(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, artifactEvidence deploymentapplication.RuntimeReleaseArtifactEvidence, binding identitysdk.Binding, notificationFactory notificationsdk.Factory, dataExchangeFactory dataexchangesdk.Factory, integrationFactory integrationsdk.Factory) *Runtime {
	return newWithExtensionsUsingStore(ctx, cfg, businessHandlers, connectorProviders, releaseIdentity, binding, notificationFactory, dataExchangeFactory, integrationFactory, nil, artifactEvidence)
}

func NewProjectWithIdentityAndDatabase(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, artifactEvidence deploymentapplication.RuntimeReleaseArtifactEvidence, binding identitysdk.Binding, notificationFactory notificationsdk.Factory, dataExchangeFactory dataexchangesdk.Factory, integrationFactory integrationsdk.Factory, store *persistence.RuntimeStore) *Runtime {
	return newWithExtensionsUsingStore(ctx, cfg, businessHandlers, connectorProviders, releaseIdentity, binding, notificationFactory, dataExchangeFactory, integrationFactory, store, artifactEvidence)
}

func NewProjectWithFactoriesAndDatabase(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, artifactEvidence deploymentapplication.RuntimeReleaseArtifactEvidence, identity identitysdk.Binding, notification notificationsdk.Factory, dataExchange dataexchangesdk.Factory, integration integrationsdk.Factory, store *persistence.RuntimeStore) *Runtime {
	return newWithExtensionsUsingFactoriesAndStore(ctx, cfg, businessHandlers, connectorProviders, releaseIdentity, identity, notification, dataExchange, integration, store, artifactEvidence)
}

// NewProjectWithAllFactoriesAndDatabase lets a generated project select the
// remaining source-owned module topologies explicitly.
func NewProjectWithAllFactoriesAndDatabase(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, artifactEvidence deploymentapplication.RuntimeReleaseArtifactEvidence, identity identitysdk.Binding, notification notificationsdk.Factory, dataExchange dataexchangesdk.Factory, integration integrationsdk.Factory, store *persistence.RuntimeStore, monitoring ...monitoringsdk.Factory) *Runtime {
	var factory monitoringsdk.Factory
	if len(monitoring) > 0 {
		factory = monitoring[0]
	}
	return newWithExtensionsUsingAllFactoriesAndStore(ctx, cfg, businessHandlers, connectorProviders, releaseIdentity, identity, notification, factory, nil, dataExchange, nil, integration, reportmodule.NewFactory(), store, ProjectStartupOptions{}, artifactEvidence)
}

func NewProjectWithOwnerFactoriesAndDatabase(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, artifactEvidence deploymentapplication.RuntimeReleaseArtifactEvidence, identity identitysdk.Binding, notification notificationsdk.Factory, monitoring monitoringsdk.Factory, scheduler schedulersdk.Factory, dataExchange dataexchangesdk.Factory, integration integrationsdk.Factory, store *persistence.RuntimeStore, agent ...agentsdk.Factory) *Runtime {
	var agentFactory agentsdk.Factory
	if len(agent) > 0 {
		agentFactory = agent[0]
	}
	return newWithExtensionsUsingAllFactoriesAndStore(ctx, cfg, businessHandlers, connectorProviders, releaseIdentity, identity, notification, monitoring, scheduler, dataExchange, agentFactory, integration, reportmodule.NewFactory(), store, ProjectStartupOptions{}, artifactEvidence)
}

func newWithExtensions(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, identityBinding identitysdk.Binding, notificationFactory notificationsdk.Factory, dataExchangeFactory dataexchangesdk.Factory, integrationFactory integrationsdk.Factory, artifactEvidence ...deploymentapplication.RuntimeReleaseArtifactEvidence) *Runtime {
	return newWithExtensionsUsingStore(ctx, cfg, businessHandlers, connectorProviders, releaseIdentity, identityBinding, notificationFactory, dataExchangeFactory, integrationFactory, nil, artifactEvidence...)
}

func newWithExtensionsUsingStore(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, identityBinding identitysdk.Binding, notificationFactory notificationsdk.Factory, dataExchangeFactory dataexchangesdk.Factory, integrationFactory integrationsdk.Factory, preparedStore *persistence.RuntimeStore, artifactEvidence ...deploymentapplication.RuntimeReleaseArtifactEvidence) *Runtime {
	return newWithExtensionsUsingFactoriesAndStore(ctx, cfg, businessHandlers, connectorProviders, releaseIdentity, identityBinding, notificationFactory, dataExchangeFactory, integrationFactory, preparedStore, artifactEvidence...)
}

func newWithExtensionsUsingFactoriesAndStore(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, identityBinding identitysdk.Binding, notificationFactory notificationsdk.Factory, dataExchangeFactory dataexchangesdk.Factory, integrationFactory integrationsdk.Factory, preparedStore *persistence.RuntimeStore, artifactEvidence ...deploymentapplication.RuntimeReleaseArtifactEvidence) *Runtime {
	return newWithExtensionsUsingAllFactoriesAndStore(ctx, cfg, businessHandlers, connectorProviders, releaseIdentity, identityBinding, notificationFactory, nil, nil, dataExchangeFactory, nil, integrationFactory, reportmodule.NewFactory(), preparedStore, ProjectStartupOptions{}, artifactEvidence...)
}

func newWithExtensionsUsingAllFactoriesAndStore(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, identityBinding identitysdk.Binding, notificationFactory notificationsdk.Factory, monitoringFactory monitoringsdk.Factory, schedulerFactory schedulersdk.Factory, dataExchangeFactory dataexchangesdk.Factory, agentFactory agentsdk.Factory, integrationFactory integrationsdk.Factory, reportFactory reportsdk.Factory, preparedStore *persistence.RuntimeStore, startupOptions ProjectStartupOptions, artifactEvidence ...deploymentapplication.RuntimeReleaseArtifactEvidence) *Runtime {
	if ctx == nil {
		panic("bootstrap.NewWithExtensions requires a non-nil lifecycle context")
	}
	if businessHandlers == nil || !businessHandlers.Frozen() {
		panic("bootstrap.NewWithExtensions requires a frozen business handler registry")
	}
	if connectorProviders == nil || !connectorProviders.Frozen() {
		panic("bootstrap.NewWithExtensions requires a frozen connector provider registry")
	}
	if identityBinding == nil {
		panic("bootstrap.NewWithExtensions requires an Identity SDK Binding")
	}
	if notificationFactory == nil {
		panic("bootstrap.NewWithExtensions requires a Notification SDK Factory")
	}
	if dataExchangeFactory == nil {
		panic("bootstrap.NewWithExtensions requires a Data Exchange SDK Factory")
	}
	if integrationFactory == nil {
		panic("bootstrap.NewWithExtensions requires an Integration SDK Factory")
	}
	if reportFactory == nil {
		panic("bootstrap.NewWithExtensions requires a Report SDK Factory")
	}
	cfg = normalizeRuntimeConfig(cfg)
	mustCompleteRuntimeStartup(principalmodel.ConfigureInstallationWorkspaceID(cfg.IdentityWorkspaceID))
	mustCompleteRuntimeStartup(cfg.ValidateSecurity())
	// Connectors-owned definitions arrive through Integration after its Binding opens.
	manifestPreparationConfig := cfg
	manifestPreparationConfig.SkipManifestValidation = true
	seedManifest, err := prepareRuntimeManifest(ctx, manifestPreparationConfig)
	mustCompleteRuntimeStartup(err)
	store := preparedStore
	if store == nil {
		store, err = prepareRuntimeStore(ctx, cfg)
		mustCompleteRuntimeStartup(err)
	}
	workerDependencies, err := newRuntimeWorkerDependencies(cfg.RuntimeInstanceID)
	mustCompleteRuntimeStartup(err)
	releaseCohort := deploymentapplication.NewDeploymentRuntimeReleaseCohortApplicationService(deploymentpersistence.NewRuntimeReleaseCohortStore(store))
	releaseAdmission := &deploymentapplication.RuntimeReleaseAdmission{}
	var releaseLease deploymentmodel.RuntimeReleaseCohortLease
	startupOwnsStore := preparedStore == nil
	defer func() {
		if !startupOwnsStore {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.HTTPShutdownTimeout)
		defer cancel()
		_ = releaseCohort.Leave(cleanupCtx, releaseLease)
		_ = store.Close()
	}()
	releaseLease, err = releaseCohort.Join(ctx, workerDependencies.WorkerID.String(), releaseIdentity, workerDependencies.Clock.Now())
	mustCompleteRuntimeStartup(err)
	metadataBinding, err := metadatamodule.NewFactory().OpenModule(ctx, metadatasdk.ApplicationRef{InstallationID: valueOrDefault(seedManifest.TemplateID, "domainry-runtime")}, runtimeMetadataModuleHost{store: store})
	mustCompleteRuntimeStartup(err)
	mustCompleteRuntimeStartup(metadataBinding.Descriptor().Validate())
	mustCompleteRuntimeStartup(store.BindMetadata(metadataBinding))
	reportPersistenceHost := runtimeReportModuleHost{store: store}
	reportBinding, err := reportFactory.Open(ctx, reportsdk.ApplicationRef{RuntimeID: cfg.RuntimeInstanceID}, reportPersistenceHost)
	mustCompleteRuntimeStartup(err)
	mustCompleteRuntimeStartup(reportBinding.Descriptor().Validate())
	if reportBinding.Snapshots() == nil {
		mustCompleteRuntimeStartup(errors.New("Report Binding returned no snapshot repository"))
	}
	integrationTriggers := &runtimeIntegrationTriggerRelay{}
	integrationOwner, err := openRuntimeIntegration(ctx, integrationsdk.ApplicationRef{RuntimeID: cfg.RuntimeInstanceID}, integrationFactory, runtimeIntegrationModuleHost{store: store, providers: connectorProviders, triggers: integrationTriggers})
	mustCompleteRuntimeStartup(err)
	mustCompleteRuntimeStartup(bindIdentitySecurityChallengeDelivery(identityBinding, integrationOwner.Operations))
	ownerProjectedManifest, validationErr := addIntegrationOwnerValidationCatalog(ctx, seedManifest, integrationOwner.Catalog)
	mustCompleteRuntimeStartup(validationErr)
	if !cfg.SkipManifestValidation && !(cfg.AllowEmptyAuthoringManifest && runtimeManifestHasNoBusinessObjects(seedManifest)) {
		mustCompleteRuntimeStartup(manifestvalidation.ValidateManifest(ownerProjectedManifest))
	}
	mustCompleteRuntimeStartup(SynchronizeReportDefinitions(ctx, reportBinding, seedManifest))
	restoredMetadata, err := restoreRuntimeMetadata(ctx, store, seedManifest, cfg.EffectiveDefinitionUpgradeMode())
	mustCompleteRuntimeStartup(err)
	manifest := restoredMetadata.manifest
	auditBinding, err := auditmoduleimpl.NewFactory(auditmoduleimpl.Options{}).OpenModule(ctx, auditsdk.ApplicationRef{InstallationID: valueOrDefault(manifest.TemplateID, "domainry-runtime")}, runtimeauditmodule.NewHost(store))
	mustCompleteRuntimeStartup(err)
	mustCompleteRuntimeStartup(auditBinding.Descriptor().Validate())
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
		expectedSchemaRevision, err = restoredMetadata.metadataStore.SnapshotRevision(ctx, scope)
		mustCompleteRuntimeStartup(err)
		mustCompleteRuntimeStartup(requireRuntimeSchemaRevision(expectedSchemaRevision))
	}
	releaseIntegrity := deploymentapplication.NewRuntimeReleaseIntegrity(
		releaseIdentity, firstRuntimeReleaseArtifactEvidence(artifactEvidence), expectedSchemaRevision,
		func(checkCtx context.Context) (string, error) {
			scope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "verify project Runtime schema identity")
			return restoredMetadata.metadataStore.SnapshotRevision(checkCtx, scope)
		},
		businessHandlers, connectorProviders,
	)
	mustCompleteRuntimeStartup(integrationOwner.Requirements.SynchronizeConnections(ctx, composition.IntegrationConnectionRequirements(manifest.Integrations.Connections)))
	mustCompleteRuntimeStartup(integrationOwner.Requirements.SynchronizeEventMappings(ctx, composition.IntegrationEventMappingRequirements(manifest.Integrations.EventMappings)))
	mustCompleteRuntimeStartup(manifestseed.SyncRows(ctx, runtimeAuditRepository, workflowpersistence.NewWorkflowWorkerStore(store)))
	templateID := valueOrDefault(manifest.TemplateID, generatedTemplateID)
	runtimeNotificationEventTypes, err := NotificationRuntimeEventTypes(manifest.NotificationEventTypes, localization.SupportedLocales(), localization.DefaultLocale, localization.Lookup)
	mustCompleteRuntimeStartup(err)
	workflowNotificationTasks := workflowpersistence.NewWorkflowProcessStore(store)
	notificationActionAuthorizers := notificationfacade.NewActionAuthorizerRegistry()
	reportNotificationActions := &runtimeNotificationActionAuthorizerBinding{}
	notificationActionAuthorizers.Register("report", reportNotificationActions.Authorize)
	automationNotificationActions := &runtimeNotificationActionAuthorizerBinding{}
	notificationActionAuthorizers.Register("automation_rule", automationNotificationActions.Authorize)
	projectRecordNotificationActions := &runtimeNotificationResolvedActionAuthorizerBinding{}
	notificationActionAuthorizers.RegisterResolved("project_record", projectRecordNotificationActions.Authorize)
	recordExportNotificationActions := &runtimeNotificationResolvedActionAuthorizerBinding{}
	notificationActionAuthorizers.RegisterResolved("record_export", recordExportNotificationActions.Authorize)
	notificationActionAuthorizers.Register("workflow_task", newWorkflowTaskNotificationActionAuthorizer(workflowNotificationTasks.GetTask))
	notificationActionAuthorizers.Register("scheduler_job", newSchedulerNotificationActionAuthorizer(metadataBinding.Definitions()))
	notificationActionAuthorizers.Freeze()
	var templateRenderer composition.NotificationRenderer
	var notificationHTTP *notificationfacade.NotificationApplicationService
	var notificationCompiler runtimeNotificationCompiler
	var notificationPublisher notificationIntentPublisher
	var sdkDeliveryGateway *notificationSDKDeliveryGateway
	var notificationBinding notificationsdk.Binding
	var notificationWorkers notificationsdk.LocalWorkers
	var notificationRelay *notificationpublication.Relay
	{
		catalog, catalogErr := notificationSDKCatalog(valueOrDefault(manifest.DefaultLocale, cfg.AppLocale), manifest, runtimeNotificationEventTypes)
		mustCompleteRuntimeStartup(catalogErr)
		sdkDeliveryGateway = &notificationSDKDeliveryGateway{repository: publicationhandoffpersistence.NewPublicationStore(store), productName: cfg.EffectiveProductBrandName()}
		notificationScope, scopeErr := resolveLegacyNotificationWorkspaceScope(cfg)
		mustCompleteRuntimeStartup(scopeErr)
		application := notificationScope.applicationRef()
		if moduleFactory, ok := notificationFactory.(modulehost.Factory); ok {
			host := notificationSDKModuleHost{
				store: store, identity: identityBinding, clock: workerDependencies.Clock, workerID: workerDependencies.WorkerID.String(), catalog: catalog,
				projection: identityProjection, workflow: workflowNotificationTasks.GetTask, delivery: sdkDeliveryGateway,
				metrics: notificationSDKDeliveryMetrics{operations: integrationOwner.Operations, clock: workerDependencies.Clock}, validator: modulehost.DefaultProviderTemplateValidator{},
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
	}
	systemTemplateBinding, ok := notificationBinding.(notificationsdk.SystemTemplateBinding)
	if !ok || systemTemplateBinding.SystemTemplates() == nil {
		mustCompleteRuntimeStartup(errors.New("Notification Binding returned no system template restoration port"))
	}
	// The second restoration pass reconciles notification templates through the
	// Notification module; the physical definition upgrade already ran above, so
	// its plan is now empty and apply mode only rewrites nothing.
	manifest, err = restoreRuntimeManifest(ctx, sdkNotificationTemplateCatalog{system: systemTemplateBinding.SystemTemplates()}, restoredMetadata.metadataStore, seedManifest, cfg.EffectiveDefinitionUpgradeMode())
	mustCompleteRuntimeStartup(err)
	manifest, err = addIntegrationOwnerValidationCatalog(ctx, manifest, integrationOwner.Catalog)
	mustCompleteRuntimeStartup(err)
	systemSubjectBinding, ok := notificationBinding.(notificationsdk.SystemSubjectBinding)
	if !ok || systemSubjectBinding.SystemSubjects() == nil {
		mustCompleteRuntimeStartup(errors.New("Notification Binding returned no system subject lifecycle port"))
	}
	systemRetentionBinding, ok := notificationBinding.(notificationsdk.SystemRetentionBinding)
	if !ok || systemRetentionBinding.SystemRetention() == nil {
		mustCompleteRuntimeStartup(errors.New("Notification Binding returned no system retention port"))
	}
	manifest.TemplateID = templateID
	manifest.Version = valueOrDefault(manifest.Version, generatedTemplateVersion)
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
	var agentBinding agentsdk.Binding
	agentBinding, err = openManifestAgentBinding(ctx, cfg.RuntimeInstanceID, store, agentFactory, manifest)
	mustCompleteRuntimeStartup(err)
	mustCompleteRuntimeStartup(synchronizeAgentDefinitions(ctx, agentBinding, &manifest))
	serviceAssembly, err := assembleRuntimeServices(ctx, cfg, manifest, templateRenderer, store, identityProjection, identityPrincipals, runtimeAudit, workerDependencies, runtimeExtensionRegistries{
		businessHandlers: businessHandlers, connectorProviders: connectorProviders,
		notificationCompiler:            startupCallbacks.CompileNotification,
		taskNotificationCommitter:       workflowpersistence.NewWorkflowTaskNotificationStore(store),
		notificationPublisher:           notificationPublisher,
		integrationOwnerDelivery:        integrationOwner.Delivery,
		integrationOwnerCatalog:         integrationOwner.Catalog,
		integrationOwnerManagement:      integrationOwner.Management,
		integrationOwnerOperations:      integrationOwner.Operations,
		dataExchangeProviderKey:         identityDataExchangeKey,
		dataExchangeImportProvider:      identityDataExchangeImport,
		dataExchangeExportProvider:      identityDataExchangeExport,
		integrationMode:                 integrationOwner.Binding.Descriptor().Mode,
		notificationSubjectLifecycle:    notificationSystemSubjectLifecycle{subjects: systemSubjectBinding.SystemSubjects()},
		notificationRetention:           notificationSystemRetention{retention: systemRetentionBinding.SystemRetention()},
		auditRepository:                 runtimeAuditRepository,
		auditSubjectLifecycle:           runtimeauditmodule.NewSubjectLifecycle(auditBinding),
		dataExchangeFactory:             dataExchangeFactory,
		agentBinding:                    agentBinding,
		reportBinding:                   reportBinding,
		identityHandlerDeliveryBinder:   identityHandlerDeliveryBinder,
		organizationUnitDeliveryBinder:  organizationUnitDeliveryBinder,
		storeOrganizationDeliveryBinder: storeOrganizationDeliveryBinder,
		workspaceIdentityUsageBinder:    workspaceIdentityUsageBinder,
	})
	mustCompleteRuntimeStartup(err)
	records, recordRepository := serviceAssembly.services, serviceAssembly.records

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
	// Bind Agent only after its business owner ports, including Report, are ready.
	mustCompleteRuntimeStartup(transportbootstrap.BindAgentApplicationHost(transportbootstrap.AgentApplicationHostDependencies{
		RuntimeID:   cfg.RuntimeInstanceID,
		Application: identitysdk.ApplicationScope{WorkspaceID: identitysdk.WorkspaceID(cfg.IdentityWorkspaceID), ApplicationKey: identitysdk.ApplicationKey(cfg.IdentityAudience)},
		Binding:     agentBinding, Records: records, Principals: identityPrincipals,
		RateLimiter: sharedRateLimiter, IntegrationSecretKey: cfg.IntegrationSecretKey,
		IdentityIssuer: identityBinding.Descriptor().Issuer,
	}))
	integrationTriggers.Bind(newRuntimeIntegrationTriggerSink(records, identityPrincipals))
	var monitoringBinding monitoringsdk.Binding
	if monitoringFactory != nil {
		application := monitoringsdk.ApplicationRef{RuntimeID: cfg.RuntimeInstanceID}
		monitoringBinding, err = openMonitoringBinding(ctx, monitoringFactory, application, newMonitoringModuleHost(records.Applications().RuntimeStatus, manifest.TemplateID, manifest.Version))
		mustCompleteRuntimeStartup(err)
	}
	var schedulerBinding schedulersdk.Binding
	if schedulerFactory != nil {
		application := schedulersdk.ApplicationRef{RuntimeID: cfg.RuntimeInstanceID}
		host := composition.NewSchedulerSDKModuleHost(records.SchedulerDefinitionSource(), records.Applications().TargetExecutions, records.Applications().PublicationHandoff, composition.IntegrationConnectionRequirements(manifest.Integrations.Connections), store, workerDependencies.WorkerID.String(), manifest.SchedulerDefinitions)
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
	handlerDescriptors := businessHandlers.Descriptors()
	for _, role := range manifest.Roles {
		if !role.ProvisionToWorkspaces || identityBinding.Descriptor().Mode == identitysdk.DeploymentModeExternal {
			continue
		}
		bootstrapRoleCatalog, catalogErr := RuntimeWorkspaceBootstrapRoleCatalog(records.Schema().Objects, manifest.Roles, manifest.InitialWorkspaceAdministratorRole, cfg.IdentityAudience, handlerDescriptors...)
		mustCompleteRuntimeStartup(catalogErr)
		workspaceRolePolicy, catalogErr = workspaceprovisionpersistence.NewWorkspaceBootstrapRolePolicyEvidence(bootstrapRoleCatalog, startupOptions.ProjectNavigationCatalog)
		mustCompleteRuntimeStartup(catalogErr)
		manifest.InitialWorkspaceAdministratorRole = bootstrapRoleCatalog.InitialWorkspaceAdministratorRoleKey
		break
	}
	authorizationRegistry, err := reconcileRuntimeIdentityAuthorization(ctx, identityBinding, records.Schema(), authorizationModuleActions, nil, manifest.Roles, cfg.IdentityWorkspaceID, cfg.IdentityAudience, cfg.IdentityRedirectURLs)
	mustCompleteRuntimeStartup(err)
	authorizationRegistrySnapshot := &runtimeAuthorizationRegistrySnapshot{}
	authorizationRegistrySnapshot.Store(authorizationRegistry)
	if binder, embedded := identityBinding.(identitysdk.PermissionUsageProviderBinder); embedded {
		mustCompleteRuntimeStartup(binder.BindPermissionUsageProvider(authorizationRegistrySnapshot))
	}
	mustCompleteRuntimeStartup(publishRuntimeProjectRoles(ctx, identityBinding, records.Schema().Objects, manifest.Roles, cfg.IdentityWorkspaceID, cfg.IdentityAudience, handlerDescriptors...))
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
	// Workflow run_as references are validated against Identity's live role
	// catalog. Publish the complete project roles and their permissions above
	// before activating workflows, including on an existing workspace upgrade.
	workflowScope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "initialize published workflow definitions")
	if err := records.Applications().Workflows.InitializePublishedWorkflowDefinitions(ctx, manifest.Workflows, workflowScope); err != nil {
		mustCompleteRuntimeStartup(fmt.Errorf("initialize workflow definitions: %w", err))
	}
	referenceResolver := newIdentityBusinessSeedReferenceResolver(cfg.IdentityWorkspaceID, cfg.IdentityAudience, identityProjection, startupOptions.BusinessSeedReferenceCandidates)
	err = synchronizeRuntimeSeeds(ctx, store, manifest, !cfg.BusinessSeedSyncDisabled, referenceResolver)
	mustCompleteRuntimeStartup(err)
	refreshRuntimeActionCatalog := func(_ context.Context, snapshot appschemamodel.ApplicationSchemaSnapshot) (appschemaapplication.ApplicationSchemaReloadCommit, error) {
		return func() { records.Applications().Actions.ReplaceDefinitions(snapshot.Actions) }, nil
	}
	records.Applications().ApplicationSchema.AddReloadObserver(func(ctx context.Context, snapshot appschemamodel.ApplicationSchemaSnapshot) (appschemaapplication.ApplicationSchemaReloadPreparation, error) {
		commit, err := refreshRuntimeActionCatalog(ctx, snapshot)
		return appschemaapplication.ApplicationSchemaReloadPreparation{Commit: commit}, err
	})
	records.Applications().ApplicationSchema.AddReloadObserver(func(_ context.Context, snapshot appschemamodel.ApplicationSchemaSnapshot) (appschemaapplication.ApplicationSchemaReloadPreparation, error) {
		publishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		previousRegistry := authorizationRegistrySnapshot.Load()
		candidateRegistry, err := reconcileRuntimeIdentityAuthorization(publishCtx, identityBinding, snapshot, authorizationModuleActions, previousRegistry, manifest.Roles, cfg.IdentityWorkspaceID, cfg.IdentityAudience, cfg.IdentityRedirectURLs)
		if err != nil {
			return appschemaapplication.ApplicationSchemaReloadPreparation{}, fmt.Errorf("reconcile Runtime authorization definitions with Identity: %w", err)
		}
		preparation := appschemaapplication.ApplicationSchemaReloadPreparation{
			Commit: func() { authorizationRegistrySnapshot.Store(candidateRegistry) },
		}
		if identityBinding != nil {
			previousSnapshot := records.Schema()
			application := identitysdk.ApplicationRef{
				WorkspaceID: identitysdk.WorkspaceID(strings.TrimSpace(cfg.IdentityWorkspaceID)), ApplicationKey: identitysdk.ApplicationKey(strings.TrimSpace(cfg.IdentityAudience)),
			}
			rollbackIdentityPublication := func(rollbackCtx context.Context) error {
				return errors.Join(
					reconcileRuntimePermissionRegistries(rollbackCtx, identityBinding.Permissions(), application, candidateRegistry, previousRegistry),
					publishRuntimeProjectRoles(rollbackCtx, identityBinding, previousSnapshot.Objects, manifest.Roles, cfg.IdentityWorkspaceID, cfg.IdentityAudience, handlerDescriptors...),
					publishRuntimeProjectProfileExtensions(rollbackCtx, identityBinding, previousSnapshot.IdentityProfileExtensions),
				)
			}
			preparation.Abort = func(abortCtx context.Context) error {
				rollbackCtx, rollbackCancel := context.WithTimeout(context.WithoutCancel(abortCtx), 10*time.Second)
				defer rollbackCancel()
				return rollbackIdentityPublication(rollbackCtx)
			}
			if err := publishRuntimeProjectRoles(publishCtx, identityBinding, snapshot.Objects, manifest.Roles, cfg.IdentityWorkspaceID, cfg.IdentityAudience, handlerDescriptors...); err != nil {
				return appschemaapplication.ApplicationSchemaReloadPreparation{}, errors.Join(
					fmt.Errorf("publish Runtime project roles and objects to Identity: %w", err),
					fmt.Errorf("rollback Runtime Identity publication after project catalog failure: %w", rollbackIdentityPublication(publishCtx)),
				)
			}
			if err := publishRuntimeProjectProfileExtensions(publishCtx, identityBinding, snapshot.IdentityProfileExtensions); err != nil {
				return appschemaapplication.ApplicationSchemaReloadPreparation{}, errors.Join(
					fmt.Errorf("publish Runtime profile extensions to Identity: %w", err),
					fmt.Errorf("rollback Runtime Identity publication after profile-extension failure: %w", rollbackIdentityPublication(publishCtx)),
				)
			}
		}
		return preparation, nil
	})
	records.Applications().Actions.ReplaceDefinitions(records.Schema().Actions)
	manifest.ManifestHash = seedManifest.ManifestHash
	runtime := constructRuntime(runtimeConstructionInput{
		config:               cfg,
		templateID:           templateID,
		store:                store,
		applicationServices:  records,
		authorizationActions: authorizationRegistrySnapshot.Load,
		moduleBindings:       moduleBindings,
		identityBinding:      identityBinding,
		identityProjection:   identityProjection,
		identityPrincipals:   identityPrincipals,
		principalCache:       principalCache,
		integrationMode:      integrationOwner.Binding.Descriptor().Mode,
		integrationBinding:   integrationOwner.Binding,
		integrationWorkers:   integrationOwner.Workers,
		dataExchangeBinding:  serviceAssembly.dataExchangeBinding,
		lifecycleBinding:     serviceAssembly.lifecycleBinding,
		manifest:             manifest,
		workspaceRolePolicy:  workspaceRolePolicy,
		recordRepository:     recordRepository,
		rateLimiter:          sharedRateLimiter,
		notificationHTTP:     notificationHTTP,
		notificationBinding:  notificationBinding,
		monitoringBinding:    monitoringBinding,
		schedulerBinding:     schedulerBinding,
		agentBinding:         agentBinding,
		auditBinding:         auditBinding,
		metadataBinding:      metadataBinding,
		reportBinding:        reportBinding,
		notificationWorkers:  notificationWorkers,
		notificationRelay:    notificationRelay,
		worker:               serviceAssembly.worker,
		businessHandlers:     businessHandlers,
		connectorProviders:   connectorProviders,
		releaseIdentity:      releaseIdentity,
		releaseCohort:        releaseCohort,
		releaseLease:         releaseLease,
		releaseAdmission:     releaseAdmission,
		releaseIntegrity:     releaseIntegrity,
	})
	runtime.borrowedStore = preparedStore != nil
	runtime.startMetadataSnapshotWatcher(ctx)
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
