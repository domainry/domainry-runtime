package runtimehost

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/domainry/domainry-foundation/modulehttp"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityprincipal "github.com/domainry/domainry-identity-sdk/authorization/principal"
	identityhttpmiddleware "github.com/domainry/domainry-identity-sdk/httpmiddleware"
)

func newModuleHTTPRouteGuard(binding identitysdk.Binding) (moduleRouteGuard, error) {
	resolver, err := identityprincipal.NewResolver(binding, identityprincipal.Options{})
	if err != nil {
		return nil, fmt.Errorf("construct module HTTP principal resolver: %w", err)
	}
	middleware, err := identityhttpmiddleware.New(resolver, identityhttpmiddleware.WithAuthorization(binding.Authorization()))
	if err != nil {
		return nil, fmt.Errorf("construct module HTTP identity middleware: %w", err)
	}
	return func(route modulehttp.Route, next http.Handler) (http.Handler, error) {
		var protected http.Handler
		switch route.Authentication {
		case modulehttp.AuthenticationAuthenticated:
			if len(route.AnyPermissions) != 0 {
				protected = middleware.RequireAnyPermission(route.AnyPermissions, next)
			} else if permission := strings.TrimSpace(route.Permission); permission != "" {
				protected = middleware.RequirePermission(permission, next)
			} else {
				protected = middleware.RequireAuthenticated(next)
			}
		case modulehttp.AuthenticationService:
			return nil, fmt.Errorf("service-authenticated module routes require an explicit service principal verifier")
		default:
			return nil, fmt.Errorf("unsupported guarded authentication %q", route.Authentication)
		}
		return middleware.Authenticate(protected), nil
	}, nil
}
