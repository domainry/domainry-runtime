package http

import (
	"context"
	"net/http"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type routerIdentityMiddlewareStub struct {
	principal identitysdk.Principal
	invalid   bool
}

func (stub routerIdentityMiddlewareStub) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		parts := strings.Fields(request.Header.Get("Authorization"))
		if stub.invalid || len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		identity := identitysdk.RequestIdentity{Principal: stub.principal, AccessToken: parts[1]}
		next.ServeHTTP(writer, request.WithContext(identitysdk.WithRequestIdentity(request.Context(), identity)))
	})
}

func (stub routerIdentityMiddlewareStub) RequirePasswordChanged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		identity, ok := identitysdk.RequestIdentityFromContext(request.Context())
		if !ok || identity.Principal.MustChangePassword {
			if ok {
				writer.WriteHeader(http.StatusForbidden)
			} else {
				writer.WriteHeader(http.StatusUnauthorized)
			}
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func routerWithIdentitySDK(principal identitysdk.Principal) *HTTPRouter {
	return &HTTPRouter{
		identityAuthentication: routerIdentityMiddlewareStub{principal: principal},
		identityPrincipal:      principalmodel.NewPrincipalFromIdentity,
	}
}

type routerIdentityAuthorizationStub struct {
	principal principalmodel.Principal
	error     error
}

func (stub routerIdentityAuthorizationStub) Resolve(context.Context, identitysdk.PrincipalResolutionRequest) (identitysdk.PrincipalResolution, error) {
	if stub.error != nil {
		return identitysdk.PrincipalResolution{}, stub.error
	}
	bundle := identitysdk.AccessBundle{}
	if stub.principal.AccessBundle != nil {
		bundle = *stub.principal.AccessBundle
	}
	return identitysdk.PrincipalResolution{Principal: stub.principal.Principal, AccessBundle: bundle}, nil
}

func routerTestPrincipal(principal identitysdk.Principal, permissions ...string) principalmodel.Principal {
	bundle := identitysdk.AccessBundle{ContractVersion: identitysdk.CurrentPolicyBundleVersion}
	for _, permission := range permissions {
		resource, action, ok := strings.Cut(permission, ".")
		if !ok {
			continue
		}
		bundle.FunctionGrants = append(bundle.FunctionGrants, identitysdk.FunctionGrant{
			Resource: identitysdk.ResourceType(resource), Action: identitysdk.Action(action), Effect: identitysdk.EffectAllow,
		})
	}
	principal.AccessBundle = &bundle
	return principalmodel.NewPrincipalFromIdentity(principal, "")
}

type routerCountingRegistrar struct{ calls *int }

func (stub routerCountingRegistrar) RegisterRoutes(*http.ServeMux) { *stub.calls++ }

func completeRouterForListenerGroupTests(config HTTPRouterConfig) *HTTPRouter {
	router := NewHTTPRouter(config, HTTPRouterDependencies{})
	calls := 0
	registrar := routerCountingRegistrar{calls: &calls}
	router.recordHTTP, router.uploadHTTP, router.discoveryHTTP = registrar, registrar, registrar
	router.workflowHTTP, router.automationHTTP, router.dispatchHTTP = registrar, registrar, registrar
	router.applicationSchemaHTTP, router.operationsHTTP = registrar, registrar
	return router
}

type routerRuntimeStatusStub struct {
	storageErr   error
	migrationErr error
}

func (stub routerRuntimeStatusStub) Health(context.Context) map[string]any {
	return map[string]any{"status": "ok"}
}

func (stub routerRuntimeStatusStub) StorageReadiness(context.Context) error { return stub.storageErr }
func (stub routerRuntimeStatusStub) MigrationReadiness(context.Context) error {
	return stub.migrationErr
}
