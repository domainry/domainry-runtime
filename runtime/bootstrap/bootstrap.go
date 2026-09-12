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
type BusinessSeedReferenceCandidate = runtimebootstrap.BusinessSeedReferenceCandidate
type ProjectStartupOptions = runtimebootstrap.ProjectStartupOptions
type DefinitionUpgradePlanRequested = runtimebootstrap.DefinitionUpgradePlanRequested

func PrepareProjectDatabase(ctx context.Context, cfg config.Config) (*ProjectDatabase, error) {
	return runtimebootstrap.PrepareProjectDatabase(ctx, cfg)
}

func New(ctx context.Context, cfg config.Config, identity identitysdk.Binding, notification notificationsdk.Factory, dataExchange dataexchangesdk.Factory, integration integrationsdk.Factory) *Runtime {
	return runtimebootstrap.New(ctx, cfg, identity, notification, dataExchange, integration)
}

func NewWithScheduler(ctx context.Context, cfg config.Config, identity identitysdk.Binding, notification notificationsdk.Factory, scheduler schedulersdk.Factory, dataExchange dataexchangesdk.Factory, integration integrationsdk.Factory, agent ...agentsdk.Factory) *Runtime {
	return runtimebootstrap.NewWithScheduler(ctx, cfg, identity, notification, scheduler, dataExchange, integration, agent...)
}

func NewWithBusinessHandlers(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, identity identitysdk.Binding, notification notificationsdk.Factory, dataExchange dataexchangesdk.Factory, integration integrationsdk.Factory) *Runtime {
	return runtimebootstrap.NewWithBusinessHandlers(ctx, cfg, handlers, identity, notification, dataExchange, integration)
}

func NewWithBusinessHandlersAndScheduler(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, identity identitysdk.Binding, notification notificationsdk.Factory, scheduler schedulersdk.Factory, dataExchange dataexchangesdk.Factory, integration integrationsdk.Factory, agent ...agentsdk.Factory) *Runtime {
	return runtimebootstrap.NewWithBusinessHandlersAndScheduler(ctx, cfg, handlers, identity, notification, scheduler, dataExchange, integration, agent...)
}

func NewWithExtensions(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, identity identitysdk.Binding, notification notificationsdk.Factory, dataExchange dataexchangesdk.Factory, integration integrationsdk.Factory) *Runtime {
	return runtimebootstrap.NewWithExtensions(ctx, cfg, handlers, connectors, identity, notification, dataExchange, integration)
}

func NewVerifiedProjectWithIdentity(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, evidence RuntimeReleaseArtifactEvidence, binding identitysdk.Binding, notification notificationsdk.Factory, dataExchange dataexchangesdk.Factory, integration integrationsdk.Factory) *Runtime {
	return runtimebootstrap.NewProjectWithIdentity(ctx, cfg, handlers, connectors, releaseIdentity, evidence, binding, notification, dataExchange, integration)
}

func NewVerifiedProjectWithIdentityAndDatabase(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, evidence RuntimeReleaseArtifactEvidence, binding identitysdk.Binding, notification notificationsdk.Factory, dataExchange dataexchangesdk.Factory, integration integrationsdk.Factory, database *ProjectDatabase) *Runtime {
	return runtimebootstrap.NewProjectWithIdentityAndDatabase(ctx, cfg, handlers, connectors, releaseIdentity, evidence, binding, notification, dataExchange, integration, database)
}

func NewVerifiedProjectWithFactoriesAndDatabase(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, evidence RuntimeReleaseArtifactEvidence, identity identitysdk.Binding, notification notificationsdk.Factory, dataExchange dataexchangesdk.Factory, integration integrationsdk.Factory, database *ProjectDatabase) *Runtime {
	return runtimebootstrap.NewProjectWithFactoriesAndDatabase(ctx, cfg, handlers, connectors, releaseIdentity, evidence, identity, notification, dataExchange, integration, database)
}

func NewVerifiedProjectWithAllFactoriesAndDatabase(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, evidence RuntimeReleaseArtifactEvidence, identity identitysdk.Binding, notification notificationsdk.Factory, dataExchange dataexchangesdk.Factory, integration integrationsdk.Factory, database *ProjectDatabase, monitoring ...monitoringsdk.Factory) *Runtime {
	return runtimebootstrap.NewProjectWithAllFactoriesAndDatabase(ctx, cfg, handlers, connectors, releaseIdentity, evidence, identity, notification, dataExchange, integration, database, monitoring...)
}

func NewVerifiedProjectWithOwnerFactoriesAndDatabase(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, evidence RuntimeReleaseArtifactEvidence, identity identitysdk.Binding, notification notificationsdk.Factory, monitoring monitoringsdk.Factory, scheduler schedulersdk.Factory, dataExchange dataexchangesdk.Factory, integration integrationsdk.Factory, database *ProjectDatabase, agent ...agentsdk.Factory) *Runtime {
	return runtimebootstrap.NewProjectWithOwnerFactoriesAndDatabase(ctx, cfg, handlers, connectors, releaseIdentity, evidence, identity, notification, monitoring, scheduler, dataExchange, integration, database, agent...)
}

func NewVerifiedProjectWithTopologyFactoriesAndDatabase(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, evidence RuntimeReleaseArtifactEvidence, identity identitysdk.Binding, notification notificationsdk.Factory, monitoring monitoringsdk.Factory, scheduler schedulersdk.Factory, dataExchange dataexchangesdk.Factory, integration integrationsdk.Factory, database *ProjectDatabase, agent ...agentsdk.Factory) *Runtime {
	return runtimebootstrap.NewVerifiedProjectWithTopologyFactoriesAndDatabase(ctx, cfg, handlers, connectors, releaseIdentity, evidence, identity, notification, monitoring, scheduler, dataExchange, integration, database, agent...)
}

func NewVerifiedProjectWithAllTopologyFactoriesAndDatabase(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, evidence RuntimeReleaseArtifactEvidence, identity identitysdk.Binding, notification notificationsdk.Factory, monitoring monitoringsdk.Factory, scheduler schedulersdk.Factory, dataExchange dataexchangesdk.Factory, integration integrationsdk.Factory, report reportsdk.Factory, database *ProjectDatabase, agent ...agentsdk.Factory) *Runtime {
	return runtimebootstrap.NewVerifiedProjectWithAllTopologyFactoriesAndDatabase(ctx, cfg, handlers, connectors, releaseIdentity, evidence, identity, notification, monitoring, scheduler, dataExchange, integration, report, database, agent...)
}

func NewVerifiedProjectWithAllTopologyFactoriesAndDatabaseOptions(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, evidence RuntimeReleaseArtifactEvidence, identity identitysdk.Binding, notification notificationsdk.Factory, monitoring monitoringsdk.Factory, scheduler schedulersdk.Factory, dataExchange dataexchangesdk.Factory, integration integrationsdk.Factory, report reportsdk.Factory, database *ProjectDatabase, options ProjectStartupOptions, agent ...agentsdk.Factory) *Runtime {
	return runtimebootstrap.NewVerifiedProjectWithAllTopologyFactoriesAndDatabaseOptions(ctx, cfg, handlers, connectors, releaseIdentity, evidence, identity, notification, monitoring, scheduler, dataExchange, integration, report, database, options, agent...)
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
