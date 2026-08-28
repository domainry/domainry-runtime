package transport

import (
	"context"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"strings"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityprincipal "github.com/domainry/domainry-identity-sdk/authorization/principal"
	identityhttpmiddleware "github.com/domainry/domainry-identity-sdk/httpmiddleware"

	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	metadataapplication "github.com/domainry/domainry-runtime/runtime/application/metadata"
	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	principalapplication "github.com/domainry/domainry-runtime/runtime/application/principal"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	capacityplatform "github.com/domainry/domainry-runtime/runtime/platform/capacity"

	notificationapplication "github.com/domainry/domainry-runtime/runtime/application/notification"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	businesseventcontract "github.com/domainry/domainry-runtime/runtime/domain/businessevent/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	businesseventmemory "github.com/domainry/domainry-runtime/runtime/infrastructure/broadcast/memory"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	operationspersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/operations"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
)

type HTTPServerDependencies struct {
	Config                 config.Config
	Records                *composition.RuntimeServices
	IdentityBinding        identitysdk.Binding
	Store                  *persistence.RuntimeStore
	RateLimiter            ratelimit.Limiter
	Notifications          *notificationapplication.NotificationApplicationService
	Manifest               manifestmodel.ManifestSchema
	WorkerControl          *workerplatform.Controller
	Clock                  identitysdk.Clock
	RuntimeInstanceID      string
	ReleaseIdentity        runtimehttp.RuntimeReleaseIdentity
	ReleaseAdmission       runtimehttp.RuntimeReleaseAdmissionProvider
	ReleaseIntegrity       runtimehttp.RuntimeReleaseIntegrityProvider
	BusinessEventBackplane businesseventcontract.Backplane
}

type httpServerAssembly struct {
	dependencies  HTTPServerDependencies
	server        *runtimehttp.HTTPRouter
	callbacks     runtimehttp.HandlerCallbacks
	handlers      runtimehttp.HTTPRouterHandlers
	recordQueries *recordapplication.RecordApplicationService
	integrations  *integrationapplication.IntegrationApplicationService
	metadata      *metadataapplication.MetadataApplicationService
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
	integrations := records.Applications().Integrations
	integrations.UseConnectorCapacity(ctx, capacityplatform.NewController(capacityplatform.Limits{GlobalInFlight: dependencies.Config.CapacityConnectorGlobalInFlight, WorkspaceInFlight: dependencies.Config.CapacityConnectorWorkspaceInFlight, UseCaseInFlight: dependencies.Config.CapacityConnectorProviderInFlight, RetryInFlight: dependencies.Config.CapacityRetryInFlight, GlobalRate: dependencies.Config.CapacityConnectorGlobalRatePerMinute, WorkspaceRate: dependencies.Config.CapacityConnectorWorkspaceRatePerMinute, UseCaseRate: dependencies.Config.CapacityConnectorProviderRatePerMinute, RateWindow: time.Minute, MaxWorkspaceStates: dependencies.Config.CapacityMaxWorkspaceStates, MaxUseCaseStates: 256, WorkspaceStateTTL: dependencies.Config.CapacityWorkspaceStateTTL, DegradedRatio: dependencies.Config.CapacityDegradedRatio, RecoveryRatio: dependencies.Config.CapacityRecoveryRatio, RetryAfter: dependencies.Config.CapacityRetryAfter}, nil))
	integrations.UseQueueBackpressureThresholds(ctx, dependencies.Config.CapacityQueueDepthThreshold, dependencies.Config.CapacityQueueOldestAgeThreshold)
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
		IdentityAuthentication: identityAuthentication, IdentityPrincipal: principalmodel.NewPrincipalFromIdentity, IntegrationAuthentication: integrations,
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
		SecurityAudit: records.Applications().Audit, RuntimeStatus: records.Applications().RuntimeStatus,
		TechnicalMetrics: func(ctx context.Context) string {
			workerMetrics, agentTaskMetrics, agentInteractiveMetrics := runtimeOptionalWorkerMetrics(ctx, dependencies.WorkerControl, records.Applications().AgentTaskWorker, records.Applications().AgentInteractiveRuns)
			return runtimeTechnicalOpenMetrics(ctx, dependencies.Store, records.Applications().RuntimeStatus) + integrations.OperationalMetricsOpenMetrics(ctx) + records.Applications().Records.BatchJobOpenMetrics(ctx) + workerMetrics + agentTaskMetrics + agentInteractiveMetrics
		},
		Backpressure: integrations.QueueBackpressureActive, WorkerControl: dependencies.WorkerControl,
		RuntimeInstanceID:       dependencies.RuntimeInstanceID,
		OperationsControlState:  runtimeOperationsControlState(dependencies.Store),
		RuntimeReleaseAdmission: dependencies.ReleaseAdmission,
		RuntimeReleaseIntegrity: dependencies.ReleaseIntegrity,
		BusinessEventBackplane:  backplane,
	})
	assembly := &httpServerAssembly{
		dependencies: dependencies, server: server, callbacks: server.HandlerCallbacks(),
		recordQueries: recordApplication, integrations: integrations,
		metadata: records.Applications().Metadata, identityHTTP: identityAuthentication,
		principals: identityPrincipals,
	}
	assembly.wireOperationsApplication()
	assembly.wirePartyAndIdentityReferences(ctx)
	assembly.wireRecordAndProcessHandlers()
	assembly.wireMetadataAndBusinessHandlers()
	assembly.wireIntegrationAndAgentHandlers(normalizeAgentHTTPConfig(dependencies.Config))
	server = runtimehttp.UseHandlers(server, assembly.handlers)
	server = runtimehttp.UseServiceIdentity(server, dependencies.Config.RuntimeVersion)
	server = runtimehttp.UseRuntimeReleaseIdentity(server, dependencies.ReleaseIdentity)
	return runtimehttp.UseManifest(server, dependencies.Manifest)
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

func normalizeAgentHTTPConfig(runtimeConfig config.Config) runtimehttp.AgentHTTPConfig {
	config := runtimehttp.AgentHTTPConfig{
		BaseURL: runtimeConfig.AgentHTTPBaseURL, APIKey: runtimeConfig.AgentHTTPAPIKey,
		AgentID: runtimeConfig.AgentHTTPAgentID, Timeout: runtimeConfig.AgentHTTPTimeout,
		RateLimitPerMinute: runtimeConfig.AgentHTTPRateLimitPerMinute,
	}
	config.BaseURL = strings.TrimRight(strings.TrimSpace(config.BaseURL), "/")
	if config.BaseURL == "" {
		config.BaseURL = "https://integration.domainry.ai"
	}
	if config.Timeout <= 0 {
		config.Timeout = 120 * time.Second
	}
	if config.RateLimitPerMinute <= 0 {
		config.RateLimitPerMinute = 60
	}
	return config
}
