package http

import (
	"context"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	"net/http"
	"strings"
	"time"

	actioncontract "github.com/domainry/domainry-foundation/action"
	capacityplatform "github.com/domainry/domainry-foundation/capacity"
	"github.com/domainry/domainry-foundation/modulehttp"
	"github.com/domainry/domainry-foundation/ratelimit"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	businesssystemapplication "github.com/domainry/domainry-runtime/runtime/application/businesssystem"
	businesseventcontract "github.com/domainry/domainry-runtime/runtime/domain/businessevent/contract"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	appschemahttp "github.com/domainry/domainry-runtime/runtime/transport/http/appschema"
	automationhttp "github.com/domainry/domainry-runtime/runtime/transport/http/automation"
	businesseventhttp "github.com/domainry/domainry-runtime/runtime/transport/http/businessevents"
	businessreferencehttp "github.com/domainry/domainry-runtime/runtime/transport/http/businessreferences"
	businesssystemhttp "github.com/domainry/domainry-runtime/runtime/transport/http/businesssystem"
	discoveryhttp "github.com/domainry/domainry-runtime/runtime/transport/http/discovery"
	dispatchhttp "github.com/domainry/domainry-runtime/runtime/transport/http/dispatch"
	lifecyclehttp "github.com/domainry/domainry-runtime/runtime/transport/http/lifecycle"
	notificationhttp "github.com/domainry/domainry-runtime/runtime/transport/http/notifications"
	openapihttp "github.com/domainry/domainry-runtime/runtime/transport/http/openapi"
	operationshttp "github.com/domainry/domainry-runtime/runtime/transport/http/operations"
	publicationhandoffhttp "github.com/domainry/domainry-runtime/runtime/transport/http/publicationhandoff"
	recordhttp "github.com/domainry/domainry-runtime/runtime/transport/http/records"
	uploadhttp "github.com/domainry/domainry-runtime/runtime/transport/http/uploads"
	workflowhttp "github.com/domainry/domainry-runtime/runtime/transport/http/workflows"
	workspaceprovisionhttp "github.com/domainry/domainry-runtime/runtime/transport/http/workspaceprovision"
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
	WorkspaceProvisionClientID        string
	WorkspaceProvisionSigningSecret   string
	ProductBrandName                  string
	CORSAllowedOrigins                []string
	AllowDevAuthHeaders               bool
	HealthCheckTimeout                time.Duration
	MaxJSONBodyBytes                  int64
	CapacityLimits                    capacityplatform.Limits
	RequestTimeout                    time.Duration
	ListenerGroupPolicies             map[ListenerRouteGroup]ListenerRouteGroupPolicy
	BusinessEventReplayLimit          int
	BusinessEventSubscriberBuffer     int
	BusinessEventGlobalConnections    int
	BusinessEventWorkspaceConnections int
	BusinessEventPrincipalConnections int
	BusinessEventHeartbeatInterval    time.Duration
	BusinessEventRetryInterval        time.Duration
}

type ListenerRouteGroupPolicy struct {
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
	PrincipalFromIntegrationAPIKey(context.Context, string, string, string) (principalmodel.Principal, integrationsdk.APIKey, error)
}

type BusinessPrincipalResolver interface {
	ResolveBusinessPrincipal(context.Context, principalmodel.Principal, string, string) (principalmodel.Principal, error)
}

type WorkspaceAdmission interface {
	WorkspaceActive(context.Context, string) (bool, error)
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
	IdentityAuthentication           IdentityRequestMiddleware
	IdentityPrincipal                IdentityPrincipalProjection
	IntegrationAuthentication        IntegrationAuthenticationPrincipalProvider
	IdentityAuthorization            identitysdk.PrincipalResolver
	AuthorizationActions             func() *actioncontract.Registry
	BusinessPrincipal                BusinessPrincipalResolver
	WorkspaceAdmission               WorkspaceAdmission
	SecurityAudit                    SecurityAuditAppender
	RuntimeStatus                    DeploymentRuntimeStatusProvider
	TechnicalMetrics                 TechnicalMetricsProvider
	Backpressure                     func(context.Context) bool
	WorkerControl                    *workerplatform.Controller
	OperationsControlState           OperationsControlStateProvider
	RuntimeReleaseAdmission          RuntimeReleaseAdmissionProvider
	RuntimeReleaseIntegrity          RuntimeReleaseIntegrityProvider
	RuntimeInstanceID                string
	BusinessEventBackplane           businesseventcontract.Backplane
	RateLimiter                      ratelimit.Limiter
	ModuleHTTPAdapters               []modulehttp.Adapter
	RuntimeAuthoringScenarioReceipts *businesssystemapplication.RuntimeAuthoringScenarioReceiptService
}

type HTTPRouterHandlers struct {
	Records            *recordhttp.RecordsHandler
	Uploads            *uploadhttp.UploadsHandler
	Discovery          *discoveryhttp.DiscoveryHandler
	OpenAPI            *openapihttp.OpenAPIHandler
	Workflows          *workflowhttp.WorkflowsHandler
	Automation         *automationhttp.AutomationHandler
	Dispatch           *dispatchhttp.ExecutionHandler
	BusinessReferences *businessreferencehttp.BusinessReferencesHandler
	PublicationHandoff *publicationhandoffhttp.Handler
	BusinessSystem     *businesssystemhttp.BusinessSystemHandler
	ApplicationSchema  *appschemahttp.ApplicationSchemaHandler
	Notifications      *notificationhttp.NotificationsHandler
	Operations         *operationshttp.OperationsHandler
	Lifecycle          *lifecyclehttp.LifecycleHandler
	BusinessEvents     *businesseventhttp.BusinessEventsHandler
	WorkspaceProvision *workspaceprovisionhttp.WorkspaceProvisionHandler
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
