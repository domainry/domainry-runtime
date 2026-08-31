package transport

import (
	"context"

	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-foundation/modulehttp"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityprincipal "github.com/domainry/domainry-identity-sdk/authorization/principal"
	identityhttpmiddleware "github.com/domainry/domainry-identity-sdk/httpmiddleware"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	monitoringsdk "github.com/domainry/domainry-monitoring-sdk"
	partysdk "github.com/domainry/domainry-party-sdk"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"

	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	principalapplication "github.com/domainry/domainry-runtime/runtime/application/principal"
	publicationhandoff "github.com/domainry/domainry-runtime/runtime/application/publicationhandoff"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	workspaceprovisionapplication "github.com/domainry/domainry-runtime/runtime/application/workspaceprovision"
	capacityplatform "github.com/domainry/domainry-runtime/runtime/platform/capacity"

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
	publicationhandoffpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/publicationhandoff"
	workspaceprovisionpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workspaceprovision"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
	integrationhttp "github.com/domainry/domainry-runtime/runtime/transport/http/integrations"
	notificationhttp "github.com/domainry/domainry-runtime/runtime/transport/http/notifications"
	workspaceprovisionhttp "github.com/domainry/domainry-runtime/runtime/transport/http/workspaceprovision"
)

type HTTPServerDependencies struct {
	Config                   config.Config
	Records                  *composition.RuntimeServices
	IdentityBinding          identitysdk.Binding
	PartyBinding             partysdk.Binding
	MonitoringBinding        monitoringsdk.Binding
	SchedulerBinding         schedulersdk.Binding
	Store                    *persistence.RuntimeStore
	IntegrationBinding       integrationsdk.Binding
	AgentRepositories        agentpersistence.Binding
	LifecycleBinding         lifecyclesdk.Binding
	RateLimiter              ratelimit.Limiter
	Manifest                 manifestmodel.ManifestSchema
	WorkerControl            *workerplatform.Controller
	Clock                    identitysdk.Clock
	RuntimeInstanceID        string
	ReleaseIdentity          runtimehttp.RuntimeReleaseIdentity
	ReleaseAdmission         runtimehttp.RuntimeReleaseAdmissionProvider
	ReleaseIntegrity         runtimehttp.RuntimeReleaseIntegrityProvider
	BusinessEventBackplane   businesseventcontract.Backplane
	ModuleHTTPSurfaces       []modulehttp.Surface
	NotificationInboxActions notificationhttp.NotificationInboxActionResolver
}

type httpServerAssembly struct {
	dependencies  HTTPServerDependencies
	server        *runtimehttp.HTTPRouter
	callbacks     runtimehttp.HandlerCallbacks
	handlers      runtimehttp.HTTPRouterHandlers
	recordQueries *recordapplication.RecordApplicationService
	publications  *publicationhandoff.PublicationHandoffApplicationService
	metadata      *appschemaapplication.ApplicationSchemaApplicationService
	operations    *operationsapplication.OperationsApplicationService
	identityHTTP  *identityhttpmiddleware.Middleware
	principals    identitysdk.PrincipalResolver
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
	resolver, err := identityprincipal.NewResolver(dependencies.IdentityBinding, identityprincipal.Options{Clock: dependencies.Clock, MaxCacheTTL: time.Minute})
	if err != nil {
		panic("assemble Identity SDK principal resolver: " + err.Error())
	}
	identityAuthentication, err := identityhttpmiddleware.New(resolver, identityhttpmiddleware.WithAuthorization(dependencies.IdentityBinding.Authorization()))
	if err != nil {
		panic("assemble Identity SDK HTTP middleware: " + err.Error())
	}
	identityDirectory := dependencies.IdentityBinding.Directory()
	identityPrincipals := dependencies.IdentityBinding.Principals()
	if identityDirectory == nil || identityPrincipals == nil {
		panic("transport.AssembleRuntimeHTTPServer requires a complete Identity SDK Binding")
	}
	var integrationAuthentication runtimehttp.IntegrationAuthenticationPrincipalProvider
	server := runtimehttp.NewHTTPRouter(runtimehttp.HTTPRouterConfig{
		ProductBrandName:   dependencies.Config.EffectiveProductBrandName(),
		CORSAllowedOrigins: dependencies.Config.CORSAllowedOrigins, AllowDevAuthHeaders: dependencies.Config.RuntimeAllowDevIdentityHeaders,
		HealthCheckTimeout: dependencies.Config.HealthCheckTimeout,
		MaxJSONBodyBytes:   int64(dependencies.Config.HTTPMaxJSONBodyBytes),
		RequestTimeout:     dependencies.Config.CapacityRequestTimeout,
		SurfaceGroupPolicies: map[runtimehttp.SurfaceRouteGroup]runtimehttp.SurfaceRouteGroupPolicy{
			runtimehttp.SurfaceRouteGroupPublic: {
				MaxJSONBodyBytes: int64(dependencies.Config.HTTPPublicMaxJSONBodyBytes), RequestTimeout: dependencies.Config.HTTPPublicRequestTimeout,
				RateLimitPerMinute: dependencies.Config.HTTPPublicRateLimitPerMinute, AuditClass: "public_surface",
				AllowedOrigins: append(append([]string(nil), dependencies.Config.SurfaceBusinessOrigins...), dependencies.Config.SurfacePortalOrigins...),
			},
			runtimehttp.SurfaceRouteGroupTenantAdmin: {
				MaxJSONBodyBytes: int64(dependencies.Config.HTTPTenantAdminMaxJSONBodyBytes), RequestTimeout: dependencies.Config.HTTPTenantAdminRequestTimeout,
				RateLimitPerMinute: dependencies.Config.HTTPTenantAdminRateLimitPerMinute, AuditClass: "tenant_governance",
				AllowedOrigins: append([]string(nil), dependencies.Config.SurfaceAdminOrigins...),
			},
			runtimehttp.SurfaceRouteGroupOps: {
				MaxJSONBodyBytes: int64(dependencies.Config.HTTPOpsMaxJSONBodyBytes), RequestTimeout: dependencies.Config.HTTPOpsRequestTimeout,
				RateLimitPerMinute: dependencies.Config.HTTPOpsRateLimitPerMinute, AuditClass: "privileged_operations",
				AllowedOrigins: append([]string(nil), dependencies.Config.SurfaceAdminOrigins...),
			},
		},
		CapacityLimits:           capacityplatform.Limits{GlobalInFlight: dependencies.Config.CapacityGlobalInFlight, WorkspaceInFlight: dependencies.Config.CapacityWorkspaceInFlight, UseCaseInFlight: dependencies.Config.CapacityUseCaseInFlight, RetryInFlight: dependencies.Config.CapacityRetryInFlight, GlobalRate: dependencies.Config.CapacityGlobalRatePerMinute, WorkspaceRate: dependencies.Config.CapacityWorkspaceRatePerMinute, UseCaseRate: dependencies.Config.CapacityUseCaseRatePerMinute, RateWindow: time.Minute, MaxWorkspaceStates: dependencies.Config.CapacityMaxWorkspaceStates, MaxUseCaseStates: dependencies.Config.CapacityMaxUseCaseStates, WorkspaceStateTTL: dependencies.Config.CapacityWorkspaceStateTTL, DegradedRatio: dependencies.Config.CapacityDegradedRatio, RecoveryRatio: dependencies.Config.CapacityRecoveryRatio, RetryAfter: dependencies.Config.CapacityRetryAfter},
		BusinessEventReplayLimit: dependencies.Config.BusinessEventReplayLimit, BusinessEventSubscriberBuffer: dependencies.Config.BusinessEventSubscriberBuffer,
		BusinessEventGlobalConnections: dependencies.Config.BusinessEventGlobalConnections, BusinessEventWorkspaceConnections: dependencies.Config.BusinessEventWorkspaceConnections,
		BusinessEventPrincipalConnections: dependencies.Config.BusinessEventPrincipalConnections, BusinessEventHeartbeatInterval: dependencies.Config.BusinessEventHeartbeatInterval,
		BusinessEventRetryInterval: dependencies.Config.BusinessEventRetryInterval,
	}, runtimehttp.HTTPRouterDependencies{
		IdentityAuthentication: identityAuthentication, IdentityPrincipal: principalmodel.NewPrincipalFromIdentity, IntegrationAuthentication: integrationAuthentication,
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
		SecurityAudit: records.Applications().Audit, RuntimeStatus: runtimeStatusProvider(dependencies),
		TechnicalMetrics: func(ctx context.Context) string {
			workerMetrics, agentTaskMetrics, agentInteractiveMetrics := runtimeOptionalWorkerMetrics(ctx, dependencies.WorkerControl, records.Applications().AgentTaskWorker, records.Applications().AgentInteractiveRuns)
			return runtimeTechnicalOpenMetrics(ctx, dependencies.Store, records.Applications().RuntimeStatus) + workerMetrics + agentTaskMetrics + agentInteractiveMetrics
		},
		WorkerControl:           dependencies.WorkerControl,
		RuntimeInstanceID:       dependencies.RuntimeInstanceID,
		OperationsControlState:  runtimeOperationsControlState(dependencies.Store),
		RuntimeReleaseAdmission: dependencies.ReleaseAdmission,
		RuntimeReleaseIntegrity: dependencies.ReleaseIntegrity,
		BusinessEventBackplane:  backplane,
	})
	assembly := &httpServerAssembly{
		dependencies: dependencies, server: server, callbacks: server.HandlerCallbacks(),
		recordQueries: recordApplication, publications: publications,
		metadata: records.Applications().ApplicationSchema, identityHTTP: identityAuthentication,
		principals: identityPrincipals,
	}
	assembly.wireOperationsApplication()
	assembly.wirePartyAndIdentityReferences(ctx)
	assembly.wireRecordAndProcessHandlers()
	assembly.wireMetadataAndBusinessHandlers()
	assembly.wireRuntimeIntegrationGateway()
	assembly.wireWorkspaceProvisioning()
	assembly.wireIntegrationAndAgentHandlers(dependencies.Config.AgentDialogRateLimitPerMinute)
	server = runtimehttp.UseHandlers(server, assembly.handlers)
	server = runtimehttp.UseServiceIdentity(server, dependencies.Config.RuntimeVersion)
	server = runtimehttp.UseRuntimeReleaseIdentity(server, dependencies.ReleaseIdentity)
	return runtimehttp.UseManifest(server, dependencies.Manifest)
}

func (a *httpServerAssembly) wireWorkspaceProvisioning() {
	if a.dependencies.Store == nil {
		return
	}
	repository := workspaceprovisionpersistence.NewWorkspaceProvisionStoreWithFailureInjector(
		a.dependencies.Store,
		a.dependencies.IdentityBinding,
		a.dependencies.Manifest,
		workspaceprovisionpersistence.NewAcceptanceFailureInjector(a.dependencies.Config.WorkspaceProvisionFailurePoint),
	)
	service := workspaceprovisionapplication.NewWorkspaceProvisionApplicationService(repository)
	a.handlers.WorkspaceProvision = workspaceprovisionhttp.NewWorkspaceProvisionHandler(workspaceprovisionhttp.WorkspaceProvisionDependencies{
		UseCases: service, Principal: a.callbacks.Principal, DecodeJSON: a.callbacks.DecodeJSON,
		WriteJSON: a.callbacks.WriteJSON, WriteServiceError: a.callbacks.WriteServiceError,
		ProvisionGuard: a.identityHTTP.PermissionFunc(workspaceprovisionapplication.Permission),
		ReconcileGuard: a.identityHTTP.PermissionFunc(workspaceprovisionapplication.ReconcilePermission),
		SecurityAudit:  a.callbacks.SecurityAudit,
	})
}

// wireRuntimeIntegrationGateway exposes only the Runtime-owned Integration
// Outbox handoff read. Product HTTP is exposed by the Integration deployment.
func (a *httpServerAssembly) wireRuntimeIntegrationGateway() {
	if a.dependencies.Store == nil || a.dependencies.IntegrationBinding == nil {
		return
	}
	service := integrationapplication.NewIntegrationApplicationService(integrationapplication.ApplicationDependencies{
		PublicationRepository: publicationhandoffpersistence.NewPublicationStore(a.dependencies.Store),
	})
	a.handlers.Integrations = integrationhttp.NewIntegrationsHandler(integrationhttp.IntegrationsDependencies{
		RuntimeExecution: service,
		Principal:        a.callbacks.Principal, WriteJSON: a.callbacks.WriteJSON,
		WriteServiceError: a.callbacks.WriteServiceError,
		Admin:             a.identityHTTP.PermissionFunc("workspace.admin"),
		Authenticated:     a.identityHTTP.AuthenticatedFunc,
	})
}

func runtimeStatusProvider(dependencies HTTPServerDependencies) runtimehttp.DeploymentRuntimeStatusProvider {
	if dependencies.MonitoringBinding != nil {
		return dependencies.MonitoringBinding
	}
	return dependencies.Records.Applications().RuntimeStatus
}

func runtimeOptionalWorkerMetrics(ctx context.Context, workers *workerplatform.Controller, agentTasks *agentapplication.AgentTaskWorker, interactive *agentapplication.AgentInteractiveRunApplicationService) (string, string, string) {
	workerMetrics := ""
	if workers != nil {
		workerMetrics = workers.OpenMetrics()
	}
	agentTaskMetrics := ""
	if agentTasks != nil {
		agentTaskMetrics = agentTasks.OpenMetrics()
	}
	agentInteractiveMetrics := ""
	if interactive != nil {
		agentInteractiveMetrics = interactive.OpenMetrics(ctx)
	}
	return workerMetrics, agentTaskMetrics, agentInteractiveMetrics
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
