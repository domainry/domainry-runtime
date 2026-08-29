package bootstrap

import (
	"context"
	"net/http"

	"github.com/domainry/domainry-connector-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	monitoringsdk "github.com/domainry/domainry-monitoring-sdk"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	partysdk "github.com/domainry/domainry-party-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
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

func New(ctx context.Context, cfg config.Config, identity identitysdk.Binding, notification notificationsdk.Factory, party partysdk.Factory) *Runtime {
	return runtimebootstrap.New(ctx, cfg, identity, notification, party)
}

func NewWithBusinessHandlers(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, identity identitysdk.Binding, notification notificationsdk.Factory, party partysdk.Factory) *Runtime {
	return runtimebootstrap.NewWithBusinessHandlers(ctx, cfg, handlers, identity, notification, party)
}

func NewWithExtensions(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, identity identitysdk.Binding, notification notificationsdk.Factory, party partysdk.Factory) *Runtime {
	return runtimebootstrap.NewWithExtensions(ctx, cfg, handlers, connectors, identity, notification, party)
}

func NewVerifiedProjectWithIdentity(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, evidence RuntimeReleaseArtifactEvidence, binding identitysdk.Binding, notification notificationsdk.Factory, party partysdk.Factory) *Runtime {
	return runtimebootstrap.NewProjectWithIdentity(ctx, cfg, handlers, connectors, releaseIdentity, evidence, binding, notification, party)
}

func NewVerifiedProjectWithIdentityAndDatabase(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, evidence RuntimeReleaseArtifactEvidence, binding identitysdk.Binding, notification notificationsdk.Factory, party partysdk.Factory, database *ProjectDatabase) *Runtime {
	return runtimebootstrap.NewProjectWithIdentityAndDatabase(ctx, cfg, handlers, connectors, releaseIdentity, evidence, binding, notification, party, database)
}

func NewVerifiedProjectWithFactoriesAndDatabase(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, evidence RuntimeReleaseArtifactEvidence, identity identitysdk.Binding, notification notificationsdk.Factory, party partysdk.Factory, database *ProjectDatabase) *Runtime {
	return runtimebootstrap.NewProjectWithFactoriesAndDatabase(ctx, cfg, handlers, connectors, releaseIdentity, evidence, identity, notification, party, database)
}

func NewVerifiedProjectWithAllFactoriesAndDatabase(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, evidence RuntimeReleaseArtifactEvidence, identity identitysdk.Binding, notification notificationsdk.Factory, party partysdk.Factory, database *ProjectDatabase, monitoring ...monitoringsdk.Factory) *Runtime {
	return runtimebootstrap.NewProjectWithAllFactoriesAndDatabase(ctx, cfg, handlers, connectors, releaseIdentity, evidence, identity, notification, party, database, monitoring...)
}

func NewVerifiedProjectWithOwnerFactoriesAndDatabase(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, evidence RuntimeReleaseArtifactEvidence, identity identitysdk.Binding, notification notificationsdk.Factory, party partysdk.Factory, monitoring monitoringsdk.Factory, scheduler schedulersdk.Factory, database *ProjectDatabase) *Runtime {
	return runtimebootstrap.NewProjectWithOwnerFactoriesAndDatabase(ctx, cfg, handlers, connectors, releaseIdentity, evidence, identity, notification, party, monitoring, scheduler, database)
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

// ActionConnectorGateway is an internal host-composition seam. It deliberately
// remains a package function so Runtime exposes only process lifecycle methods.
func ActionConnectorGateway(runtime *Runtime) *integrationapplication.ActionConnectorGateway {
	return runtimebootstrap.ActionConnectorGateway(runtime)
}

func AssembleHTTPServer(ctx context.Context, records *composition.RuntimeServices, identity identitysdk.Binding, uploadDir string, corsAllowedOrigins []string, allowDevAuthHeaders bool, agentHTTP runtimehttp.AgentHTTPConfig) *runtimehttp.HTTPRouter {
	return transportbootstrap.AssembleHTTPServer(ctx, records, identity, uploadDir, corsAllowedOrigins, allowDevAuthHeaders, agentHTTP)
}
