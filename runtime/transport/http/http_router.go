package http

import (
	"context"
	"net/http"
	"strings"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	businesseventapplication "github.com/domainry/domainry-runtime/runtime/application/businessevent"
	capabilitybusiness "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	surfacemodel "github.com/domainry/domainry-runtime/runtime/domain/surface/model"
	capacityplatform "github.com/domainry/domainry-runtime/runtime/platform/capacity"
	healthplatform "github.com/domainry/domainry-runtime/runtime/platform/health"
	"github.com/domainry/domainry-runtime/runtime/platform/productbrand"
	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
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
	recordHTTP              httpRouteRegistrar
	surfaceContextHTTP      httpRouteRegistrar
	uploadHTTP              httpRouteRegistrar
	discoveryHTTP           httpRouteRegistrar
	openAPIHTTP             httpRouteRegistrar
	operationsHTTP          httpRouteRegistrar
	lifecycleHTTP           httpRouteRegistrar
	workflowHTTP            httpRouteRegistrar
	automationHTTP          httpRouteRegistrar
	schedulerHTTP           httpRouteRegistrar
	reportHTTP              httpRouteRegistrar
	frontendCapabilityHTTP  httpRouteRegistrar
	businessReferenceHTTP   httpRouteRegistrar
	businessSystemHTTP      httpRouteRegistrar
	capabilityHTTP          httpRouteRegistrar
	applicationSchemaHTTP   httpRouteRegistrar
	notificationHTTP        httpRouteRegistrar
	partyHTTP               httpRouteRegistrar
	identityAuthorization   identitysdk.PrincipalResolver
	businessPrincipal       BusinessPrincipalResolver
	identityAuthentication  IdentityRequestMiddleware
	identityPrincipal       IdentityPrincipalProjection
	integrationAuth         IntegrationAuthenticationPrincipalProvider
	securityAudit           SecurityAuditAppender
	runtimeStatus           DeploymentRuntimeStatusProvider
	corsAllowedOrigins      []string
	surfaceGroupPolicies    map[SurfaceRouteGroup]SurfaceRouteGroupPolicy
	surfaceGroupCapacity    map[SurfaceRouteGroup]*capacityplatform.Controller
	rateLimiter             ratelimit.Limiter
	allowDevAuthHeaders     bool
	agentDialogHTTP         httpRouteRegistrar
	technicalMetrics        TechnicalMetricsProvider
	httpMetrics             HTTPMetricsCollector
	healthRegistry          *healthplatform.Registry
	healthCheckTimeout      time.Duration
	maxJSONBodyBytes        int64
	capacityController      *capacityplatform.Controller
	requestTimeout          time.Duration
	backpressure            func(context.Context) bool
	operationsControlState  OperationsControlStateProvider
	runtimeReleaseAdmission RuntimeReleaseAdmissionProvider
	runtimeReleaseIntegrity RuntimeReleaseIntegrityProvider
	runtimeInstanceID       string
	workerControl           *workerplatform.Controller
	businessEvents          *businesseventapplication.BusinessEventApplicationService
	businessEventHTTP       *businesseventhttp.BusinessEventsHandler
	serviceKind             string
	productBrandName        string
	runtimeVersion          string
	apiContractVersion      string
	apiContractHash         string
	manifestTemplateID      string
	manifestHash            string
	manifest                manifestmodel.ManifestSchema
	releaseIdentity         RuntimeReleaseIdentity
}

func NewHTTPRouter(config HTTPRouterConfig, deps HTTPRouterDependencies) *HTTPRouter {
	if err := validateCompiledEndpointSurfaceContracts(); err != nil {
		panic("invalid compiled Runtime endpoint Surface contract: " + err.Error())
	}
	controller := capacityplatform.NewController(config.CapacityLimits, func(event capacityplatform.Event) {
		zap.L().Warn("runtime capacity state", zap.String("code", event.Code), zap.String("state", string(event.State)), zap.String("dimension", string(event.Dimension)), zap.Int("current", event.Current), zap.Int("limit", event.Limit))
	})
	router := &HTTPRouter{
		identityAuthorization: deps.IdentityAuthorization, businessPrincipal: deps.BusinessPrincipal,
		identityAuthentication: deps.IdentityAuthentication, identityPrincipal: deps.IdentityPrincipal, integrationAuth: deps.IntegrationAuthentication,
		securityAudit: deps.SecurityAudit, runtimeStatus: deps.RuntimeStatus,
		corsAllowedOrigins: normalizeCORSOrigins(config.CORSAllowedOrigins), allowDevAuthHeaders: config.AllowDevAuthHeaders,
		surfaceGroupPolicies: config.SurfaceGroupPolicies,
		surfaceGroupCapacity: map[SurfaceRouteGroup]*capacityplatform.Controller{}, rateLimiter: deps.RateLimiter,
		technicalMetrics: deps.TechnicalMetrics,
		httpMetrics:      NewMemoryHTTPMetricsCollector(defaultHTTPMetricsMaxSeries), serviceKind: BusinessRuntimeServiceKind,
		productBrandName: productbrand.ResolveName(config.ProductBrandName),
		healthRegistry:   healthplatform.NewRegistry(), healthCheckTimeout: config.HealthCheckTimeout, maxJSONBodyBytes: config.MaxJSONBodyBytes,
		capacityController: controller, requestTimeout: config.RequestTimeout, backpressure: deps.Backpressure, workerControl: deps.WorkerControl,
		operationsControlState: deps.OperationsControlState, runtimeInstanceID: strings.TrimSpace(deps.RuntimeInstanceID),
		runtimeReleaseAdmission: deps.RuntimeReleaseAdmission,
		runtimeReleaseIntegrity: deps.RuntimeReleaseIntegrity,
		runtimeVersion:          "dev", apiContractVersion: BusinessRuntimeAPIContractVersion, apiContractHash: BusinessRuntimeAPIContractHash(),
	}
	for group, policy := range config.SurfaceGroupPolicies {
		if policy.RateLimitPerMinute <= 0 {
			continue
		}
		if router.rateLimiter != nil {
			continue
		}
		router.surfaceGroupCapacity[group] = capacityplatform.NewController(capacityplatform.Limits{
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

type SurfaceRouteGroup string

const (
	SurfaceRouteGroupAll         SurfaceRouteGroup = "all"
	SurfaceRouteGroupPublic      SurfaceRouteGroup = "public"
	SurfaceRouteGroupTenantAdmin SurfaceRouteGroup = "tenant-admin"
	SurfaceRouteGroupOps         SurfaceRouteGroup = "ops"
)

func (s *HTTPRouter) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /metrics", s.metrics)
	mux.HandleFunc("GET /", s.apiInfo)
	s.registerProbeRoutes(mux)
	runOptionalRouteRegistrar(s.partyHTTP != nil, func() { s.partyHTTP.RegisterRoutes(mux) })
	s.discoveryHTTP.RegisterRoutes(mux)
	s.openAPIHTTP.RegisterRoutes(mux)
	s.reportHTTP.RegisterRoutes(mux)
	runOptionalRouteRegistrar(s.businessEventHTTP != nil, func() { s.businessEventHTTP.RegisterRoutes(mux) })
	runOptionalRouteRegistrar(s.agentDialogHTTP != nil, func() { s.agentDialogHTTP.RegisterRoutes(mux) })
	s.applicationSchemaHTTP.RegisterRoutes(mux)
	s.capabilityHTTP.RegisterRoutes(mux)
	s.frontendCapabilityHTTP.RegisterRoutes(mux)
	s.businessSystemHTTP.RegisterRoutes(mux)
	s.businessReferenceHTTP.RegisterRoutes(mux)
	runOptionalRouteRegistrar(s.notificationHTTP != nil, func() { s.notificationHTTP.RegisterRoutes(mux) })
	s.uploadHTTP.RegisterRoutes(mux)
	s.surfaceContextHTTP.RegisterRoutes(mux)
	s.recordHTTP.RegisterRoutes(mux)
	s.workflowHTTP.RegisterRoutes(mux)
	s.automationHTTP.RegisterRoutes(mux)
	s.schedulerHTTP.RegisterRoutes(mux)
	s.operationsHTTP.RegisterRoutes(mux)
	runOptionalRouteRegistrar(s.lifecycleHTTP != nil, func() { s.lifecycleHTTP.RegisterRoutes(mux) })
	s.registerFallbackRoutes(mux)
	admitted := s.withAdmission(mux, mux)
	controlled := s.withOperationalControls(mux, admitted)
	published := s.withBusinessEventPublication(controlled)
	highRiskAuthorized := s.withHighRiskOperationPolicy(mux, published)
	authenticated := s.withAuth(mux, highRiskAuthorized)
	return s.withMetrics(s.withRecovery(s.withCORS(authenticated)))
}

// RoutesForSurfaceGroup builds the externally attached listener mux from the
// compiled route inventory. The listener mux itself only registers endpoint
// patterns owned by the requested Surface group; unknown or cross-group paths
// never reach the full Runtime router.
func (s *HTTPRouter) RoutesForSurfaceGroup(group SurfaceRouteGroup) http.Handler {
	if group == SurfaceRouteGroupAll {
		return s.Routes()
	}
	targets := routeGroupSurfaces(group)
	if len(targets) == 0 {
		return http.HandlerFunc(s.notFound)
	}
	if s.httpMetrics != nil {
		s.httpMetrics.RegisterSurfaceGroup(string(group), SurfaceRouteGroupEndpointCount(group))
	}
	full := s.Routes()
	mux := http.NewServeMux()
	for route, surfaces := range runtimeSurfaceRoutePolicies {
		if !routeVisibleOnGroup(surfaces, targets) {
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
	mux.Handle("GET /openapi.json", s.surfaceOpenAPIHandler(group, full))
	mux.HandleFunc("/{path...}", s.notFound)
	return s.withSurfaceRouteGroupPolicy(group, mux)
}

func routeGroupSurfaces(group SurfaceRouteGroup) []surfacemodel.ProductSurface {
	switch group {
	case SurfaceRouteGroupPublic:
		return []surfacemodel.ProductSurface{
			surfacemodel.ProductSurfaceBusinessWorkspace,
			surfacemodel.ProductSurfaceConsumerPortal,
		}
	case SurfaceRouteGroupTenantAdmin:
		return []surfacemodel.ProductSurface{surfacemodel.ProductSurfaceAdminConsole}
	case SurfaceRouteGroupOps:
		return []surfacemodel.ProductSurface{surfacemodel.ProductSurfaceAdminConsole}
	default:
		return nil
	}
}

func routeVisibleOnGroup(routeSurfaces, groupSurfaces []surfacemodel.ProductSurface) bool {
	if len(routeSurfaces) == 0 {
		return true
	}
	for _, routeSurface := range routeSurfaces {
		if surfaceInTargets(routeSurface, groupSurfaces) {
			return true
		}
	}
	return false
}

func SurfaceRouteGroupEndpointCount(group SurfaceRouteGroup) int {
	if group == SurfaceRouteGroupAll {
		return len(runtimeSurfaceRoutePolicies)
	}
	targets := routeGroupSurfaces(group)
	if len(targets) == 0 {
		return 0
	}
	count := 0
	for _, surfaces := range runtimeSurfaceRoutePolicies {
		if routeVisibleOnGroup(surfaces, targets) {
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
