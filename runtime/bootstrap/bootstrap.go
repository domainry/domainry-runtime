package bootstrap

import (
	"context"
	"net/http"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-agent-sdk/businessrpc"

	connector "github.com/domainry/domainry-connector-sdk"
	dataexchangesdk "github.com/domainry/domainry-data-exchange-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	monitoringsdk "github.com/domainry/domainry-monitoring-sdk"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	reportsdk "github.com/domainry/domainry-report-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	runtimebootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap/runtime"
	transportbootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap/transport"
	projectmodel "github.com/domainry/domainry-runtime/runtime/domain/project/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

type Runtime = runtimebootstrap.Runtime
type ConversationWebOptions = runtimebootstrap.ConversationWebOptions

func ConversationWebHandler(runtime *Runtime, options ConversationWebOptions) (http.Handler, error) {
	return runtimebootstrap.ConversationWebHandler(runtime, options)
}

func ConversationBusinessSource(runtime *Runtime) (businessrpc.Backend, error) {
	return runtimebootstrap.ConversationBusinessSource(runtime)
}

func ConversationBusinessHandler(runtime *Runtime, token string) (http.Handler, error) {
	return runtimebootstrap.ConversationBusinessHandler(runtime, token)
}

type EntrypointMux = transportbootstrap.EntrypointMux
type RuntimeReleaseArtifactEvidence = deploymentapplication.RuntimeReleaseArtifactEvidence
type ProjectDatabase = persistence.RuntimeStore
type RuntimeSchemaCapabilities = persistence.RuntimeSchemaCapabilities
type ProjectStartupOptions = runtimebootstrap.ProjectStartupOptions
type DevelopmentDataOptions = runtimebootstrap.DevelopmentDataOptions
type FoundationModuleFactories = runtimebootstrap.FoundationModuleFactories

func ProjectSchemaCapabilities(model projectmodel.RuntimeModel, extensions *runtimeext.ProjectExtensionRegistry) RuntimeSchemaCapabilities {
	return runtimebootstrap.ProjectSchemaCapabilities(model, extensions)
}

func PrepareProjectDatabase(ctx context.Context, cfg config.Config, capabilities RuntimeSchemaCapabilities) (*ProjectDatabase, error) {
	return runtimebootstrap.PrepareProjectDatabase(ctx, cfg, capabilities)
}

func NewVerifiedProjectWithAllTopologyFactoriesAndDatabaseOptions(ctx context.Context, cfg config.Config, handlers *runtimeext.ProjectExtensionRegistry, connectors *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, evidence RuntimeReleaseArtifactEvidence, identity identitysdk.Binding, foundationModules FoundationModuleFactories, notification notificationsdk.Factory, monitoring monitoringsdk.Factory, scheduler schedulersdk.Factory, dataExchange dataexchangesdk.Factory, integration integrationsdk.Factory, report reportsdk.Factory, database *ProjectDatabase, options ProjectStartupOptions, agent ...agentsdk.Factory) *Runtime {
	return runtimebootstrap.NewVerifiedProjectWithAllTopologyFactoriesAndDatabaseOptions(ctx, cfg, handlers, connectors, releaseIdentity, evidence, identity, foundationModules, notification, monitoring, scheduler, dataExchange, integration, report, database, options, agent...)
}

func BindHTTP(ctx context.Context, runtime *Runtime) *Runtime {
	return runtimebootstrap.BindHTTP(ctx, runtime)
}

func StartWorkers(ctx context.Context, runtime *Runtime) {
	runtimebootstrap.StartWorkers(ctx, runtime)
}

func RoutesForListenerGroup(runtime *Runtime, group runtimehttp.ListenerRouteGroup) http.Handler {
	return runtimebootstrap.RoutesForListenerGroup(runtime, group)
}

func AssembleHTTPServer(ctx context.Context, records *composition.RuntimeServices, identity identitysdk.Binding, uploadDir string, corsAllowedOrigins []string, allowDevAuthHeaders bool) *runtimehttp.HTTPRouter {
	return transportbootstrap.AssembleHTTPServer(ctx, records, identity, uploadDir, corsAllowedOrigins, allowDevAuthHeaders)
}
