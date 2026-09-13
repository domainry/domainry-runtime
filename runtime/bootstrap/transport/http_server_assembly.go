package transport

import (
	"context"
	"crypto/sha256"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulehttp"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityprincipal "github.com/domainry/domainry-identity-sdk/authorization/principal"
	identityhttpmiddleware "github.com/domainry/domainry-identity-sdk/httpmiddleware"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	monitoringsdk "github.com/domainry/domainry-monitoring-sdk"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"

	capacityplatform "github.com/domainry/domainry-foundation/capacity"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	businesssystemapplication "github.com/domainry/domainry-runtime/runtime/application/businesssystem"
	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	principalapplication "github.com/domainry/domainry-runtime/runtime/application/principal"
	publicationhandoff "github.com/domainry/domainry-runtime/runtime/application/publicationhandoff"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	workspaceprovisionapplication "github.com/domainry/domainry-runtime/runtime/application/workspaceprovision"

	"github.com/domainry/domainry-foundation/ratelimit"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	businesseventcontract "github.com/domainry/domainry-runtime/runtime/domain/businessevent/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	businesseventmemory "github.com/domainry/domainry-runtime/runtime/infrastructure/broadcast/memory"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	operationspersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/operations"
	workspaceprovisionpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workspaceprovision"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
	notificationhttp "github.com/domainry/domainry-runtime/runtime/transport/http/notifications"
	publicationhandoffhttp "github.com/domainry/domainry-runtime/runtime/transport/http/publicationhandoff"
	workspaceprovisionhttp "github.com/domainry/domainry-runtime/runtime/transport/http/workspaceprovision"
	"go.uber.org/zap"
)

type HTTPServerDependencies struct {
	Config                   config.Config
	Records                  *composition.RuntimeServices
	IdentityBinding          identitysdk.Binding
	PrincipalCache           identityprincipal.Cache
	AuthorizationActions     func() *actioncontract.Registry
	MonitoringBinding        monitoringsdk.Binding
	SchedulerBinding         schedulersdk.Binding
	Store                    *persistence.RuntimeStore
	IntegrationBinding       integrationsdk.Binding
	AgentRepositories        agentpersistence.Binding
	AgentBinding             agentsdk.Binding
	LifecycleBinding         lifecyclesdk.Binding
	RateLimiter              ratelimit.Limiter
	Manifest                 manifestmodel.ManifestSchema
	WorkspaceRolePolicy      workspaceprovisionpersistence.WorkspaceBootstrapRolePolicyEvidence
	WorkerControl            *workerplatform.Controller
	Clock                    identitysdk.Clock
	RuntimeInstanceID        string
	ReleaseIdentity          runtimehttp.RuntimeReleaseIdentity
	ReleaseAdmission         runtimehttp.RuntimeReleaseAdmissionProvider
	ReleaseIntegrity         runtimehttp.RuntimeReleaseIntegrityProvider
	BusinessEventBackplane   businesseventcontract.Backplane
	ModuleHTTPAdapters       []modulehttp.Adapter
	NotificationInboxActions notificationhttp.NotificationInboxActionResolver
	BusinessHandlers         *runtimeext.BusinessHandlerRegistry
}

type httpServerAssembly struct {
	dependencies     HTTPServerDependencies
	server           *runtimehttp.HTTPRouter
	callbacks        runtimehttp.HandlerCallbacks
	handlers         runtimehttp.HTTPRouterHandlers
	recordQueries    *recordapplication.RecordApplicationService
	publications     *publicationhandoff.PublicationHandoffApplicationService
	metadata         *appschemaapplication.ApplicationSchemaApplicationService
	operations       *operationsapplication.OperationsApplicationService
	identityHTTP     *identityhttpmiddleware.Middleware
	principals       identitysdk.PrincipalResolver
	scenarioReceipts *businesssystemapplication.RuntimeAuthoringScenarioReceiptService
}

func AssembleRuntimeHTTPServer(ctx context.Context, dependencies HTTPServerDependencies) *runtimehttp.HTTPRouter {
	if ctx == nil {
		panic("transport.AssembleRuntimeHTTPServer requires a non-nil construction context")
	}
	records := dependencies.Records
	backplane := dependencies.BusinessEventBackplane
	if backplane == nil {
		backplane = businesseventmemory.NewBusinessEventBackplane(dependencies.Config.BusinessEventReplayLimit, dependencies.Config.BusinessEventSubscriberBuffer)
	}
	recordApplication := records.Applications().Records
	if dependencies.IdentityBinding == nil {
		panic("transport.AssembleRuntimeHTTPServer requires an Identity SDK Binding")
	}
	publications := records.Applications().PublicationHandoff
	resolver, err := identityprincipal.NewAuthenticator(dependencies.IdentityBinding, identityprincipal.Options{
		Clock: dependencies.Clock, MaxCacheTTL: dependencies.Config.EffectivePrincipalCacheTTL(), Cache: dependencies.PrincipalCache,
		OnCacheError: func(err error) {
			zap.L().Warn("Identity principal cache operation failed; resolving from authoritative binding", zap.String("error_kind", "identity_principal_cache_operation_failed"), zap.Error(err))
		},
	})
	if err != nil {
		panic("assemble Identity SDK principal resolver: " + err.Error())
	}
	identityAuthentication, err := identityhttpmiddleware.New(resolver, identityhttpmiddleware.WithAuthorization(dependencies.IdentityBinding.Authorization()), identityhttpmiddleware.WithBindingCredential(dependencies.IdentityBinding))
	if err != nil {
		panic("assemble Identity SDK HTTP middleware: " + err.Error())
	}
	identityProjection := dependencies.IdentityBinding.Projection()
	identityPrincipals := dependencies.IdentityBinding.Principals()
	if identityProjection == nil || identityPrincipals == nil {
		panic("transport.AssembleRuntimeHTTPServer requires a complete Identity SDK Binding")
	}
	workspaceAdministrationStore := workspaceprovisionpersistence.NewWorkspaceAdministrationStore(dependencies.Store)
	var integrationAuthentication runtimehttp.IntegrationAuthenticationPrincipalProvider
	scenarioReceiptKey := sha256.Sum256([]byte("domainry-runtime-authoring-scenario-receipt-v1:" + dependencies.Config.IntegrationSecretKey))
	scenarioReceipts := businesssystemapplication.NewRuntimeAuthoringScenarioReceiptService(scenarioReceiptKey[:])
	server := runtimehttp.NewHTTPRouter(runtimehttp.HTTPRouterConfig{
		WorkspaceProvisionClientID:      dependencies.Config.RuntimeWorkspaceProvisionClientID,
		WorkspaceProvisionSigningSecret: dependencies.Config.RuntimeWorkspaceProvisionSigningSecret,
		ProductBrandName:                dependencies.Config.EffectiveProductBrandName(),
		CORSAllowedOrigins:              dependencies.Config.CORSAllowedOrigins, AllowDevAuthHeaders: dependencies.Config.RuntimeAllowDevIdentityHeaders,
		HealthCheckTimeout: dependencies.Config.HealthCheckTimeout,
		MaxJSONBodyBytes:   int64(dependencies.Config.HTTPMaxJSONBodyBytes),
		RequestTimeout:     dependencies.Config.CapacityRequestTimeout,
		ListenerGroupPolicies: map[runtimehttp.ListenerRouteGroup]runtimehttp.ListenerRouteGroupPolicy{
			runtimehttp.ListenerRouteGroupPublic: {
				MaxJSONBodyBytes: int64(dependencies.Config.HTTPPublicMaxJSONBodyBytes), RequestTimeout: dependencies.Config.HTTPPublicRequestTimeout,
				RateLimitPerMinute: dependencies.Config.HTTPPublicRateLimitPerMinute, AuditClass: "public_listener",
				AllowedOrigins: append([]string(nil), dependencies.Config.HTTPPublicOrigins...),
			},
			runtimehttp.ListenerRouteGroupManagement: {
				MaxJSONBodyBytes: int64(dependencies.Config.HTTPManagementMaxJSONBodyBytes), RequestTimeout: dependencies.Config.HTTPManagementRequestTimeout,
				RateLimitPerMinute: dependencies.Config.HTTPManagementRateLimitPerMinute, AuditClass: "tenant_governance",
				AllowedOrigins: append([]string(nil), dependencies.Config.HTTPManagementOrigins...),
			},
			runtimehttp.ListenerRouteGroupOps: {
				MaxJSONBodyBytes: int64(dependencies.Config.HTTPOpsMaxJSONBodyBytes), RequestTimeout: dependencies.Config.HTTPOpsRequestTimeout,
				RateLimitPerMinute: dependencies.Config.HTTPOpsRateLimitPerMinute, AuditClass: "privileged_operations",
				AllowedOrigins: append([]string(nil), dependencies.Config.HTTPOpsOrigins...),
			},
		},
		CapacityLimits:           capacityplatform.Limits{GlobalInFlight: dependencies.Config.CapacityGlobalInFlight, WorkspaceInFlight: dependencies.Config.CapacityWorkspaceInFlight, UseCaseInFlight: dependencies.Config.CapacityUseCaseInFlight, RetryInFlight: dependencies.Config.CapacityRetryInFlight, GlobalRate: dependencies.Config.CapacityGlobalRatePerMinute, WorkspaceRate: dependencies.Config.CapacityWorkspaceRatePerMinute, UseCaseRate: dependencies.Config.CapacityUseCaseRatePerMinute, RateWindow: time.Minute, MaxWorkspaceStates: dependencies.Config.CapacityMaxWorkspaceStates, MaxUseCaseStates: dependencies.Config.CapacityMaxUseCaseStates, WorkspaceStateTTL: dependencies.Config.CapacityWorkspaceStateTTL, DegradedRatio: dependencies.Config.CapacityDegradedRatio, RecoveryRatio: dependencies.Config.CapacityRecoveryRatio, RetryAfter: dependencies.Config.CapacityRetryAfter},
		BusinessEventReplayLimit: dependencies.Config.BusinessEventReplayLimit, BusinessEventSubscriberBuffer: dependencies.Config.BusinessEventSubscriberBuffer,
		BusinessEventGlobalConnections: dependencies.Config.BusinessEventGlobalConnections, BusinessEventWorkspaceConnections: dependencies.Config.BusinessEventWorkspaceConnections,
		BusinessEventPrincipalConnections: dependencies.Config.BusinessEventPrincipalConnections, BusinessEventHeartbeatInterval: dependencies.Config.BusinessEventHeartbeatInterval,
		BusinessEventRetryInterval: dependencies.Config.BusinessEventRetryInterval,
	}, runtimehttp.HTTPRouterDependencies{
		IdentityAuthentication: identityAuthentication, IdentityPrincipal: principalmodel.NewPrincipalFromIdentity, IntegrationAuthentication: integrationAuthentication,
		AuthorizationActions:  dependencies.AuthorizationActions,
		RateLimiter:           dependencies.RateLimiter,
		IdentityAuthorization: identityPrincipals,
		BusinessPrincipal: principalapplication.NewBusinessPrincipalApplicationService(principalapplication.BusinessPrincipalDependencies{
			Records: recordApplication.Repository(),
			Objects: func() []definitionmodel.ObjectSchema {
				return records.Schema().Objects
			},
			Extensions: func() []profilebindingmodel.Binding {
				return records.Schema().IdentityProfileExtensions
			},
		}),
		WorkspaceAdmission: workspaceAdministrationStore,
		SecurityAudit:      records.Applications().Audit, RuntimeStatus: dependencies.MonitoringBinding,
		TechnicalMetrics: func(ctx context.Context) string {
			return runtimeTechnicalOpenMetrics(ctx, dependencies.Store, records.Applications().RuntimeStatus) + runtimeOptionalWorkerMetrics(dependencies.WorkerControl)
		},
		WorkerControl:                    dependencies.WorkerControl,
		RuntimeInstanceID:                dependencies.RuntimeInstanceID,
		OperationsControlState:           runtimeOperationsControlState(dependencies.Store),
		RuntimeReleaseAdmission:          dependencies.ReleaseAdmission,
		RuntimeReleaseIntegrity:          dependencies.ReleaseIntegrity,
		BusinessEventBackplane:           backplane,
		ModuleHTTPAdapters:               dependencies.ModuleHTTPAdapters,
		RuntimeAuthoringScenarioReceipts: scenarioReceipts,
	})
	assembly := &httpServerAssembly{
		dependencies: dependencies, server: server, callbacks: server.HandlerCallbacks(),
		recordQueries: recordApplication, publications: publications,
		metadata: records.Applications().ApplicationSchema, identityHTTP: identityAuthentication,
		principals:       identityPrincipals,
		scenarioReceipts: scenarioReceipts,
	}
	assembly.wireOperationsApplication()
	assembly.bindAgentApplicationHost()
	assembly.wireIdentityReferences(ctx)
	assembly.wireRecordAndProcessHandlers()
	assembly.wireMetadataAndBusinessHandlers()
	assembly.wireRuntimePublicationHandoff()
	assembly.wireWorkspaceProvisioning(ctx)
	assembly.wireNotificationHandlers()
	server = runtimehttp.UseHandlers(server, assembly.handlers)
	server = runtimehttp.UseServiceIdentity(server, dependencies.Config.RuntimeVersion)
	server = runtimehttp.UseRuntimeReleaseIdentity(server, dependencies.ReleaseIdentity)
	return runtimehttp.UseManifest(server, dependencies.Manifest)
}

func (a *httpServerAssembly) wireWorkspaceProvisioning(ctx context.Context) {
	if a.dependencies.Store == nil {
		return
	}
	repository := workspaceprovisionpersistence.NewWorkspaceProvisionStoreWithFailureInjector(
		a.dependencies.Store,
		a.dependencies.IdentityBinding,
		a.dependencies.Manifest,
		a.workspaceBootstrapParticipant(),
		workspaceprovisionpersistence.NewAcceptanceFailureInjector(a.dependencies.Config.WorkspaceProvisionFailurePoint),
		a.dependencies.WorkspaceRolePolicy,
	)
	service := workspaceprovisionapplication.NewWorkspaceProvisionApplicationService(repository)
	installationIdentity, err := a.dependencies.Store.InstallationIdentity(ctx)
	if err != nil {
		panic("assemble Workspace administration cursor: " + err.Error())
	}
	cursorKey := a.dependencies.Config.AuditExportTokenKey
	if cursorKey == "" {
		cursorKey = config.DevAuditExportTokenKey
	}
	cursor, err := workspaceprovisionapplication.NewWorkspaceAdministrationCursorCodec([]byte(cursorKey), installationIdentity, nil)
	if err != nil {
		panic("assemble Workspace administration cursor: " + err.Error())
	}
	administration := workspaceprovisionapplication.NewWorkspaceAdministrationApplicationService(
		workspaceprovisionpersistence.NewWorkspaceAdministrationStore(a.dependencies.Store), cursor,
	)
	a.handlers.WorkspaceProvision = workspaceprovisionhttp.NewWorkspaceProvisionHandler(workspaceprovisionhttp.WorkspaceProvisionDependencies{
		UseCases: service, Administration: administration, Principal: a.callbacks.Principal, DecodeJSON: a.callbacks.DecodeJSON,
		WriteJSON: a.callbacks.WriteJSON, WriteServiceError: a.callbacks.WriteServiceError,
		SecurityAudit: a.callbacks.SecurityAudit,
	})
}

func (a *httpServerAssembly) workspaceBootstrapParticipant() runtimeext.WorkspaceBootstrapParticipant {
	if a == nil || a.dependencies.BusinessHandlers == nil {
		return nil
	}
	return a.dependencies.BusinessHandlers.WorkspaceBootstrapParticipant()
}

// wireRuntimePublicationHandoff exposes only the Runtime-owned publication
// handoff read. Product HTTP is exposed by the Integration deployment.
func (a *httpServerAssembly) wireRuntimePublicationHandoff() {
	if a.dependencies.Store == nil || a.dependencies.IntegrationBinding == nil {
		return
	}
	a.handlers.PublicationHandoff = publicationhandoffhttp.NewHandler(publicationhandoffhttp.Dependencies{
		Intents:   a.publications,
		Principal: a.callbacks.Principal, WriteJSON: a.callbacks.WriteJSON,
		WriteServiceError: a.callbacks.WriteServiceError,
		Authenticated:     a.identityHTTP.AuthenticatedFunc,
	})
}

func runtimeOptionalWorkerMetrics(workers *workerplatform.Controller) string {
	if workers == nil {
		return ""
	}
	return workers.OpenMetrics()
}

func runtimeOperationsControlState(store *persistence.RuntimeStore) func(context.Context, string, string) (bool, bool, error) {
	return func(ctx context.Context, kind, owner string) (bool, bool, error) {
		if store == nil {
			return false, false, nil
		}
		control, found, err := operationspersistence.NewOperationsStore(store).GetOperationsControl(ctx, operationsmodel.OperationsSystemPurposeRuntimeControl, operationsmodel.OperationsControlKind(kind), owner)
		return control.Active(), found, err
	}
}
