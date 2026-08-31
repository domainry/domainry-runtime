package runtime

import (
	"context"
	"errors"
	"fmt"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	auditsdk "github.com/domainry/domainry-audit-sdk"
	auditmoduleimpl "github.com/domainry/domainry-audit/module"
	connector "github.com/domainry/domainry-connector-sdk"
	dataexchangesdk "github.com/domainry/domainry-data-exchange-sdk"
	dataexchangemodulehost "github.com/domainry/domainry-data-exchange-sdk/modulehost"
	"github.com/domainry/domainry-foundation/modulehttp"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	metadatamodule "github.com/domainry/domainry-metadata/module"
	monitoringsdk "github.com/domainry/domainry-monitoring-sdk"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	"github.com/domainry/domainry-notification-sdk/modulehost"
	partysdk "github.com/domainry/domainry-party-sdk"
	reportsdk "github.com/domainry/domainry-report-sdk"
	reportmodule "github.com/domainry/domainry-report/module"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
	schedulermodulehost "github.com/domainry/domainry-scheduler-sdk/modulehost"
	schedulersaashost "github.com/domainry/domainry-scheduler-sdk/saashost"
	"go.uber.org/zap"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	notificationfacade "github.com/domainry/domainry-runtime/runtime/application/notificationfacade"
	publicationhandoff "github.com/domainry/domainry-runtime/runtime/application/publicationhandoff"
	manifestseed "github.com/domainry/domainry-runtime/runtime/application/seed/globalcapability"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	manifestvalidation "github.com/domainry/domainry-runtime/runtime/domain/manifest/validation"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	runtimeauditmodule "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/auditmodule"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	deploymentpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/deployment"
	notificationpublication "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/notificationpublication"
	publicationhandoffpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/publicationhandoff"
	workflowpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workflow"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/runtime/platform/localization"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
)

func New(ctx context.Context, cfg config.Config, identityBinding identitysdk.Binding, notificationFactory notificationsdk.Factory, partyFactory partysdk.Factory, dataExchangeFactory dataexchangesdk.Factory, integrationFactory integrationsdk.Factory) *Runtime {
	businessHandlers := runtimeext.NewBusinessHandlerRegistry()
	businessHandlers.Freeze()
	connectorProviders := emptyConnectorProviderRegistry()
	return NewWithExtensions(ctx, cfg, businessHandlers, connectorProviders, identityBinding, notificationFactory, partyFactory, dataExchangeFactory, integrationFactory)
}

func NewWithScheduler(ctx context.Context, cfg config.Config, identityBinding identitysdk.Binding, notificationFactory notificationsdk.Factory, partyFactory partysdk.Factory, schedulerFactory schedulersdk.Factory, dataExchangeFactory dataexchangesdk.Factory, integrationFactory integrationsdk.Factory, agent ...agentsdk.Factory) *Runtime {
	businessHandlers := runtimeext.NewBusinessHandlerRegistry()
	businessHandlers.Freeze()
	var agentFactory agentsdk.Factory
	if len(agent) > 0 {
		agentFactory = agent[0]
	}
	return newWithExtensionsUsingAllFactoriesAndStore(ctx, cfg, businessHandlers, emptyConnectorProviderRegistry(), runtimehttp.RuntimeReleaseIdentity{}, identityBinding, notificationFactory, partyFactory, nil, schedulerFactory, dataExchangeFactory, agentFactory, integrationFactory, reportmodule.NewFactory(), nil)
}

func NewWithBusinessHandlers(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, identityBinding identitysdk.Binding, notificationFactory notificationsdk.Factory, partyFactory partysdk.Factory, dataExchangeFactory dataexchangesdk.Factory, integrationFactory integrationsdk.Factory) *Runtime {
	connectorProviders := emptyConnectorProviderRegistry()
	return NewWithExtensions(ctx, cfg, businessHandlers, connectorProviders, identityBinding, notificationFactory, partyFactory, dataExchangeFactory, integrationFactory)
}

func NewWithBusinessHandlersAndScheduler(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, identityBinding identitysdk.Binding, notificationFactory notificationsdk.Factory, partyFactory partysdk.Factory, schedulerFactory schedulersdk.Factory, dataExchangeFactory dataexchangesdk.Factory, integrationFactory integrationsdk.Factory, agent ...agentsdk.Factory) *Runtime {
	var agentFactory agentsdk.Factory
	if len(agent) > 0 {
		agentFactory = agent[0]
	}
	return newWithExtensionsUsingAllFactoriesAndStore(ctx, cfg, businessHandlers, emptyConnectorProviderRegistry(), runtimehttp.RuntimeReleaseIdentity{}, identityBinding, notificationFactory, partyFactory, nil, schedulerFactory, dataExchangeFactory, agentFactory, integrationFactory, reportmodule.NewFactory(), nil)
}

func emptyConnectorProviderRegistry() *connector.Registry {
	registry := connector.NewRegistry()
	registry.Freeze()
	return registry
}

func NewWithExtensions(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, identityBinding identitysdk.Binding, notificationFactory notificationsdk.Factory, partyFactory partysdk.Factory, dataExchangeFactory dataexchangesdk.Factory, integrationFactory integrationsdk.Factory) *Runtime {
	return newWithExtensions(ctx, cfg, businessHandlers, connectorProviders, runtimehttp.RuntimeReleaseIdentity{}, identityBinding, notificationFactory, partyFactory, dataExchangeFactory, integrationFactory)
}

// NewProjectWithIdentity assembles Runtime against one already-opened SDK
// Binding. The generated project host owns Module/SaaS selection, Identity
// lifecycle, and optional Identity HTTP surfaces.
func NewProjectWithIdentity(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, artifactEvidence deploymentapplication.RuntimeReleaseArtifactEvidence, binding identitysdk.Binding, notificationFactory notificationsdk.Factory, partyFactory partysdk.Factory, dataExchangeFactory dataexchangesdk.Factory, integrationFactory integrationsdk.Factory) *Runtime {
	return newWithExtensionsUsingStore(ctx, cfg, businessHandlers, connectorProviders, releaseIdentity, binding, notificationFactory, partyFactory, dataExchangeFactory, integrationFactory, nil, artifactEvidence)
}

func NewProjectWithIdentityAndDatabase(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, artifactEvidence deploymentapplication.RuntimeReleaseArtifactEvidence, binding identitysdk.Binding, notificationFactory notificationsdk.Factory, partyFactory partysdk.Factory, dataExchangeFactory dataexchangesdk.Factory, integrationFactory integrationsdk.Factory, store *persistence.RuntimeStore) *Runtime {
	return newWithExtensionsUsingStore(ctx, cfg, businessHandlers, connectorProviders, releaseIdentity, binding, notificationFactory, partyFactory, dataExchangeFactory, integrationFactory, store, artifactEvidence)
}

func NewProjectWithFactoriesAndDatabase(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, artifactEvidence deploymentapplication.RuntimeReleaseArtifactEvidence, identity identitysdk.Binding, notification notificationsdk.Factory, party partysdk.Factory, dataExchange dataexchangesdk.Factory, integration integrationsdk.Factory, store *persistence.RuntimeStore) *Runtime {
	return newWithExtensionsUsingFactoriesAndStore(ctx, cfg, businessHandlers, connectorProviders, releaseIdentity, identity, notification, party, dataExchange, integration, store, artifactEvidence)
}

// NewProjectWithAllFactoriesAndDatabase lets a generated project select Party
// Module or SaaS explicitly. Existing constructors retain Module by default.
func NewProjectWithAllFactoriesAndDatabase(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, artifactEvidence deploymentapplication.RuntimeReleaseArtifactEvidence, identity identitysdk.Binding, notification notificationsdk.Factory, party partysdk.Factory, dataExchange dataexchangesdk.Factory, integration integrationsdk.Factory, store *persistence.RuntimeStore, monitoring ...monitoringsdk.Factory) *Runtime {
	var factory monitoringsdk.Factory
	if len(monitoring) > 0 {
		factory = monitoring[0]
	}
	return newWithExtensionsUsingAllFactoriesAndStore(ctx, cfg, businessHandlers, connectorProviders, releaseIdentity, identity, notification, party, factory, nil, dataExchange, nil, integration, reportmodule.NewFactory(), store, artifactEvidence)
}

func NewProjectWithOwnerFactoriesAndDatabase(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, artifactEvidence deploymentapplication.RuntimeReleaseArtifactEvidence, identity identitysdk.Binding, notification notificationsdk.Factory, party partysdk.Factory, monitoring monitoringsdk.Factory, scheduler schedulersdk.Factory, dataExchange dataexchangesdk.Factory, integration integrationsdk.Factory, store *persistence.RuntimeStore, agent ...agentsdk.Factory) *Runtime {
	var agentFactory agentsdk.Factory
	if len(agent) > 0 {
		agentFactory = agent[0]
	}
	return newWithExtensionsUsingAllFactoriesAndStore(ctx, cfg, businessHandlers, connectorProviders, releaseIdentity, identity, notification, party, monitoring, scheduler, dataExchange, agentFactory, integration, reportmodule.NewFactory(), store, artifactEvidence)
}

func newWithExtensions(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, identityBinding identitysdk.Binding, notificationFactory notificationsdk.Factory, partyFactory partysdk.Factory, dataExchangeFactory dataexchangesdk.Factory, integrationFactory integrationsdk.Factory, artifactEvidence ...deploymentapplication.RuntimeReleaseArtifactEvidence) *Runtime {
	return newWithExtensionsUsingStore(ctx, cfg, businessHandlers, connectorProviders, releaseIdentity, identityBinding, notificationFactory, partyFactory, dataExchangeFactory, integrationFactory, nil, artifactEvidence...)
}

func newWithExtensionsUsingStore(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, identityBinding identitysdk.Binding, notificationFactory notificationsdk.Factory, partyFactory partysdk.Factory, dataExchangeFactory dataexchangesdk.Factory, integrationFactory integrationsdk.Factory, preparedStore *persistence.RuntimeStore, artifactEvidence ...deploymentapplication.RuntimeReleaseArtifactEvidence) *Runtime {
	return newWithExtensionsUsingFactoriesAndStore(ctx, cfg, businessHandlers, connectorProviders, releaseIdentity, identityBinding, notificationFactory, partyFactory, dataExchangeFactory, integrationFactory, preparedStore, artifactEvidence...)
}

func newWithExtensionsUsingFactoriesAndStore(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, identityBinding identitysdk.Binding, notificationFactory notificationsdk.Factory, partyFactory partysdk.Factory, dataExchangeFactory dataexchangesdk.Factory, integrationFactory integrationsdk.Factory, preparedStore *persistence.RuntimeStore, artifactEvidence ...deploymentapplication.RuntimeReleaseArtifactEvidence) *Runtime {
	return newWithExtensionsUsingAllFactoriesAndStore(ctx, cfg, businessHandlers, connectorProviders, releaseIdentity, identityBinding, notificationFactory, partyFactory, nil, nil, dataExchangeFactory, nil, integrationFactory, reportmodule.NewFactory(), preparedStore, artifactEvidence...)
}

func newWithExtensionsUsingAllFactoriesAndStore(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, identityBinding identitysdk.Binding, notificationFactory notificationsdk.Factory, partyFactory partysdk.Factory, monitoringFactory monitoringsdk.Factory, schedulerFactory schedulersdk.Factory, dataExchangeFactory dataexchangesdk.Factory, agentFactory agentsdk.Factory, integrationFactory integrationsdk.Factory, reportFactory reportsdk.Factory, preparedStore *persistence.RuntimeStore, artifactEvidence ...deploymentapplication.RuntimeReleaseArtifactEvidence) *Runtime {
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
	if partyFactory == nil {
		panic("bootstrap.NewWithExtensions requires a Party SDK Factory")
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
	ownerProjectedManifest, validationErr := addIntegrationOwnerValidationCatalog(ctx, seedManifest, integrationOwner.Catalog)
	mustCompleteRuntimeStartup(validationErr)
	if !cfg.SkipManifestValidation && !(cfg.AllowEmptyAuthoringManifest && runtimeManifestHasNoBusinessObjects(seedManifest)) {
		mustCompleteRuntimeStartup(manifestvalidation.ValidateManifest(ownerProjectedManifest))
	}
	mustCompleteRuntimeStartup(synchronizeReportDefinitions(ctx, reportBinding, seedManifest))
	restoredMetadata, err := restoreRuntimeMetadata(ctx, store, seedManifest)
	mustCompleteRuntimeStartup(err)
	manifest := restoredMetadata.manifest
	auditBinding, err := auditmoduleimpl.NewFactory(auditmoduleimpl.Options{}).OpenModule(ctx, auditsdk.ApplicationRef{InstallationID: valueOrDefault(manifest.TemplateID, "domainry-runtime")}, runtimeauditmodule.NewHost(store))
	mustCompleteRuntimeStartup(err)
	mustCompleteRuntimeStartup(auditBinding.Descriptor().Validate())
	runtimeAuditRepository := runtimeauditmodule.NewAuditStore(auditBinding)
	runtimeAudit := auditapplication.NewAuditApplicationService(runtimeAuditRepository)
	identityDirectory := identityBinding.Directory()
	identityPrincipals := identityBinding.Principals()
	if identityDirectory == nil || identityPrincipals == nil {
		mustCompleteRuntimeStartup(errors.New("Identity Binding returned incomplete Runtime ports"))
	}
	identityDataExchangeKey, identityDataExchangeImport, identityDataExchangeExport := identityDataExchangeProviders(identityBinding)
	partyApplication := partysdk.ApplicationRef{TenantID: cfg.PartyTenantID, WorkspaceID: cfg.PartyWorkspaceID, ApplicationKey: cfg.PartyApplicationKey}
	partyHost := partySDKModuleHost{store: store, directory: identityDirectory, audit: auditBinding.Appender()}
	partyBinding, err := openPartyBinding(ctx, partyFactory, partyApplication, partyHost)
	mustCompleteRuntimeStartup(err)
	if partyBinding.Descriptor().Mode == partysdk.DeploymentModeSaaS && identityBinding.Descriptor().Mode != identitysdk.DeploymentModeSaaS {
		mustCompleteRuntimeStartup(errors.New("Party SaaS requires Identity SaaS"))
	}
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
		application := notificationsdk.ApplicationRef{TenantID: cfg.NotificationTenantID, WorkspaceID: cfg.NotificationWorkspaceID, ApplicationKey: cfg.NotificationApplicationKey}
		if moduleFactory, ok := notificationFactory.(modulehost.Factory); ok {
			host := notificationSDKModuleHost{
				store: store, identity: identityBinding, clock: workerDependencies.Clock, workerID: workerDependencies.WorkerID.String(), catalog: catalog,
				directory: identityDirectory, workflow: workflowNotificationTasks.GetTask, delivery: sdkDeliveryGateway,
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
			mustCompleteRuntimeStartup(store.BindNotificationSaaSPublications(persistence.NotificationSaaSPublicationScope{
				TenantID: cfg.NotificationTenantID, WorkspaceID: cfg.NotificationWorkspaceID, ApplicationKey: cfg.NotificationApplicationKey,
			}))
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
	manifest, err = restoreRuntimeManifest(ctx, sdkNotificationTemplateCatalog{system: systemTemplateBinding.SystemTemplates()}, restoredMetadata.metadataStore, seedManifest)
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
	workflowNotificationScope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "compile workflow notification intent")
	startupCallbacks := runtimeStartupCallbacks{records: nil, notificationManagement: notificationCompiler, workflowNotificationScope: workflowNotificationScope}
	var agentBinding agentsdk.Binding
	agentBinding, err = openAgentBinding(ctx, cfg.RuntimeInstanceID, store, agentFactory)
	mustCompleteRuntimeStartup(err)
	mustCompleteRuntimeStartup(synchronizeAgentDefinitions(ctx, agentBinding, &manifest))
	serviceAssembly, err := assembleRuntimeServices(ctx, cfg, manifest, templateRenderer, store, identityDirectory, identityPrincipals, partyBinding.Directory(), runtimeAudit, workerDependencies, runtimeExtensionRegistries{
		businessHandlers: businessHandlers, connectorProviders: connectorProviders,
		notificationCompiler:         startupCallbacks.CompileNotification,
		taskNotificationCommitter:    workflowpersistence.NewWorkflowTaskNotificationStore(store),
		notificationPublisher:        notificationPublisher,
		integrationOwnerDelivery:     integrationOwner.Delivery,
		integrationOwnerCatalog:      integrationOwner.Catalog,
		integrationOwnerManagement:   integrationOwner.Management,
		integrationOwnerOperations:   integrationOwner.Operations,
		dataExchangeProviderKey:      identityDataExchangeKey,
		dataExchangeImportProvider:   identityDataExchangeImport,
		dataExchangeExportProvider:   identityDataExchangeExport,
		integrationMode:              integrationOwner.Binding.Descriptor().Mode,
		notificationSubjectLifecycle: notificationSystemSubjectLifecycle{subjects: systemSubjectBinding.SystemSubjects()},
		notificationRetention:        notificationSystemRetention{retention: systemRetentionBinding.SystemRetention()},
		auditRepository:              runtimeAuditRepository,
		auditSubjectLifecycle:        runtimeauditmodule.NewSubjectLifecycle(auditBinding),
		dataExchangeFactory:          dataExchangeFactory,
		agentBinding:                 agentBinding,
		reportBinding:                reportBinding,
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
	if !ok || !auditBinding.Descriptor().Capabilities.HTTPSurface {
		mustCompleteRuntimeStartup(errors.New("Audit Binding does not declare its product HTTP surface capability"))
	}
	auditSurfaces := auditHTTP.HTTPSurfaces()
	if len(auditSurfaces) != 1 {
		mustCompleteRuntimeStartup(errors.New("Audit Binding must return exactly one product HTTP surface"))
	}
	auditSurface := auditSurfaces[0]
	if auditSurface == nil || auditSurface.Owner() != "audit" || auditSurface.Name() != "product" {
		mustCompleteRuntimeStartup(errors.New("Audit Binding returned an unexpected product HTTP surface"))
	}
	mustCompleteRuntimeStartup(modulehttp.ValidateSurface(auditSurface))
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
		host := composition.NewSchedulerSDKModuleHost(records.Applications().Scheduler, records.Applications().PublicationHandoff, composition.IntegrationConnectionRequirements(manifest.Integrations.Connections), store, workerDependencies.WorkerID.String(), manifest.SchedulerDefinitions)
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
	mustCompleteRuntimeStartup(publishRuntimeIdentityCatalog(ctx, identityBinding, records.Schema(), cfg.IdentityWorkspaceID, cfg.IdentityAudience, cfg.IdentityRedirectURLs))
	mustCompleteRuntimeStartup(publishRuntimeProjectRoles(ctx, identityBinding, manifest.Roles, cfg.IdentityWorkspaceID, cfg.IdentityAudience))
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
	mustCompleteRuntimeStartup(validateRuntimeActionReadiness(records.Applications().Actions))
	err = synchronizeRuntimeSeeds(ctx, store, manifest, !cfg.BusinessSeedSyncDisabled, identityDirectory)
	mustCompleteRuntimeStartup(err)
	refreshRuntimeActionCatalog := func(snapshot appschemamodel.ApplicationSchemaSnapshot) {
		records.Applications().Actions.ReplaceDefinitions(snapshot.Actions)
	}
	records.Applications().ApplicationSchema.AddReloadObserver(refreshRuntimeActionCatalog)
	records.Applications().ApplicationSchema.AddReloadObserver(func(snapshot appschemamodel.ApplicationSchemaSnapshot) {
		publishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := publishRuntimeIdentityCatalog(publishCtx, identityBinding, snapshot, cfg.IdentityWorkspaceID, cfg.IdentityAudience, cfg.IdentityRedirectURLs); err != nil {
			zap.L().Error("publish Runtime authorization catalog to Identity", zap.Error(err))
		}
		if err := publishRuntimeProjectRoles(publishCtx, identityBinding, manifest.Roles, cfg.IdentityWorkspaceID, cfg.IdentityAudience); err != nil {
			zap.L().Error("publish Runtime project role catalog to Identity", zap.Error(err))
		}
	})
	refreshRuntimeActionCatalog(records.Schema())
	manifest.ManifestHash = seedManifest.ManifestHash
	runtime := constructRuntime(runtimeConstructionInput{
		config:              cfg,
		templateID:          templateID,
		store:               store,
		applicationServices: records,
		identityBinding:     identityBinding,
		identityDirectory:   identityDirectory,
		identityPrincipals:  identityPrincipals,
		integrationMode:     integrationOwner.Binding.Descriptor().Mode,
		integrationBinding:  integrationOwner.Binding,
		integrationWorkers:  integrationOwner.Workers,
		partyBinding:        partyBinding,
		dataExchangeBinding: serviceAssembly.dataExchangeBinding,
		lifecycleBinding:    serviceAssembly.lifecycleBinding,
		manifest:            manifest,
		recordRepository:    recordRepository,
		rateLimiter:         sharedRateLimiter,
		notificationHTTP:    notificationHTTP,
		notificationBinding: notificationBinding,
		monitoringBinding:   monitoringBinding,
		schedulerBinding:    schedulerBinding,
		agentBinding:        agentBinding,
		auditBinding:        auditBinding,
		metadataBinding:     metadataBinding,
		reportBinding:       reportBinding,
		notificationWorkers: notificationWorkers,
		notificationRelay:   notificationRelay,
		worker:              serviceAssembly.worker,
		businessHandlers:    businessHandlers,
		connectorProviders:  connectorProviders,
		releaseIdentity:     releaseIdentity,
		releaseCohort:       releaseCohort,
		releaseLease:        releaseLease,
		releaseAdmission:    releaseAdmission,
		releaseIntegrity:    releaseIntegrity,
	})
	runtime.borrowedStore = preparedStore != nil
	runtime.startMetadataSnapshotWatcher(ctx)
	startupOwnsStore = false
	rateLimiterTransferred = true
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
