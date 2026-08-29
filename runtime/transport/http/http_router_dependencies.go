package http

import (
	"context"
	"net/http"
	"strings"
	"time"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	businesseventcontract "github.com/domainry/domainry-runtime/runtime/domain/businessevent/contract"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	capacityplatform "github.com/domainry/domainry-runtime/runtime/platform/capacity"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	agentdialoghttp "github.com/domainry/domainry-runtime/runtime/transport/http/agentdialog"
	automationhttp "github.com/domainry/domainry-runtime/runtime/transport/http/automation"
	businesseventhttp "github.com/domainry/domainry-runtime/runtime/transport/http/businessevents"
	businessreferencehttp "github.com/domainry/domainry-runtime/runtime/transport/http/businessreferences"
	businessseedhttp "github.com/domainry/domainry-runtime/runtime/transport/http/businessseeds"
	businesssystemhttp "github.com/domainry/domainry-runtime/runtime/transport/http/businesssystem"
	capabilityhttp "github.com/domainry/domainry-runtime/runtime/transport/http/capabilities"
	changeplanhttp "github.com/domainry/domainry-runtime/runtime/transport/http/changeplans"
	discoveryhttp "github.com/domainry/domainry-runtime/runtime/transport/http/discovery"
	frontendcapabilityhttp "github.com/domainry/domainry-runtime/runtime/transport/http/frontendcapability"
	integrationhttp "github.com/domainry/domainry-runtime/runtime/transport/http/integrations"
	metadatahttp "github.com/domainry/domainry-runtime/runtime/transport/http/metadata"
	notificationhttp "github.com/domainry/domainry-runtime/runtime/transport/http/notifications"
	openapihttp "github.com/domainry/domainry-runtime/runtime/transport/http/openapi"
	operationshttp "github.com/domainry/domainry-runtime/runtime/transport/http/operations"
	partyhttp "github.com/domainry/domainry-runtime/runtime/transport/http/party"
	recordhttp "github.com/domainry/domainry-runtime/runtime/transport/http/records"
	reporthttp "github.com/domainry/domainry-runtime/runtime/transport/http/reports"
	schedulerhttp "github.com/domainry/domainry-runtime/runtime/transport/http/scheduler"
	surfacecontexthttp "github.com/domainry/domainry-runtime/runtime/transport/http/surfacecontext"
	uploadhttp "github.com/domainry/domainry-runtime/runtime/transport/http/uploads"
	workflowhttp "github.com/domainry/domainry-runtime/runtime/transport/http/workflows"
)

func cloneStringAnyMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	out := map[string]any{}
	for key, item := range value {
		out[key] = item
	}
	return out
}

func responseParams(values ...string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := map[string]string{}
	for index := 0; index+1 < len(values); index += 2 {
		key := strings.TrimSpace(values[index])
		if key == "" {
			continue
		}
		out[key] = values[index+1]
	}
	return out
}

type HTTPRouterConfig struct {
	ProductBrandName                  string
	CORSAllowedOrigins                []string
	AllowDevAuthHeaders               bool
	HealthCheckTimeout                time.Duration
	MaxJSONBodyBytes                  int64
	CapacityLimits                    capacityplatform.Limits
	RequestTimeout                    time.Duration
	SurfaceGroupPolicies              map[SurfaceRouteGroup]SurfaceRouteGroupPolicy
	BusinessEventReplayLimit          int
	BusinessEventSubscriberBuffer     int
	BusinessEventGlobalConnections    int
	BusinessEventWorkspaceConnections int
	BusinessEventPrincipalConnections int
	BusinessEventHeartbeatInterval    time.Duration
	BusinessEventRetryInterval        time.Duration
}

type SurfaceRouteGroupPolicy struct {
	MaxJSONBodyBytes   int64
	RequestTimeout     time.Duration
	RateLimitPerMinute int
	AuditClass         string
	AllowedOrigins     []string
}

type TechnicalMetricsProvider func(context.Context) string
type OperationsControlStateProvider func(context.Context, string, string) (bool, bool, error)
type RuntimeReleaseAdmissionProvider func() error

type RuntimeReleaseIntegrityProvider interface {
	BuildReadiness(context.Context) error
	SignatureReadiness(context.Context) error
	SchemaReadiness(context.Context) error
	RegistryReadiness(context.Context) error
}

// RuntimeReleaseIdentity remains a transport-visible alias while deployment
// owns the identity and admission semantics.
type RuntimeReleaseIdentity = deploymentmodel.RuntimeReleaseIdentity

type IdentityRequestMiddleware interface {
	Authenticate(http.Handler) http.Handler
	RequirePasswordChanged(http.Handler) http.Handler
}

type IdentityPrincipalProjection func(identitysdk.Principal, string) principalmodel.Principal

type IntegrationAuthenticationPrincipalProvider interface {
	PrincipalFromIntegrationAPIKey(context.Context, string, string, string) (principalmodel.Principal, integrationmodel.IntegrationAPIKey, error)
}

type BusinessPrincipalResolver interface {
	ResolveBusinessPrincipal(context.Context, principalmodel.Principal, string, string, string) (principalmodel.Principal, error)
}

type SecurityAuditAppender interface {
	AppendWithMetadata(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any)
}

type DeploymentRuntimeStatusProvider interface {
	Health(context.Context) map[string]any
	StorageReadiness(context.Context) error
	MigrationReadiness(context.Context) error
}

type HTTPRouterDependencies struct {
	IdentityAuthentication    IdentityRequestMiddleware
	IdentityPrincipal         IdentityPrincipalProjection
	IntegrationAuthentication IntegrationAuthenticationPrincipalProvider
	IdentityAuthorization     identitysdk.PrincipalResolver
	BusinessPrincipal         BusinessPrincipalResolver
	SecurityAudit             SecurityAuditAppender
	RuntimeStatus             DeploymentRuntimeStatusProvider
	TechnicalMetrics          TechnicalMetricsProvider
	Backpressure              func(context.Context) bool
	WorkerControl             *workerplatform.Controller
	OperationsControlState    OperationsControlStateProvider
	RuntimeReleaseAdmission   RuntimeReleaseAdmissionProvider
	RuntimeReleaseIntegrity   RuntimeReleaseIntegrityProvider
	RuntimeInstanceID         string
	BusinessEventBackplane    businesseventcontract.Backplane
}

type HTTPRouterHandlers struct {
	Records              *recordhttp.RecordsHandler
	SurfaceContext       *surfacecontexthttp.SurfaceContextHandler
	Uploads              *uploadhttp.UploadsHandler
	Discovery            *discoveryhttp.DiscoveryHandler
	OpenAPI              *openapihttp.OpenAPIHandler
	Workflows            *workflowhttp.WorkflowsHandler
	Automation           *automationhttp.AutomationHandler
	Scheduler            *schedulerhttp.SchedulerHandler
	Reports              *reporthttp.ReportsHandler
	FrontendCapabilities *frontendcapabilityhttp.FrontendCapabilityHandler
	BusinessReferences   *businessreferencehttp.BusinessReferencesHandler
	BusinessSeeds        *businessseedhttp.BusinessSeedHandler
	BusinessSystem       *businesssystemhttp.BusinessSystemHandler
	Capabilities         *capabilityhttp.CapabilitiesHandler
	ChangePlans          *changeplanhttp.ChangePlansHandler
	Integrations         *integrationhttp.IntegrationsHandler
	Metadata             *metadatahttp.MetadataHandler
	Notifications        *notificationhttp.NotificationsHandler
	Party                *partyhttp.PartyHandler
	AgentDialog          *agentdialoghttp.AgentDialogHandler
	Operations           *operationshttp.OperationsHandler
	BusinessEvents       *businesseventhttp.BusinessEventsHandler
}

type HandlerCallbacks struct {
	Principal                 func(*http.Request) principalmodel.Principal
	WriteJSON                 func(http.ResponseWriter, int, any)
	WriteError                func(http.ResponseWriter, *http.Request, int, string, ...string)
	WriteServiceError         func(http.ResponseWriter, *http.Request, error)
	DecodeJSON                func(http.ResponseWriter, *http.Request, any) bool
	SecurityAudit             func(*http.Request, string, string, map[string]any)
	SecurityAuditForPrincipal func(*http.Request, principalmodel.Principal, string, string, map[string]any)
	ProvisionRequired         http.HandlerFunc
	Locale                    func(*http.Request) string
}
