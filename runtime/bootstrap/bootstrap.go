package bootstrap

import (
	"context"
	"net/http"

	"github.com/domainry/domainry-connector-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	deploymentapplication "github.com/domainry/domainry-runtime/runtime/application/deployment"
	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	runtimebootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap/runtime"
	transportbootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap/transport"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
)

type Runtime = runtimebootstrap.Runtime
type EntrypointMux = transportbootstrap.EntrypointMux
type RuntimeReleaseArtifactEvidence = deploymentapplication.RuntimeReleaseArtifactEvidence

func New(ctx context.Context, cfg config.Config, identity identitysdk.Binding) *Runtime {
	return runtimebootstrap.New(ctx, cfg, identity)
}

func NewWithBusinessHandlers(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, identity identitysdk.Binding) *Runtime {
	return runtimebootstrap.NewWithBusinessHandlers(ctx, cfg, handlers, identity)
}

func NewWithExtensions(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, identity identitysdk.Binding) *Runtime {
	return runtimebootstrap.NewWithExtensions(ctx, cfg, handlers, connectors, identity)
}

func NewVerifiedProjectWithIdentity(ctx context.Context, cfg config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, releaseIdentity runtimehttp.RuntimeReleaseIdentity, evidence RuntimeReleaseArtifactEvidence, binding identitysdk.Binding) *Runtime {
	return runtimebootstrap.NewProjectWithIdentity(ctx, cfg, handlers, connectors, releaseIdentity, evidence, binding)
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
