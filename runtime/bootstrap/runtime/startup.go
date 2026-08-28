package runtime

import (
	"context"
	"errors"
	"fmt"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"strings"
	"time"

	connector "github.com/domainry/domainry-connector-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	sourcenotification "github.com/domainry/domainry-notification"
	sourcedelivery "github.com/domainry/domainry-notification/delivery"
	sourceinbox "github.com/domainry/domainry-notification/inbox"
	"go.uber.org/zap"

	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/audit"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	notificationapplication "github.com/domainry/domainry-runtime/runtime/application/notification"
	manifestseed "github.com/domainry/domainry-runtime/runtime/application/seed/globalcapability"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	notificationcontract "github.com/domainry/domainry-runtime/runtime/domain/notification/contract"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	notification "github.com/domainry/domainry-runtime/runtime/domain/notification/service"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	auditpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/audit"
	deploymentpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/deployment"
	integrationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/integration"
	integrationnotificationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/integrationnotification"
	notificationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/notification"
	ratelimitpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/ratelimit"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	workflowpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workflow"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/runtime/platform/localization"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
)

type runtimeNotificationRecipientDirectory struct {
	directory identitysdk.Directory
}

func (b runtimeNotificationRecipientDirectory) FindUser(ctx context.Context, id string) (identitysdk.User, bool, error) {
	if b.directory == nil {
		return identitysdk.User{}, false, nil
	}
	return b.directory.FindUser(ctx, identitysdk.UserLookup{UserID: identitysdk.SubjectID(id)})
}

func (b runtimeNotificationRecipientDirectory) FindRecipient(ctx context.Context, _ sourcenotification.WorkspaceID, id sourcenotification.UserID) (sourcenotification.Recipient, bool, error) {
	user, found, err := b.FindUser(ctx, id.String())
	return sourcenotification.Recipient{ID: id, Email: user.Email, Locale: user.Locale, Timezone: user.Timezone}, found, err
}

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

func newProjectRecordNotificationActionAuthorizer(getRecord func(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error)) notificationapplication.NotificationInboxResolvedResourceAuthorizer {
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

func registerIntegrationNotificationActionAuthorizers(registry *notificationapplication.NotificationInboxActionAuthorizerRegistry, resources integrationNotificationResourceReader) {
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

func New(ctx context.Context, cfg config.Config, identityBinding identitysdk.Binding) *Runtime {
	businessHandlers := runtimeext.NewBusinessHandlerRegistry()
	businessHandlers.Freeze()
	connectorProviders := emptyConnectorProviderRegistry()
	return NewWithExtensions(ctx, cfg, businessHandlers, connectorProviders, identityBinding)
}

func NewWithBusinessHandlers(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, identityBinding identitysdk.Binding) *Runtime {
	connectorProviders := emptyConnectorProviderRegistry()
	return NewWithExtensions(ctx, cfg, businessHandlers, connectorProviders, identityBinding)
}

func emptyConnectorProviderRegistry() *connector.Registry {
	registry := connector.NewRegistry()
	registry.Freeze()
	return registry
}

func NewWithExtensions(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, identityBinding identitysdk.Binding) *Runtime {
	return newWithExtensions(ctx, cfg, businessHandlers, connectorProviders, runtimehttp.RuntimeReleaseIdentity{}, identityBinding)
}

// NewProjectWithIdentity assembles Runtime against one already-opened SDK
// Binding. The generated project host owns Module/SaaS selection, Identity
// lifecycle, and optional Identity HTTP surfaces.
func NewProjectWithIdentity(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, artifactEvidence deploymentapplication.RuntimeReleaseArtifactEvidence, binding identitysdk.Binding) *Runtime {
	return newWithExtensions(ctx, cfg, businessHandlers, connectorProviders, releaseIdentity, binding, artifactEvidence)
}

func newWithExtensions(ctx context.Context, cfg config.Config, businessHandlers *runtimeext.BusinessHandlerRegistry, connectorProviders *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, identityBinding identitysdk.Binding, artifactEvidence ...deploymentapplication.RuntimeReleaseArtifactEvidence) *Runtime {
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
	cfg = normalizeRuntimeConfig(cfg)
	mustCompleteRuntimeStartup(cfg.ValidateSecurity())
	seedManifest, err := prepareRuntimeManifest(ctx, cfg)
	mustCompleteRuntimeStartup(err)
	store, err := prepareRuntimeStore(ctx, cfg)
	mustCompleteRuntimeStartup(err)
	workerDependencies, err := newRuntimeWorkerDependencies(cfg.RuntimeInstanceID)
	mustCompleteRuntimeStartup(err)
	releaseCohort := deploymentapplication.NewDeploymentRuntimeReleaseCohortApplicationService(deploymentpersistence.NewRuntimeReleaseCohortStore(store))
	releaseAdmission := &deploymentapplication.RuntimeReleaseAdmission{}
	var releaseLease deploymentmodel.RuntimeReleaseCohortLease
	startupOwnsStore := true
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
	runtimeAudit := auditapplication.NewAuditApplicationService(auditpersistence.NewAuditStore(store), auditpersistence.NewAuditBusinessExportStore(store))
	identityDirectory := identityBinding.Directory()
	identityPrincipals := identityBinding.Principals()
	if identityDirectory == nil || identityPrincipals == nil {
		mustCompleteRuntimeStartup(errors.New("Identity Binding returned incomplete Runtime ports"))
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
	mustCompleteRuntimeStartup(manifestseed.SyncRows(ctx, auditpersistence.NewAuditStore(store), workflowpersistence.NewWorkflowWorkerStore(store)))
	templateID := valueOrDefault(manifest.TemplateID, generatedTemplateID)
	recipientDirectory := runtimeNotificationRecipientDirectory{directory: identityDirectory}
	runtimeNotificationEventTypes, err := notification.NotificationRuntimeEventTypes(manifest.NotificationEventTypes, localization.SupportedLocales(), localization.DefaultLocale, localization.Lookup)
	mustCompleteRuntimeStartup(err)
	notificationEventCatalog, err := notification.NewNotificationEventCatalogWithRules(runtimeNotificationEventTypes, manifest.NotificationRules)
	mustCompleteRuntimeStartup(err)
	workflowNotificationTasks := workflowpersistence.NewWorkflowProcessStore(store)
	notificationAudienceResolvers := notificationapplication.NewNotificationAudienceResolverRegistry()
	notificationAudienceResolvers.Register("workflow_task_assignee", newWorkflowTaskAssigneeResolver(workflowNotificationTasks.GetTask))
	notificationAudienceResolvers.Freeze()
	integrationNotificationResources := integrationpersistence.NewIntegrationConfigStore(store)
	notificationActionAuthorizers := notificationapplication.NewNotificationInboxActionAuthorizerRegistry()
	reportNotificationActions := &runtimeNotificationActionAuthorizerBinding{}
	notificationActionAuthorizers.Register("report", reportNotificationActions.Authorize)
	automationNotificationActions := &runtimeNotificationActionAuthorizerBinding{}
	notificationActionAuthorizers.Register("automation_rule", automationNotificationActions.Authorize)
	projectRecordNotificationActions := &runtimeNotificationResolvedActionAuthorizerBinding{}
	notificationActionAuthorizers.RegisterResolved("project_record", projectRecordNotificationActions.Authorize)
	notificationActionAuthorizers.Register("workflow_task", newWorkflowTaskNotificationActionAuthorizer(workflowNotificationTasks.GetTask))
	notificationActionAuthorizers.Register("scheduler_job", newSchedulerNotificationActionAuthorizer(restoredMetadata.metadataStore.GetDefinition))
	registerIntegrationNotificationActionAuthorizers(notificationActionAuthorizers, integrationNotificationResources)
	recordBatchNotifications := recordpersistence.NewRecordStore(store)
	notificationActionAuthorizers.Register("record_batch_job", newRecordBatchNotificationActionAuthorizer(recordBatchNotifications.GetRecordBatchJob))
	notificationActionAuthorizers.Freeze()
	notificationModuleStore, err := notificationpersistence.NewSQLStoreAdapter(store, workerDependencies.Clock)
	mustCompleteRuntimeStartup(err)
	templateEngine, templateManager, publicationProcessor, err := notificationapplication.NewTemplateModule(
		valueOrDefault(manifest.DefaultLocale, cfg.AppLocale), manifest.NotificationTemplates,
		notificationapplication.TemplateModuleDependencies{Store: notificationModuleStore, Clock: workerDependencies.Clock, WorkerID: workerDependencies.WorkerID.String(), Directory: recipientDirectory, WorkNotifier: runtimeNotificationWorkNotifier{broker: store.WorkerWakeups()}},
	)
	mustCompleteRuntimeStartup(err)
	templateRenderer, err := notificationapplication.NewNotificationTemplateRenderer(templateEngine)
	mustCompleteRuntimeStartup(err)
	inboxConfiguration, err := sourceinbox.NewConfiguration([]sourcenotification.Surface{
		sourcenotification.Surface(runtimeext.SurfaceBusinessWorkspace),
		sourcenotification.Surface(runtimeext.SurfaceConsumerPortal),
	}, notificationModuleChannels(manifest.NotificationRules))
	mustCompleteRuntimeStartup(err)
	inboxValidator, err := sourceinbox.NewValidator(inboxConfiguration)
	mustCompleteRuntimeStartup(err)
	notificationCatalog, err := notificationModuleCatalog(inboxValidator, runtimeNotificationEventTypes, manifest.NotificationRules)
	mustCompleteRuntimeStartup(err)
	inboxCompiler, err := sourceinbox.NewCompiler(notificationCatalog, inboxValidator, workerDependencies.Clock)
	mustCompleteRuntimeStartup(err)
	moduleWorkNotifier := runtimeNotificationWorkNotifier{broker: store.WorkerWakeups()}
	eventPublisher, err := sourceinbox.NewPublisher(inboxCompiler, notificationModuleStore, moduleWorkNotifier)
	mustCompleteRuntimeStartup(err)
	inboxProcessor, err := sourceinbox.NewProcessor(sourceinbox.ProcessorDependencies{
		Events: notificationModuleStore, Clock: workerDependencies.Clock, WorkerID: workerDependencies.WorkerID.String(),
		Audiences: notificationAudienceResolvers, RecipientLocale: runtimeNotificationRecipientLocale{lookup: recipientDirectory.FindUser}, WorkNotifier: moduleWorkNotifier,
	})
	mustCompleteRuntimeStartup(err)
	policyManager, err := sourcedelivery.NewPolicyManager(sourcedelivery.PolicyManagerDependencies{Store: notificationModuleStore, Clock: workerDependencies.Clock})
	mustCompleteRuntimeStartup(err)
	notificationOutboxDispatcher, err := notificationapplication.NewNotificationOutboxDispatcher(
		integrationpersistence.NewIntegrationDeliveryStore(store), cfg.EffectiveProductBrandName(), nil,
	)
	mustCompleteRuntimeStartup(err)
	notificationDeliveryProcessor, err := sourcedelivery.NewProcessor(sourcedelivery.ProcessorDependencies{
		Plans: notificationModuleStore, Renderer: templateEngine, Dispatcher: notificationOutboxDispatcher,
		Policy: policyManager, Clock: workerDependencies.Clock, WorkerID: workerDependencies.WorkerID.String(),
	})
	mustCompleteRuntimeStartup(err)
	mailboxManager, err := sourceinbox.NewMailboxManager(sourceinbox.MailboxManagerDependencies{
		Validator: inboxValidator, Mailboxes: notificationModuleStore, SavedViews: notificationModuleStore,
		Delegations: notificationModuleStore, Metrics: notificationModuleStore, Clock: workerDependencies.Clock,
	})
	mustCompleteRuntimeStartup(err)
	actionResolver, err := sourceinbox.NewActionResolver(mailboxManager, notificationCatalog, workerDependencies.Clock)
	mustCompleteRuntimeStartup(err)
	notificationManagement := notificationapplication.NewNotificationApplicationServiceWithModule(
		notificationActionAuthorizers.Authorize, notificationEventCatalog,
		notificationapplication.NotificationModuleDependencies{
			Mailbox: mailboxManager, Actions: actionResolver, Compiler: inboxCompiler, Publisher: eventPublisher, Processor: inboxProcessor, Policy: policyManager,
			DeliveryProcessor: notificationDeliveryProcessor, Templates: templateManager, Publications: publicationProcessor,
			DeliveryMetrics: restoredMetadata.deliveryMetricsStore, Capabilities: notificationcontract.NotificationProviderCapabilities(),
		},
	)
	notificationManagement.BindWorkerWakeups(ctx, store.WorkerWakeups())
	notificationStartupScope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "refresh published notification templates")
	mustCompleteRuntimeStartup(notificationManagement.RefreshPublished(ctx, notificationStartupScope))
	manifest.TemplateID = templateID
	manifest.Version = valueOrDefault(manifest.Version, generatedTemplateVersion)
	sharedRateLimiter := ratelimitpersistence.NewRateLimiter(store)
	mustCompleteRuntimeStartup(sharedRateLimiter.EnsureSchema(ctx))
	workflowNotificationScope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "compile workflow notification intent")
	startupCallbacks := runtimeStartupCallbacks{records: nil, notificationManagement: notificationManagement, workflowNotificationScope: workflowNotificationScope}
	serviceAssembly, err := assembleRuntimeServices(ctx, cfg, manifest, templateRenderer, store, identityDirectory, identityPrincipals, runtimeAudit, sharedRateLimiter, workerDependencies, runtimeExtensionRegistries{
		businessHandlers: businessHandlers, connectorProviders: connectorProviders,
		notificationCompiler:               startupCallbacks.CompileNotification,
		taskNotificationCommitter:          workflowpersistence.NewWorkflowTaskNotificationStore(store),
		integrationNotificationPublisher:   notificationManagement.PublishInboxIntent,
		integrationCredentialNotifications: integrationnotificationpersistence.NewIntegrationCredentialNotificationCommitter(store),
		integrationCredentialExpirySource:  integrationpersistence.NewIntegrationCredentialExpiryStore(store),
	})
	mustCompleteRuntimeStartup(err)
	records, recordRepository := serviceAssembly.services, serviceAssembly.records
	mustCompleteRuntimeStartup(publishRuntimeIdentityCatalog(ctx, identityBinding, records.Schema(), cfg.IdentityWorkspaceID, cfg.IdentityAudience, cfg.IdentityRedirectURLs))
	startupCallbacks.records = records
	notificationOutboxDispatcher.BindWakeup(func(message integrationmodel.IntegrationOutboxMessage) {
		integrationapplication.WakeIntegrationOutbox(records.Applications().Integrations, integrationapplication.IntegrationOutboxLocator{WorkspaceID: message.WorkspaceID, MessageID: message.ID})
	})
	reportNotificationActions.authorize = newReportNotificationActionAuthorizer(startupCallbacks.ReportsForPrincipal)
	automationNotificationActions.authorize = newAutomationNotificationActionAuthorizer(records.Applications().Automations.AutomationRule)
	projectRecordNotificationActions.authorize = newProjectRecordNotificationActionAuthorizer(records.Applications().Records.GetRecordForAction)
	mustCompleteRuntimeStartup(validateRuntimeActionReadiness(records.Applications().Actions))
	err = synchronizeRuntimeSeeds(ctx, store, manifest, !cfg.BusinessSeedSyncDisabled, identityDirectory)
	mustCompleteRuntimeStartup(err)
	records.Applications().Metadata.UseActionDefinitionSource(records.Applications().Actions.Definitions)
	refreshRuntimeActionCatalog := func(snapshot metadatamodel.MetadataSchemaSnapshot) {
		records.Applications().Actions.ReplaceDefinitions(snapshot.Actions)
	}
	records.Applications().Metadata.AddReloadObserver(refreshRuntimeActionCatalog)
	records.Applications().Metadata.AddReloadObserver(func(snapshot metadatamodel.MetadataSchemaSnapshot) {
		publishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := publishRuntimeIdentityCatalog(publishCtx, identityBinding, snapshot, cfg.IdentityWorkspaceID, cfg.IdentityAudience, cfg.IdentityRedirectURLs); err != nil {
			zap.L().Error("publish Runtime authorization catalog to Identity", zap.Error(err))
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
		manifest:            manifest,
		recordRepository:    recordRepository,
		rateLimiter:         sharedRateLimiter,
		notifications:       notificationManagement,
		worker:              serviceAssembly.worker,
		businessHandlers:    businessHandlers,
		connectorProviders:  connectorProviders,
		releaseIdentity:     releaseIdentity,
		releaseCohort:       releaseCohort,
		releaseLease:        releaseLease,
		releaseAdmission:    releaseAdmission,
		releaseIntegrity:    releaseIntegrity,
	})
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
