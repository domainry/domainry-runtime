package runtimehost

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityhttpapi "github.com/domainry/domainry-identity-sdk/httpapi"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
)

func openProjectIdentity(ctx context.Context, cfg config.Config, factory identitysdk.Factory) (identitysdk.Binding, []identityhttpapi.Surface, error) {
	if factory == nil {
		return nil, nil, fmt.Errorf("generated project composition did not supply an Identity SDK Factory")
	}
	application := identitysdk.ApplicationRef{
		WorkspaceID:    identitysdk.WorkspaceID(cfg.IdentityWorkspaceID),
		ApplicationKey: identitysdk.ApplicationKey(cfg.IdentityAudience),
		RedirectURLs:   append([]string(nil), cfg.IdentityRedirectURLs...),
	}
	binding, err := factory.Open(ctx, application)
	if err != nil {
		return nil, nil, fmt.Errorf("open project Identity integration: %w", err)
	}
	if binding == nil {
		return nil, nil, fmt.Errorf("Identity SDK Factory returned no Binding")
	}
	var surfaces []identityhttpapi.Surface
	if provider, ok := binding.(identityhttpapi.Provider); ok {
		surfaces = provider.HTTPSurfaces()
	}
	switch binding.Descriptor().Mode {
	case identitysdk.DeploymentModeModule:
		if len(surfaces) == 0 {
			_ = binding.Close(context.WithoutCancel(ctx))
			return nil, nil, fmt.Errorf("Identity module returned no HTTP surfaces")
		}
	case identitysdk.DeploymentModeSaaS:
		if len(surfaces) != 0 {
			_ = binding.Close(context.WithoutCancel(ctx))
			return nil, nil, fmt.Errorf("Identity SaaS binding returned in-process HTTP surfaces")
		}
	default:
		_ = binding.Close(context.WithoutCancel(ctx))
		return nil, nil, fmt.Errorf("unsupported Identity deployment mode %q", binding.Descriptor().Mode)
	}
	return binding, append([]identityhttpapi.Surface(nil), surfaces...), nil
}

func mountIdentityHTTPSurfaces(group runtimehttp.SurfaceRouteGroup, surfaces []identityhttpapi.Surface, fallback http.Handler) (http.Handler, error) {
	if fallback == nil {
		fallback = http.NotFoundHandler()
	}
	if len(surfaces) == 0 {
		return fallback, nil
	}
	mux := http.NewServeMux()
	seen := map[string]string{}
	for _, surface := range surfaces {
		if surface == nil || surface.Handler() == nil {
			return nil, fmt.Errorf("Identity HTTP surface is incomplete")
		}
		name := strings.TrimSpace(surface.Name())
		if name == "" || surface.ContractVersion() != identityhttpapi.ContractVersion {
			return nil, fmt.Errorf("Identity HTTP surface contract is invalid")
		}
		for _, route := range surface.Routes() {
			if !identityRouteVisible(group, route.Exposures) {
				continue
			}
			pattern := strings.TrimSpace(route.Pattern)
			if pattern == "" {
				return nil, fmt.Errorf("Identity HTTP surface %q has an empty route", name)
			}
			if owner, duplicate := seen[pattern]; duplicate {
				return nil, fmt.Errorf("Identity HTTP route %q is owned by both %q and %q", pattern, owner, name)
			}
			seen[pattern] = name
			mux.Handle(pattern, surface.Handler())
		}
	}
	mux.Handle("/", fallback)
	return mux, nil
}

func identityRouteVisible(group runtimehttp.SurfaceRouteGroup, exposures []identityhttpapi.Exposure) bool {
	if group == runtimehttp.SurfaceRouteGroupAll {
		return true
	}
	want := identityhttpapi.Exposure("")
	switch group {
	case runtimehttp.SurfaceRouteGroupPublic:
		want = identityhttpapi.ExposurePublic
	case runtimehttp.SurfaceRouteGroupTenantAdmin:
		want = identityhttpapi.ExposureTenantAdmin
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
