package runtimehost

import (
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/domainry/domainry-foundation/modulehttp"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
)

// moduleSurfaceRouter is the single process-host router for module-owned HTTP.
// Bind constructs a complete immutable mux before swapping it into service, so
// tenant activation and module replacement never expose a partial route set.
type moduleSurfaceRouter struct {
	mu       sync.RWMutex
	group    runtimehttp.ListenerRouteGroup
	fallback http.Handler
	handler  http.Handler
	guard    moduleRouteGuard
}

func newModuleSurfaceRouter(group runtimehttp.ListenerRouteGroup, fallback http.Handler) *moduleSurfaceRouter {
	if fallback == nil {
		fallback = http.NotFoundHandler()
	}
	return &moduleSurfaceRouter{group: group, fallback: fallback, handler: fallback}
}

func (router *moduleSurfaceRouter) Bind(surfaces []modulehttp.Surface, guards ...moduleRouteGuard) error {
	guard := router.guard
	if len(guards) > 0 {
		guard = guards[0]
	}
	handler, err := mountModuleHTTPSurfaces(router.group, surfaces, router.fallback, guard)
	if err != nil {
		return err
	}
	router.mu.Lock()
	router.handler = handler
	router.guard = guard
	router.mu.Unlock()
	return nil
}

func (router *moduleSurfaceRouter) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	router.mu.RLock()
	handler := router.handler
	router.mu.RUnlock()
	handler.ServeHTTP(writer, request)
}

func mountModuleHTTPSurfaces(group runtimehttp.ListenerRouteGroup, surfaces []modulehttp.Surface, fallback http.Handler, guards ...moduleRouteGuard) (http.Handler, error) {
	if fallback == nil {
		fallback = http.NotFoundHandler()
	}
	if len(surfaces) == 0 {
		return fallback, nil
	}
	mux := http.NewServeMux()
	var guard moduleRouteGuard
	if len(guards) > 0 {
		guard = guards[0]
	}
	seen := map[string]string{}
	for _, surface := range surfaces {
		if err := modulehttp.ValidateSurface(surface); err != nil {
			return nil, err
		}
		identity := strings.TrimSpace(surface.Owner()) + "/" + strings.TrimSpace(surface.Name())
		for _, route := range surface.Routes() {
			if !moduleRouteVisible(group, route.Exposures) {
				continue
			}
			pattern := strings.TrimSpace(route.Pattern)
			if owner, duplicate := seen[pattern]; duplicate {
				return nil, fmt.Errorf("module HTTP route %q is owned by both %q and %q", pattern, owner, identity)
			}
			handler := surface.Handler()
			if route.Authentication != modulehttp.AuthenticationAnonymous {
				if guard == nil {
					return nil, fmt.Errorf("module HTTP route %q requires a host authorization guard", pattern)
				}
				var err error
				handler, err = guard(route, handler)
				if err != nil {
					return nil, fmt.Errorf("guard module HTTP route %q: %w", pattern, err)
				}
			}
			seen[pattern] = identity
			mux.Handle(pattern, handler)
		}
	}
	mux.Handle("/", fallback)
	return mux, nil
}

func moduleRouteVisible(group runtimehttp.ListenerRouteGroup, exposures []modulehttp.Exposure) bool {
	if group == runtimehttp.ListenerRouteGroupAll {
		return true
	}
	want := modulehttp.Exposure("")
	switch group {
	case runtimehttp.ListenerRouteGroupPublic:
		want = modulehttp.ExposurePublic
	case runtimehttp.ListenerRouteGroupTenantAdmin:
		want = modulehttp.ExposureTenantAdmin
	case runtimehttp.ListenerRouteGroupOps:
		want = modulehttp.ExposureOps
	default:
		return false
	}
	for _, exposure := range exposures {
		if exposure == want {
			return true
		}
	}
	return false
}
