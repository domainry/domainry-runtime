package runtime

import (
	"context"
	"errors"
	"fmt"
	agentsdk "github.com/domainry/domainry-agent-sdk"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"strings"
	"time"

	auditsdk "github.com/domainry/domainry-audit-sdk"
	auditmoduleimpl "github.com/domainry/domainry-audit/module"
	connector "github.com/domainry/domainry-connector-sdk"
	dataexchangesdk "github.com/domainry/domainry-data-exchange-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	monitoringsdk "github.com/domainry/domainry-monitoring-sdk"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	"github.com/domainry/domainry-notification-sdk/modulehost"
	partysdk "github.com/domainry/domainry-party-sdk"
	partymodulehost "github.com/domainry/domainry-party-sdk/modulehost"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
	schedulermodulehost "github.com/domainry/domainry-scheduler-sdk/modulehost"
	schedulersaashost "github.com/domainry/domainry-scheduler-sdk/saashost"
	"go.uber.org/zap"

	"github.com/domainry/domainry-foundation/apperror"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/audit"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	notificationfacade "github.com/domainry/domainry-runtime/runtime/application/notificationfacade"
	manifestseed "github.com/domainry/domainry-runtime/runtime/application/seed/globalcapability"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	runtimeauditmodule "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/auditmodule"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	deploymentpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/deployment"
	integrationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/integration"
	integrationnotificationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/integrationnotification"
	notificationpublication "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/notificationpublication"
	ratelimitpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/ratelimit"
	workflowpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workflow"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/runtime/platform/localization"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
	notificationhttp "github.com/domainry/domainry-runtime/runtime/transport/http/notifications"
)

type runtimeNotificationActionAuthorizerBinding struct {
	authorize func(context.Context, string, principalmodel.Principal) error
}

type runtimeNotificationResolvedActionAuthorizerBinding struct {
	authorize func(context.Context, notificationmodel.NotificationInboxResolvedAction, principalmodel.Principal) error
}

func (b *runtimeNotificationResolvedActionAuthorizerBinding) Authorize(ctx context.Context, action notificationmodel.NotificationInboxResolvedAction, principal principalmodel.Principal) error {
	if b == nil || b.authorize == nil {
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.notification.inbox_action_unavailable"}
	}
	return b.authorize(ctx, action, principal)
}

func (b *runtimeNotificationActionAuthorizerBinding) Authorize(ctx context.Context, resourceID string, principal principalmodel.Principal) error {
	if b == nil || b.authorize == nil {
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.notification.inbox_action_unavailable"}
	}
	return b.authorize(ctx, resourceID, principal)
}

func newProjectRecordNotificationActionAuthorizer(getRecord func(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error)) notificationfacade.InboxResolvedResourceAuthorizer {
	return func(ctx context.Context, action notificationmodel.NotificationInboxResolvedAction, principal principalmodel.Principal) error {
		objectKey, recordID := strings.TrimSpace(action.RouteParams["object_key"]), strings.TrimSpace(action.RouteParams["resource_id"])
		if getRecord == nil || objectKey == "" || recordID == "" {
			return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.notification.inbox_action_unavailable"}
		}
		_, err := getRecord(ctx, objectKey, recordID, principal)
		return err
	}
}

type integrationNotificationResourceReader interface {
	ListSecrets(context.Context, string) ([]integrationmodel.IntegrationSecret, error)
	ListConnections(context.Context, string) ([]integrationmodel.IntegrationConnection, error)
}

func registerIntegrationNotificationActionAuthorizers(registry *notificationfacade.ActionAuthorizerRegistry, resources integrationNotificationResourceReader) {
	if registry == nil || resources == nil {
		return
	}
	registry.Register("integration_secret", func(ctx context.Context, resourceID string, principal principalmodel.Principal) error {
		if !integrationapplication.HasPermission(principal, integrationapplication.PermissionSecretManage) {
			return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.notification.inbox_action_forbidden"}
		}
		values, err := resources.ListSecrets(ctx, principal.WorkspaceID)
		if err != nil {
			return err
		}
		for _, value := range values {
			if value.Key == resourceID {
				return nil
			}
		}
		return &apperror.AppError{Kind: apperror.KindNotFound, Code: "backend.notification.inbox_action_resource_not_found"}
	})
	registry.Register("integration_connection", func(ctx context.Context, resourceID string, principal principalmodel.Principal) error {
		if !integrationapplication.HasPermission(principal, integrationapplication.PermissionConnectionManage) {
			return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.notification.inbox_action_forbidden"}
		}
		values, err := resources.ListConnections(ctx, principal.WorkspaceID)
		if err != nil {
			return err
		}
		for _, value := range values {
			if value.Key == resourceID {
				return nil
			}
		}
		return &apperror.AppError{Kind: apperror.KindNotFound, Code: "backend.notification.inbox_action_resource_not_found"}
	})
}

func New(ctx context.Context, cfg config.Config, identityBinding identitysdk.Binding, notificationFactory notificationsdk.Factory, partyFactory partysdk.Factory, dataExchangeFactory dataexchangesdk.Factory) *Runtime {
	businessHandlers := runtimeext.NewBusinessHandlerRegistry()
	businessHandlers.Freeze()
	connectorProviders := emptyConnectorProviderRegistry()
	return NewWithExtensions(ctx, cfg, businessHandlers, connectorProviders, identityBinding, notificationFactory, partyFactory, dataExchangeFactory)
}

func NewWithScheduler(ctx context.Context, cfg config.Config, identityBinding identitysdk.Binding, notificationFactory notificationsdk.Factory, partyFactory partysdk.Factory, schedulerFactory schedulersdk.Factory, dataExchangeFactory dataexchangesdk.Factory) *Runtime {
	businessHandlers := runtimeext.NewBusinessHandlerRegistry()
	businessHandlers.Freeze()
	return newWithExtensionsUsingAllFactoriesAndStore(ctx, cfg, businessHandlers, emptyConnectorProviderRegistry(), runtimehttp.RuntimeReleaseIdentity{}, identityBinding, notificationFactory, partyFactory, nil, schedulerFactory, dataExchangeFactory, nil, nil)
}

func NewWithBusinessHandlers(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, identityBinding identitysdk.Binding, notificationFactory notificationsdk.Factory, partyFactory partysdk.Factory, dataExchangeFactory dataexchangesdk.Factory) *Runtime {
	connectorProviders := emptyConnectorProviderRegistry()
	return NewWithExtensions(ctx, cfg, businessHandlers, connectorProviders, identityBinding, notificationFactory, partyFactory, dataExchangeFactory)
}

func NewWithBusinessHandlersAndScheduler(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, identityBinding identitysdk.Binding, notificationFactory notificationsdk.Factory, partyFactory partysdk.Factory, schedulerFactory schedulersdk.Factory, dataExchangeFactory dataexchangesdk.Factory) *Runtime {
	return newWithExtensionsUsingAllFactoriesAndStore(ctx, cfg, businessHandlers, emptyConnectorProviderRegistry(), runtimehttp.RuntimeReleaseIdentity{}, identityBinding, notificationFactory, partyFactory, nil, schedulerFactory, dataExchangeFactory, nil, nil)
}

func emptyConnectorProviderRegistry() *connector.Registry {
	registry := connector.NewRegistry()
	registry.Freeze()
	return registry
}

func NewWithExtensions(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, identityBinding identitysdk.Binding, notificationFactory notificationsdk.Factory, partyFactory partysdk.Factory, dataExchangeFactory dataexchangesdk.Factory) *Runtime {
	return newWithExtensions(ctx, cfg, businessHandlers, connectorProviders, runtimehttp.RuntimeReleaseIdentity{}, identityBinding, notificationFactory, partyFactory, dataExchangeFactory)
}

// NewProjectWithIdentity assembles Runtime against one already-opened SDK
// Binding. The generated project host owns Module/SaaS selection, Identity
// lifecycle, and optional Identity HTTP surfaces.
func NewProjectWithIdentity(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, artifactEvidence deploymentapplication.RuntimeReleaseArtifactEvidence, binding identitysdk.Binding, notificationFactory notificationsdk.Factory, partyFactory partysdk.Factory, dataExchangeFactory dataexchangesdk.Factory) *Runtime {
	return newWithExtensionsUsingStore(ctx, cfg, businessHandlers, connectorProviders, releaseIdentity, binding, notificationFactory, partyFactory, dataExchangeFactory, nil, artifactEvidence)
}

func NewProjectWithIdentityAndDatabase(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, artifactEvidence deploymentapplication.RuntimeReleaseArtifactEvidence, binding identitysdk.Binding, notificationFactory notificationsdk.Factory, partyFactory partysdk.Factory, dataExchangeFactory dataexchangesdk.Factory, store *persistence.RuntimeStore) *Runtime {
	return newWithExtensionsUsingStore(ctx, cfg, businessHandlers, connectorProviders, releaseIdentity, binding, notificationFactory, partyFactory, dataExchangeFactory, store, artifactEvidence)
}

func NewProjectWithFactoriesAndDatabase(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, artifactEvidence deploymentapplication.RuntimeReleaseArtifactEvidence, identity identitysdk.Binding, notification notificationsdk.Factory, party partysdk.Factory, dataExchange dataexchangesdk.Factory, store *persistence.RuntimeStore) *Runtime {
	return newWithExtensionsUsingFactoriesAndStore(ctx, cfg, businessHandlers, connectorProviders, releaseIdentity, identity, notification, party, dataExchange, store, artifactEvidence)
}

// NewProjectWithAllFactoriesAndDatabase lets a generated project select Party
// Module or SaaS explicitly. Existing constructors retain Module by default.
func NewProjectWithAllFactoriesAndDatabase(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, artifactEvidence deploymentapplication.RuntimeReleaseArtifactEvidence, identity identitysdk.Binding, notification notificationsdk.Factory, party partysdk.Factory, dataExchange dataexchangesdk.Factory, store *persistence.RuntimeStore, monitoring ...monitoringsdk.Factory) *Runtime {
	var factory monitoringsdk.Factory
	if len(monitoring) > 0 {
		factory = monitoring[0]
	}
	return newWithExtensionsUsingAllFactoriesAndStore(ctx, cfg, businessHandlers, connectorProviders, releaseIdentity, identity, notification, party, factory, nil, dataExchange, nil, store, artifactEvidence)
}

func NewProjectWithOwnerFactoriesAndDatabase(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, artifactEvidence deploymentapplication.RuntimeReleaseArtifactEvidence, identity identitysdk.Binding, notification notificationsdk.Factory, party partysdk.Factory, monitoring monitoringsdk.Factory, scheduler schedulersdk.Factory, dataExchange dataexchangesdk.Factory, store *persistence.RuntimeStore, agent ...agentsdk.Factory) *Runtime {
	var agentFactory agentsdk.Factory
	if len(agent) > 0 {
		agentFactory = agent[0]
	}
	return newWithExtensionsUsingAllFactoriesAndStore(ctx, cfg, businessHandlers, connectorProviders, releaseIdentity, identity, notification, party, monitoring, scheduler, dataExchange, agentFactory, store, artifactEvidence)
}

func newWithExtensions(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, identityBinding identitysdk.Binding, notificationFactory notificationsdk.Factory, partyFactory partysdk.Factory, dataExchangeFactory dataexchangesdk.Factory, artifactEvidence ...deploymentapplication.RuntimeReleaseArtifactEvidence) *Runtime {
	return newWithExtensionsUsingStore(ctx, cfg, businessHandlers, connectorProviders, releaseIdentity, identityBinding, notificationFactory, partyFactory, dataExchangeFactory, nil, artifactEvidence...)
}

func newWithExtensionsUsingStore(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, identityBinding identitysdk.Binding, notificationFactory notificationsdk.Factory, partyFactory partysdk.Factory, dataExchangeFactory dataexchangesdk.Factory, preparedStore *persistence.RuntimeStore, artifactEvidence ...deploymentapplication.RuntimeReleaseArtifactEvidence) *Runtime {
	return newWithExtensionsUsingFactoriesAndStore(ctx, cfg, businessHandlers, connectorProviders, releaseIdentity, identityBinding, notificationFactory, partyFactory, dataExchangeFactory, preparedStore, artifactEvidence...)
}

func newWithExtensionsUsingFactoriesAndStore(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, identityBinding identitysdk.Binding, notificationFactory notificationsdk.Factory, partyFactory partysdk.Factory, dataExchangeFactory dataexchangesdk.Factory, preparedStore *persistence.RuntimeStore, artifactEvidence ...deploymentapplication.RuntimeReleaseArtifactEvidence) *Runtime {
	return newWithExtensionsUsingAllFactoriesAndStore(ctx, cfg, businessHandlers, connectorProviders, releaseIdentity, identityBinding, notificationFactory, partyFactory, nil, nil, dataExchangeFactory, nil, preparedStore, artifactEvidence...)
}

func newWithExtensionsUsingAllFactoriesAndStore(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, identityBinding identitysdk.Binding, notificationFactory notificationsdk.Factory, partyFactory partysdk.Factory, monitoringFactory monitoringsdk.Factory, schedulerFactory schedulersdk.Factory, dataExchangeFactory dataexchangesdk.Factory, agentFactory agentsdk.Factory, preparedStore *persistence.RuntimeStore, artifactEvidence ...deploymentapplication.RuntimeReleaseArtifactEvidence) *Runtime {
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
	cfg = normalizeRuntimeConfig(cfg)
	mustCompleteRuntimeStartup(cfg.ValidateSecurity())
	seedManifest, err := prepareRuntimeManifest(ctx, cfg)
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
	restoredMetadata, err := restoreRuntimeMetadata(ctx, store, seedManifest)
	mustCompleteRuntimeStartup(err)
	manifest := restoredMetadata.manifest
	auditBinding, err := auditmoduleimpl.NewFactory(auditmoduleimpl.Options{}).OpenModule(ctx, auditsdk.ApplicationRef{InstallationID: valueOrDefault(manifest.TemplateID, "domainry-runtime")}, runtimeauditmodule.NewHost(store))
	mustCompleteRuntimeStartup(err)
	runtimeAuditRepository := runtimeauditmodule.NewRepository(auditBinding)
	runtimeAudit := auditapplication.NewAuditApplicationService(runtimeAuditRepository, auditBinding.Exporter())
	identityDirectory := identityBinding.Directory()
	identityPrincipals := identityBinding.Principals()
	if identityDirectory == nil || identityPrincipals == nil {
		mustCompleteRuntimeStartup(errors.New("Identity Binding returned incomplete Runtime ports"))
	}
	partyApplication := partysdk.ApplicationRef{TenantID: cfg.PartyTenantID, WorkspaceID: cfg.PartyWorkspaceID, ApplicationKey: cfg.PartyApplicationKey}
	var partyBinding partysdk.Binding
	if moduleFactory, ok := partyFactory.(partymodulehost.Factory); ok {
		partyBinding, err = moduleFactory.OpenModule(ctx, partyApplication, partySDKModuleHost{store: store, directory: identityDirectory})
	} else {
		partyBinding, err = partyFactory.Open(ctx, partyApplication)
	}
	mustCompleteRuntimeStartup(err)
	if partyBinding == nil {
		mustCompleteRuntimeStartup(errors.New("Party SDK Factory returned no Binding"))
	}
	if err := partyBinding.Descriptor().Validate(); err != nil {
		mustCompleteRuntimeStartup(err)
	}
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
	installationScope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "synchronize manifest integration connections")
	mustCompleteRuntimeStartup(integrationapplication.SyncManifestIntegrationConnections(ctx, integrationpersistence.NewIntegrationConfigStore(store), connectorProviders, manifest.Integrations, installationScope))
	mustCompleteRuntimeStartup(manifestseed.SyncRows(ctx, runtimeAuditRepository, workflowpersistence.NewWorkflowWorkerStore(store)))
	templateID := valueOrDefault(manifest.TemplateID, generatedTemplateID)
	runtimeNotificationEventTypes, err := NotificationRuntimeEventTypes(manifest.NotificationEventTypes, localization.SupportedLocales(), localization.DefaultLocale, localization.Lookup)
	mustCompleteRuntimeStartup(err)
	workflowNotificationTasks := workflowpersistence.NewWorkflowProcessStore(store)
	integrationNotificationResources := integrationpersistence.NewIntegrationConfigStore(store)
	notificationActionAuthorizers := notificationfacade.NewActionAuthorizerRegistry()
	reportNotificationActions := &runtimeNotificationActionAuthorizerBinding{}
	notificationActionAuthorizers.Register("report", reportNotificationActions.Authorize)
	automationNotificationActions := &runtimeNotificationActionAuthorizerBinding{}
	notificationActionAuthorizers.Register("automation_rule", automationNotificationActions.Authorize)
	projectRecordNotificationActions := &runtimeNotificationResolvedActionAuthorizerBinding{}
	notificationActionAuthorizers.RegisterResolved("project_record", projectRecordNotificationActions.Authorize)
	notificationActionAuthorizers.Register("workflow_task", newWorkflowTaskNotificationActionAuthorizer(workflowNotificationTasks.GetTask))
	notificationActionAuthorizers.Register("scheduler_job", newSchedulerNotificationActionAuthorizer(restoredMetadata.metadataStore.GetDefinition))
	registerIntegrationNotificationActionAuthorizers(notificationActionAuthorizers, integrationNotificationResources)
	notificationActionAuthorizers.Freeze()
	var templateRenderer composition.NotificationRenderer
	var notificationHTTP notificationhttp.NotificationApplication
	var notificationCompiler runtimeNotificationCompiler
	var integrationNotificationPublisher integrationapplication.IntegrationNotificationPublisher
	var sdkDeliveryGateway *notificationSDKDeliveryGateway
	var notificationBinding notificationsdk.Binding
	var notificationWorkers notificationsdk.LocalWorkers
	var notificationRelay *notificationpublication.Relay
	{
		catalog, catalogErr := notificationSDKCatalog(valueOrDefault(manifest.DefaultLocale, cfg.AppLocale), manifest, runtimeNotificationEventTypes)
		mustCompleteRuntimeStartup(catalogErr)
		sdkDeliveryGateway = &notificationSDKDeliveryGateway{repository: integrationpersistence.NewIntegrationDeliveryStore(store), productName: cfg.EffectiveProductBrandName()}
		application := notificationsdk.ApplicationRef{TenantID: cfg.NotificationTenantID, WorkspaceID: cfg.NotificationWorkspaceID, ApplicationKey: cfg.NotificationApplicationKey}
		if moduleFactory, ok := notificationFactory.(modulehost.Factory); ok {
			host := notificationSDKModuleHost{
				store: store, identity: identityBinding, clock: workerDependencies.Clock, workerID: workerDependencies.WorkerID.String(), catalog: catalog,
				directory: identityDirectory, workflow: workflowNotificationTasks.GetTask, delivery: sdkDeliveryGateway,
				metrics: notificationSDKDeliveryMetrics{store: restoredMetadata.deliveryMetricsStore}, validator: notificationSDKProviderTemplateValidator{},
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
		integrationNotificationPublisher = facade.PublishInboxIntent
	}
	systemTemplateBinding, ok := notificationBinding.(notificationsdk.SystemTemplateBinding)
	if !ok || systemTemplateBinding.SystemTemplates() == nil {
		mustCompleteRuntimeStartup(errors.New("Notification Binding returned no system template restoration port"))
	}
	manifest, err = restoreRuntimeManifest(ctx, sdkNotificationTemplateCatalog{system: systemTemplateBinding.SystemTemplates()}, restoredMetadata.metadataStore, seedManifest)
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
	sharedRateLimiter := ratelimitpersistence.NewRateLimiter(store)
	mustCompleteRuntimeStartup(sharedRateLimiter.EnsureSchema(ctx))
	workflowNotificationScope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "compile workflow notification intent")
	startupCallbacks := runtimeStartupCallbacks{records: nil, notificationManagement: notificationCompiler, workflowNotificationScope: workflowNotificationScope}
	var agentBinding agentsdk.Binding
	agentBinding, err = openAgentBinding(ctx, cfg.RuntimeInstanceID, agentFactory)
	mustCompleteRuntimeStartup(err)
	serviceAssembly, err := assembleRuntimeServices(ctx, cfg, manifest, templateRenderer, store, identityDirectory, identityPrincipals, partyBinding.Directory(), runtimeAudit, sharedRateLimiter, workerDependencies, runtimeExtensionRegistries{
		businessHandlers: businessHandlers, connectorProviders: connectorProviders,
		notificationCompiler:               startupCallbacks.CompileNotification,
		taskNotificationCommitter:          workflowpersistence.NewWorkflowTaskNotificationStore(store),
		integrationNotificationPublisher:   integrationNotificationPublisher,
		integrationCredentialNotifications: integrationnotificationpersistence.NewIntegrationCredentialNotificationCommitter(store),
		integrationCredentialExpirySource:  integrationpersistence.NewIntegrationCredentialExpiryStore(store),
		notificationSubjectLifecycle:       notificationSystemSubjectLifecycle{subjects: systemSubjectBinding.SystemSubjects()},
		notificationRetention:              notificationSystemRetention{retention: systemRetentionBinding.SystemRetention()},
		auditRepository:                    runtimeAuditRepository,
		auditSubjectLifecycle:              runtimeauditmodule.NewSubjectLifecycle(auditBinding),
		dataExchangeFactory:                dataExchangeFactory,
		agentBinding:                       agentBinding,
	})
	mustCompleteRuntimeStartup(err)
	records, recordRepository := serviceAssembly.services, serviceAssembly.records
	var monitoringBinding monitoringsdk.Binding
	if monitoringFactory != nil {
		application := monitoringsdk.ApplicationRef{RuntimeID: cfg.RuntimeInstanceID}
		monitoringBinding, err = openMonitoringBinding(ctx, monitoringFactory, application, newMonitoringModuleHost(records.Applications().RuntimeStatus, manifest.TemplateID, manifest.Version))
		mustCompleteRuntimeStartup(err)
	}
	var schedulerBinding schedulersdk.Binding
	if schedulerFactory != nil {
		application := schedulersdk.ApplicationRef{RuntimeID: cfg.RuntimeInstanceID}
		host := composition.NewSchedulerSDKModuleHost(records.Applications().Scheduler, records.Applications().Integrations, store, workerDependencies.WorkerID.String())
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
	notificationWakeup := func(message integrationmodel.IntegrationOutboxMessage) {
		integrationapplication.WakeIntegrationOutbox(records.Applications().Integrations, integrationapplication.IntegrationOutboxLocator{WorkspaceID: message.WorkspaceID, MessageID: message.ID})
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
	records.Applications().ApplicationSchema.UseActionDefinitionSource(records.Applications().Actions.Definitions)
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
		partyBinding:        partyBinding,
		dataExchangeBinding: serviceAssembly.dataExchangeBinding,
		manifest:            manifest,
		recordRepository:    recordRepository,
		rateLimiter:         sharedRateLimiter,
		notificationHTTP:    notificationHTTP,
		notificationBinding: notificationBinding,
		monitoringBinding:   monitoringBinding,
		schedulerBinding:    schedulerBinding,
		agentBinding:        agentBinding,
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
	return BindHTTP(ctx, runtime)
}

type runtimeNotificationCompiler interface {
	CompileInboxIntent(notificationmodel.NotificationIntent, principalmodel.SystemScope) (notificationmodel.NotificationEvent, error)
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
