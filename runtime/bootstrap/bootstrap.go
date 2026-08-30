package bootstrap

import (
	"context"
	"net/http"

	agentsdk "github.com/domainry/domainry-agent-sdk"

	connector "github.com/domainry/domainry-connector-sdk"
	dataexchangesdk "github.com/domainry/domainry-data-exchange-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	monitoringsdk "github.com/domainry/domainry-monitoring-sdk"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	partysdk "github.com/domainry/domainry-party-sdk"
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
type EntrypointMux = transportbootstrap.EntrypointMux
type RuntimeReleaseArtifactEvidence = deploymentapplication.RuntimeReleaseArtifactEvidence
type ProjectDatabase = persistence.RuntimeStore

func PrepareProjectDatabase(ctx context.Context, cfg config.Config) (*ProjectDatabase, error) {
	return runtimebootstrap.PrepareProjectDatabase(ctx, cfg)
}

func New(ctx context.Context, cfg config.Config, identity identitysdk.Binding, notification notificationsdk.Factory, party partysdk.Factory, dataExchange dataexchangesdk.Factory) *Runtime {
	return runtimebootstrap.New(ctx, cfg, identity, notification, party, dataExchange)
}

func NewWithScheduler(ctx context.Context, cfg config.Config, identity identitysdk.Binding, notification notificationsdk.Factory, party partysdk.Factory, scheduler schedulersdk.Factory, dataExchange dataexchangesdk.Factory, agent ...agentsdk.Factory) *Runtime {
	return runtimebootstrap.NewWithScheduler(ctx, cfg, identity, notification, party, scheduler, dataExchange, agent...)
}

func NewWithBusinessHandlers(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, identity identitysdk.Binding, notification notificationsdk.Factory, party partysdk.Factory, dataExchange dataexchangesdk.Factory) *Runtime {
	return runtimebootstrap.NewWithBusinessHandlers(ctx, cfg, handlers, identity, notification, party, dataExchange)
}

func NewWithBusinessHandlersAndScheduler(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, identity identitysdk.Binding, notification notificationsdk.Factory, party partysdk.Factory, scheduler schedulersdk.Factory, dataExchange dataexchangesdk.Factory, agent ...agentsdk.Factory) *Runtime {
	return runtimebootstrap.NewWithBusinessHandlersAndScheduler(ctx, cfg, handlers, identity, notification, party, scheduler, dataExchange, agent...)
}

func NewWithExtensions(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, identity identitysdk.Binding, notification notificationsdk.Factory, party partysdk.Factory, dataExchange dataexchangesdk.Factory) *Runtime {
	return runtimebootstrap.NewWithExtensions(ctx, cfg, handlers, connectors, identity, notification, party, dataExchange)
}

func NewVerifiedProjectWithIdentity(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, evidence RuntimeReleaseArtifactEvidence, binding identitysdk.Binding, notification notificationsdk.Factory, party partysdk.Factory, dataExchange dataexchangesdk.Factory) *Runtime {
	return runtimebootstrap.NewProjectWithIdentity(ctx, cfg, handlers, connectors, releaseIdentity, evidence, binding, notification, party, dataExchange)
}

func NewVerifiedProjectWithIdentityAndDatabase(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, evidence RuntimeReleaseArtifactEvidence, binding identitysdk.Binding, notification notificationsdk.Factory, party partysdk.Factory, dataExchange dataexchangesdk.Factory, database *ProjectDatabase) *Runtime {
	return runtimebootstrap.NewProjectWithIdentityAndDatabase(ctx, cfg, handlers, connectors, releaseIdentity, evidence, binding, notification, party, dataExchange, database)
}

func NewVerifiedProjectWithFactoriesAndDatabase(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, evidence RuntimeReleaseArtifactEvidence, identity identitysdk.Binding, notification notificationsdk.Factory, party partysdk.Factory, dataExchange dataexchangesdk.Factory, database *ProjectDatabase) *Runtime {
	return runtimebootstrap.NewProjectWithFactoriesAndDatabase(ctx, cfg, handlers, connectors, releaseIdentity, evidence, identity, notification, party, dataExchange, database)
}

func NewVerifiedProjectWithAllFactoriesAndDatabase(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, evidence RuntimeReleaseArtifactEvidence, identity identitysdk.Binding, notification notificationsdk.Factory, party partysdk.Factory, dataExchange dataexchangesdk.Factory, database *ProjectDatabase, monitoring ...monitoringsdk.Factory) *Runtime {
	return runtimebootstrap.NewProjectWithAllFactoriesAndDatabase(ctx, cfg, handlers, connectors, releaseIdentity, evidence, identity, notification, party, dataExchange, database, monitoring...)
}

func NewVerifiedProjectWithOwnerFactoriesAndDatabase(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, evidence RuntimeReleaseArtifactEvidence, identity identitysdk.Binding, notification notificationsdk.Factory, party partysdk.Factory, monitoring monitoringsdk.Factory, scheduler schedulersdk.Factory, dataExchange dataexchangesdk.Factory, database *ProjectDatabase, agent ...agentsdk.Factory) *Runtime {
	return runtimebootstrap.NewProjectWithOwnerFactoriesAndDatabase(ctx, cfg, handlers, connectors, releaseIdentity, evidence, identity, notification, party, monitoring, scheduler, dataExchange, database, agent...)
}

func NewVerifiedProjectWithTopologyFactoriesAndDatabase(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, evidence RuntimeReleaseArtifactEvidence, identity identitysdk.Binding, notification notificationsdk.Factory, party partysdk.Factory, monitoring monitoringsdk.Factory, scheduler schedulersdk.Factory, dataExchange dataexchangesdk.Factory, integration integrationsdk.Factory, database *ProjectDatabase, agent ...agentsdk.Factory) *Runtime {
	return runtimebootstrap.NewVerifiedProjectWithTopologyFactoriesAndDatabase(ctx, cfg, handlers, connectors, releaseIdentity, evidence, identity, notification, party, monitoring, scheduler, dataExchange, integration, database, agent...)
}

func PartyOrganizationScopes(runtime *Runtime) partysdk.OrganizationScopes {
	return runtimebootstrap.PartyOrganizationScopes(runtime)
}

func BindHTTP(ctx context.Context, runtime *Runtime) *Runtime {
	return runtimebootstrap.BindHTTP(ctx, runtime)
}

func StartWorkers(ctx context.Context, runtime *Runtime) {
	runtimebootstrap.StartWorkers(ctx, runtime)
}

func RoutesForSurfaceGroup(runtime *Runtime, group runtimehttp.SurfaceRouteGroup) http.Handler {
	return runtimebootstrap.RoutesForSurfaceGroup(runtime, group)
}

func AssembleHTTPServer(ctx context.Context, records *composition.RuntimeServices, identity identitysdk.Binding, uploadDir string, corsAllowedOrigins []string, allowDevAuthHeaders bool) *runtimehttp.HTTPRouter {
	return transportbootstrap.AssembleHTTPServer(ctx, records, identity, uploadDir, corsAllowedOrigins, allowDevAuthHeaders)
}
