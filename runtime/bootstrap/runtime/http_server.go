package runtime

import (
	"context"
	"fmt"
	"net/http"

	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-foundation/modulehttp"
	transportbootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap/transport"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
)

// BindHTTP attaches the inbound HTTP adapter to the assembled Runtime.
func BindHTTP(ctx context.Context, runtime *Runtime) *Runtime {
	if ctx == nil {
		panic("bootstrap.BindHTTP requires a non-nil construction context")
	}
	if runtime == nil {
		panic("bootstrap.BindHTTP requires a non-nil Runtime")
	}
	var agentRepositories agentpersistence.Binding
	if runtime.agentBinding != nil {
		agentRepositories, _ = runtime.agentBinding.(agentpersistence.Binding)
	}
	runtime.api = transportbootstrap.AssembleRuntimeHTTPServer(ctx, transportbootstrap.HTTPServerDependencies{
		Config: runtime.cfg, Records: runtime.records,
		IdentityBinding:      runtime.identityBinding,
		PrincipalCache:       runtime.principalCache,
		AuthorizationActions: runtime.authorizationActions,
		MonitoringBinding:    runtime.monitoringBinding,
		SchedulerBinding:     runtime.schedulerBinding,
		IntegrationBinding:   runtime.integrationBinding,
		LifecycleBinding:     runtime.lifecycleBinding,
		Store:                runtime.store, RateLimiter: runtime.rateLimiter,
		AgentRepositories:        agentRepositories,
		AgentBinding:             runtime.agentBinding,
		ConversationToolsFactory: runtime.conversationToolsFactory,
		ProjectModel:             runtime.projectModel,
		SchemaCapabilities:       &runtime.schemaCapabilities,
		WorkspaceRolePolicy:      runtime.workspaceRolePolicy,
		WorkerControl:            runtime.worker.Control,
		Clock:                    runtime.worker.Clock,
		RuntimeInstanceID:        runtime.worker.WorkerID.String(),
		ReleaseIdentity:          runtime.releaseIdentity,
		ReleaseAdmission:         runtime.releaseAdmission.Check,
		ReleaseIntegrity:         runtime.releaseIntegrity,
		ModuleHTTPAdapters:       runtime.ModuleHTTPAdapters(),
		NotificationInboxActions: runtime.notificationHTTP,
		ProjectExtensions:        runtime.projectExtensions,
		ProjectHTTP:              runtime.projectHTTP,
		BlobStore:                runtime.blobStore,
	})
	return runtime
}

func (a *Runtime) Routes() http.Handler {
	return a.api.Routes()
}

// GuardAuthenticatedModuleHTTPRoute lets runtimehost mount an in-process
// module ahead of Routes() without replacing Runtime authentication semantics.
func (a *Runtime) GuardAuthenticatedModuleHTTPRoute(route modulehttp.Route, next http.Handler) (http.Handler, error) {
	if a == nil || a.api == nil {
		return nil, fmt.Errorf("Runtime HTTP router is unavailable")
	}
	return a.api.GuardAuthenticatedModuleHTTPRoute(route, next)
}

// RoutesForListenerGroup is a transport attachment helper. It remains a
// composition function instead of expanding Runtime's process API.
func RoutesForListenerGroup(runtime *Runtime, group runtimehttp.ListenerRouteGroup) http.Handler {
	if runtime == nil || runtime.api == nil {
		return http.NotFoundHandler()
	}
	return runtime.api.RoutesForListenerGroup(group)
}
