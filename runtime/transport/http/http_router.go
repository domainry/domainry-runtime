package http

import (
	"context"
	"net/http"
	"strings"
	"time"

	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulehttp"
	identitysdk "github.com/domainry/domainry-identity-sdk"

	capacityplatform "github.com/domainry/domainry-foundation/capacity"
	"github.com/domainry/domainry-foundation/ratelimit"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	businesseventapplication "github.com/domainry/domainry-runtime/runtime/application/businessevent"
	businesssystemapplication "github.com/domainry/domainry-runtime/runtime/application/businesssystem"
	actionservice "github.com/domainry/domainry-runtime/runtime/domain/action/service"
	capabilitybusiness "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	endpointmodel "github.com/domainry/domainry-runtime/runtime/domain/endpoint/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	"github.com/domainry/domainry-runtime/runtime/platform/productbrand"
	businesseventhttp "github.com/domainry/domainry-runtime/runtime/transport/http/businessevents"
	businesssystemhttp "github.com/domainry/domainry-runtime/runtime/transport/http/businesssystem"
	"go.uber.org/zap"
)

const (
	BusinessRuntimeServiceKind        = "domain-runtime"
	BusinessRuntimeAPIContractVersion = capabilitybusiness.RuntimeAPIContractVersion
)

func BusinessRuntimeAPIContractHash() string {
	return capabilitybusiness.RuntimeAPIContractHash()
}

type httpRouteRegistrar interface {
	RegisterRoutes(*http.ServeMux)
}

type HTTPRouter struct {
	recordHTTP                       httpRouteRegistrar
	uploadHTTP                       httpRouteRegistrar
	discoveryHTTP                    httpRouteRegistrar
	openAPIHTTP                      httpRouteRegistrar
	operationsHTTP                   httpRouteRegistrar
	lifecycleHTTP                    httpRouteRegistrar
	workflowHTTP                     httpRouteRegistrar
	automationHTTP                   httpRouteRegistrar
	dispatchHTTP                     httpRouteRegistrar
	businessReferenceHTTP            httpRouteRegistrar
	publicationHandoffHTTP           httpRouteRegistrar
	businessSystemHTTP               httpRouteRegistrar
	applicationSchemaHTTP            httpRouteRegistrar
	notificationHTTP                 httpRouteRegistrar
	authorizationActions             func() *actioncontract.Registry
	identityAuthorization            identitysdk.PrincipalResolver
	businessPrincipal                BusinessPrincipalResolver
	workspaceAdmission               WorkspaceAdmission
	identityAuthentication           IdentityRequestMiddleware
	identityPrincipal                IdentityPrincipalProjection
	integrationAuth                  IntegrationAuthenticationPrincipalProvider
	securityAudit                    SecurityAuditAppender
	runtimeStatus                    DeploymentRuntimeStatusProvider
	corsAllowedOrigins               []string
	listenerGroupPolicies            map[ListenerRouteGroup]ListenerRouteGroupPolicy
	listenerGroupCapacity            map[ListenerRouteGroup]*capacityplatform.Controller
	rateLimiter                      ratelimit.Limiter
	allowDevAuthHeaders              bool
	technicalMetrics                 TechnicalMetricsProvider
	httpMetrics                      HTTPMetricsCollector
	healthRegistry                   *runtimeHealthRegistry
	healthCheckTimeout               time.Duration
	maxJSONBodyBytes                 int64
	capacityController               *capacityplatform.Controller
	requestTimeout                   time.Duration
	backpressure                     func(context.Context) bool
	operationsControlState           OperationsControlStateProvider
	runtimeReleaseAdmission          RuntimeReleaseAdmissionProvider
	runtimeReleaseIntegrity          RuntimeReleaseIntegrityProvider
	runtimeInstanceID                string
	workspaceProvisionClientID       string
	workspaceProvisionSigningSecret  []byte
	workerControl                    *workerplatform.Controller
	businessEvents                   *businesseventapplication.BusinessEventApplicationService
	businessEventHTTP                *businesseventhttp.BusinessEventsHandler
	workspaceProvisionHTTP           httpRouteRegistrar
	serviceKind                      string
	productBrandName                 string
	runtimeVersion                   string
	apiContractVersion               string
	apiContractHash                  string
	manifestTemplateID               string
	manifestHash                     string
	manifest                         manifestmodel.ManifestSchema
	releaseIdentity                  RuntimeReleaseIdentity
	moduleHTTPRoutes                 map[string]moduleHTTPRoute
	runtimeAuthoringScenarioReceipts *businesssystemapplication.RuntimeAuthoringScenarioReceiptService
}

func NewHTTPRouter(config HTTPRouterConfig, deps HTTPRouterDependencies) *HTTPRouter {
	if err := validateCompiledEndpointContracts(); err != nil {
		panic("invalid compiled Runtime endpoint contract: " + err.Error())
	}
	controller := capacityplatform.NewController(config.CapacityLimits, func(event capacityplatform.Event) {
		zap.L().Warn("runtime capacity state", zap.String("code", event.Code), zap.String("state", string(event.State)), zap.String("dimension", string(event.Dimension)), zap.Int("current", event.Current), zap.Int("limit", event.Limit))
	})
	authorizationActions := deps.AuthorizationActions
	if authorizationActions == nil {
		contributedActions, err := modulehttp.AuthorizationActionsFromAdapters(deps.ModuleHTTPAdapters)
		if err != nil {
			panic("assemble Runtime HTTP authorization contributions: " + err.Error())
		}
		registry, err := actionservice.BuildAuthorizationRegistry(actionservice.AuthorizationRegistryInput{
			ApplicationKey: "runtime-http", ContributedActions: contributedActions, EndpointContracts: runtimeEndpointContracts,
		})
		if err != nil {
			panic("assemble Runtime HTTP authorization registry: " + err.Error())
		}
		authorizationActions = func() *actioncontract.Registry { return registry }
	}
	router := &HTTPRouter{
		identityAuthorization: deps.IdentityAuthorization, businessPrincipal: deps.BusinessPrincipal, workspaceAdmission: deps.WorkspaceAdmission,
		authorizationActions:   authorizationActions,
		identityAuthentication: deps.IdentityAuthentication, identityPrincipal: deps.IdentityPrincipal, integrationAuth: deps.IntegrationAuthentication,
		securityAudit: deps.SecurityAudit, runtimeStatus: deps.RuntimeStatus,
		workspaceProvisionClientID:      config.WorkspaceProvisionClientID,
		workspaceProvisionSigningSecret: []byte(config.WorkspaceProvisionSigningSecret),
		corsAllowedOrigins:              normalizeCORSOrigins(config.CORSAllowedOrigins), allowDevAuthHeaders: config.AllowDevAuthHeaders,
		listenerGroupPolicies: config.ListenerGroupPolicies,
		listenerGroupCapacity: map[ListenerRouteGroup]*capacityplatform.Controller{}, rateLimiter: deps.RateLimiter,
		technicalMetrics: deps.TechnicalMetrics,
		httpMetrics:      NewMemoryHTTPMetricsCollector(defaultHTTPMetricsMaxSeries), serviceKind: BusinessRuntimeServiceKind,
		productBrandName: productbrand.ResolveName(config.ProductBrandName),
		healthRegistry:   newRuntimeHealthRegistry(), healthCheckTimeout: config.HealthCheckTimeout, maxJSONBodyBytes: config.MaxJSONBodyBytes,
		capacityController: controller, requestTimeout: config.RequestTimeout, backpressure: deps.Backpressure, workerControl: deps.WorkerControl,
		operationsControlState: deps.OperationsControlState, runtimeInstanceID: strings.TrimSpace(deps.RuntimeInstanceID),
		runtimeReleaseAdmission: deps.RuntimeReleaseAdmission,
		runtimeReleaseIntegrity: deps.RuntimeReleaseIntegrity,
		runtimeVersion:          "dev", apiContractVersion: BusinessRuntimeAPIContractVersion, apiContractHash: BusinessRuntimeAPIContractHash(),
		moduleHTTPRoutes:                 buildModuleHTTPRouteIndex(deps.ModuleHTTPAdapters),
		runtimeAuthoringScenarioReceipts: deps.RuntimeAuthoringScenarioReceipts,
	}
	for group, policy := range config.ListenerGroupPolicies {
		if policy.RateLimitPerMinute <= 0 {
			continue
		}
		if router.rateLimiter != nil {
			continue
		}
		router.listenerGroupCapacity[group] = capacityplatform.NewController(capacityplatform.Limits{
			GlobalRate: policy.RateLimitPerMinute,
			RateWindow: time.Minute,
		}, nil)
	}
	router.configureBusinessEvents(config, deps.BusinessEventBackplane)
	return router
}

func UseServiceIdentity(server *HTTPRouter, runtimeVersion string) *HTTPRouter {
	if value := strings.TrimSpace(runtimeVersion); value != "" {
		server.runtimeVersion = value
	}
	return server
}

func UseRuntimeReleaseIdentity(server *HTTPRouter, identity RuntimeReleaseIdentity) *HTTPRouter {
	server.releaseIdentity = identity
	return server
}

func UseManifestIdentity(server *HTTPRouter, templateID, manifestHash string) *HTTPRouter {
	server.manifestTemplateID = strings.TrimSpace(templateID)
	server.manifestHash = strings.TrimSpace(manifestHash)
	return server
}

func UseManifest(server *HTTPRouter, manifest manifestmodel.ManifestSchema) *HTTPRouter {
	server.manifest = manifest
	return UseManifestIdentity(server, manifest.TemplateID, manifest.ManifestHash)
}

func (s *HTTPRouter) BusinessSystemRuntimeMetadata() businesssystemhttp.RuntimeMetadata {
	return businesssystemhttp.RuntimeMetadata{
		ServiceKind: s.serviceKind, RuntimeVersion: s.runtimeVersion,
		APIContractVersion: s.apiContractVersion, APIContractHash: s.apiContractHash,
		ManifestHash: s.manifestHash, Manifest: s.manifest,
	}
}

func UseHTTPMetricsCollector(server *HTTPRouter, collector HTTPMetricsCollector) *HTTPRouter {
	if collector != nil {
		server.httpMetrics = collector
	}
	return server
}

type HTTPRequestMetric struct {
	Method             string   `json:"method"`
	Route              string   `json:"route"`
	StatusClass        string   `json:"status_class"`
	Count              int64    `json:"count"`
	ErrorCount         int64    `json:"error_count"`
	DurationSecondsSum float64  `json:"duration_seconds_sum"`
	DurationBuckets    []uint64 `json:"duration_buckets"`
}

type ListenerRouteGroup string

const (
	ListenerRouteGroupAll        ListenerRouteGroup = "all"
	ListenerRouteGroupPublic     ListenerRouteGroup = "public"
	ListenerRouteGroupManagement ListenerRouteGroup = "management"
	ListenerRouteGroupOps        ListenerRouteGroup = "ops"
)

func (s *HTTPRouter) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /metrics", s.metrics)
	mux.HandleFunc("GET /", s.apiInfo)
	s.registerProbeRoutes(mux)
	s.discoveryHTTP.RegisterRoutes(mux)
	s.openAPIHTTP.RegisterRoutes(mux)
	runOptionalRouteRegistrar(s.businessEventHTTP != nil, func() { s.businessEventHTTP.RegisterRoutes(mux) })
	s.applicationSchemaHTTP.RegisterRoutes(mux)
	s.businessSystemHTTP.RegisterRoutes(mux)
	s.businessReferenceHTTP.RegisterRoutes(mux)
	runOptionalRouteRegistrar(s.publicationHandoffHTTP != nil, func() { s.publicationHandoffHTTP.RegisterRoutes(mux) })
	runOptionalRouteRegistrar(s.notificationHTTP != nil, func() { s.notificationHTTP.RegisterRoutes(mux) })
	s.uploadHTTP.RegisterRoutes(mux)
	s.recordHTTP.RegisterRoutes(mux)
	s.workflowHTTP.RegisterRoutes(mux)
	s.automationHTTP.RegisterRoutes(mux)
	s.dispatchHTTP.RegisterRoutes(mux)
	s.operationsHTTP.RegisterRoutes(mux)
	runOptionalRouteRegistrar(s.lifecycleHTTP != nil, func() { s.lifecycleHTTP.RegisterRoutes(mux) })
	runOptionalRouteRegistrar(s.workspaceProvisionHTTP != nil, func() { s.workspaceProvisionHTTP.RegisterRoutes(mux) })
	s.registerModuleHTTPRoutes(mux)
	s.registerFallbackRoutes(mux)
	admitted := s.withAdmission(mux, mux)
	controlled := s.withOperationalControls(mux, admitted)
	published := s.withBusinessEventPublication(controlled)
	highRiskAuthorized := s.withHighRiskOperationPolicy(mux, published)
	actionAuthorized := s.withActionAuthorization(mux, highRiskAuthorized)
	authenticated := s.withAuth(mux, actionAuthorized)
	return s.withMetrics(s.withRecovery(s.withCORS(s.withRuntimeAuthoringScenarioEvidence(authenticated))))
}

// RoutesForListenerGroup builds an externally attached listener mux from the
// endpoint exposure inventory. Listener exposure is a deployment boundary and
// is independent of frontend product shells and request identity.
func (s *HTTPRouter) RoutesForListenerGroup(group ListenerRouteGroup) http.Handler {
	if group == ListenerRouteGroupAll {
		return s.Routes()
	}
	exposure, ok := listenerExposure(group)
	if !ok {
		return http.HandlerFunc(s.notFound)
	}
	if s.httpMetrics != nil {
		s.httpMetrics.RegisterListenerGroup(string(group), ListenerRouteGroupEndpointCount(group))
	}
	full := s.Routes()
	mux := http.NewServeMux()
	for route, contract := range runtimeEndpointContracts {
		if !endpointVisibleOnListener(contract, exposure) {
			continue
		}
		method, path, ok := strings.Cut(route, " ")
		if !ok || method == "" || path == "" {
			continue
		}
		if method == http.MethodGet && path == "/openapi.json" {
			continue
		}
		if path == "/" {
			path = "/{$}"
		}
		mux.Handle(method+" "+path, full)
	}
	for identity, binding := range s.moduleHTTPRoutes {
		if _, ownedByRuntime := runtimeEndpointContracts[identity]; ownedByRuntime || !moduleRouteVisibleOnListener(binding.route, group) {
			continue
		}
		mux.Handle(identity, full)
	}
	mux.Handle("GET /openapi.json", s.listenerOpenAPIHandler(group, full))
	mux.HandleFunc("/{path...}", s.notFound)
	return s.withListenerRouteGroupPolicy(group, mux)
}

func listenerExposure(group ListenerRouteGroup) (endpointmodel.ListenerExposure, bool) {
	switch group {
	case ListenerRouteGroupPublic:
		return endpointmodel.ListenerExposurePublic, true
	case ListenerRouteGroupManagement:
		return endpointmodel.ListenerExposureManagement, true
	case ListenerRouteGroupOps:
		return endpointmodel.ListenerExposureOps, true
	default:
		return "", false
	}
}

func ListenerRouteGroupEndpointCount(group ListenerRouteGroup) int {
	if group == ListenerRouteGroupAll {
		return len(runtimeEndpointContracts)
	}
	exposure, ok := listenerExposure(group)
	if !ok {
		return 0
	}
	count := 0
	for _, contract := range runtimeEndpointContracts {
		if endpointVisibleOnListener(contract, exposure) {
			count++
		}
	}
	return count
}

func runOptionalRouteRegistrar(enabled bool, registrar func()) {
	if enabled {
		registrar()
	}
}
