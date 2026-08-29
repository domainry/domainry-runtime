package runtime

import (
	"context"
	"net/http"

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
	runtime.api = transportbootstrap.AssembleRuntimeHTTPServer(ctx, transportbootstrap.HTTPServerDependencies{
		Config: runtime.cfg, Records: runtime.records,
		IdentityBinding:   runtime.identityBinding,
		PartyBinding:      runtime.partyBinding,
		MonitoringBinding: runtime.monitoringBinding,
		SchedulerBinding:  runtime.schedulerBinding,
		Store:             runtime.store, RateLimiter: runtime.rateLimiter,
		Notifications: runtime.notificationHTTP, Manifest: runtime.manifest,
		WorkerControl:     runtime.worker.Control,
		Clock:             runtime.worker.Clock,
		RuntimeInstanceID: runtime.worker.WorkerID.String(),
		ReleaseIdentity:   runtime.releaseIdentity,
		ReleaseAdmission:  runtime.releaseAdmission.Check,
		ReleaseIntegrity:  runtime.releaseIntegrity,
	})
	return runtime
}

func (a *Runtime) Routes() http.Handler {
	return a.api.Routes()
}

// RoutesForSurfaceGroup is a transport attachment helper. It remains a
// composition function instead of expanding Runtime's process API.
func RoutesForSurfaceGroup(runtime *Runtime, group runtimehttp.SurfaceRouteGroup) http.Handler {
	if runtime == nil || runtime.api == nil {
		return http.NotFoundHandler()
	}
	return runtime.api.RoutesForSurfaceGroup(group)
}
