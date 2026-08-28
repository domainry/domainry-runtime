package bootstrap

import (
	"context"
	"net/http"

	"github.com/domainry/domainry-connector-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	runtimebootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap/runtime"
	transportbootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap/transport"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
)

type Runtime = runtimebootstrap.Runtime
type EntrypointMux = transportbootstrap.EntrypointMux
type RuntimeReleaseArtifactEvidence = deploymentapplication.RuntimeReleaseArtifactEvidence
type ProjectDatabase = persistence.RuntimeStore

func PrepareProjectDatabase(ctx context.Context, cfg config.Config) (*ProjectDatabase, error) {
	return runtimebootstrap.PrepareProjectDatabase(ctx, cfg)
}

func New(ctx context.Context, cfg config.Config, identity identitysdk.Binding, notification notificationsdk.Factory) *Runtime {
	return runtimebootstrap.New(ctx, cfg, identity, notification)
}

func NewWithBusinessHandlers(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, identity identitysdk.Binding, notification notificationsdk.Factory) *Runtime {
	return runtimebootstrap.NewWithBusinessHandlers(ctx, cfg, handlers, identity, notification)
}

func NewWithExtensions(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, identity identitysdk.Binding, notification notificationsdk.Factory) *Runtime {
	return runtimebootstrap.NewWithExtensions(ctx, cfg, handlers, connectors, identity, notification)
}

func NewVerifiedProjectWithIdentity(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, evidence RuntimeReleaseArtifactEvidence, binding identitysdk.Binding, notification notificationsdk.Factory) *Runtime {
	return runtimebootstrap.NewProjectWithIdentity(ctx, cfg, handlers, connectors, releaseIdentity, evidence, binding, notification)
}

func NewVerifiedProjectWithIdentityAndDatabase(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, evidence RuntimeReleaseArtifactEvidence, binding identitysdk.Binding, notification notificationsdk.Factory, database *ProjectDatabase) *Runtime {
	return runtimebootstrap.NewProjectWithIdentityAndStore(ctx, cfg, handlers, connectors, releaseIdentity, evidence, binding, notification, database)
}

func NewVerifiedProjectWithFactoriesAndDatabase(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, evidence RuntimeReleaseArtifactEvidence, identity identitysdk.Binding, notification notificationsdk.Factory, database *ProjectDatabase) *Runtime {
	return runtimebootstrap.NewProjectWithFactoriesAndStore(ctx, cfg, handlers, connectors, releaseIdentity, evidence, identity, notification, database)
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
