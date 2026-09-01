package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulehttp"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestModuleHTTPRouteAuthorizationUsesDeclaredPermissionPolicy(t *testing.T) {
	call := func(route modulehttp.Route, principal *principalmodel.Principal) (int, bool) {
		t.Helper()
		executed := false
		registry := actioncontract.NewRegistry()
		if err := registry.Register(route.Action); err != nil {
			t.Fatal(err)
		}
		if err := registry.Freeze(); err != nil {
			t.Fatal(err)
		}
		router := &HTTPRouter{authorizationActions: func() *actioncontract.Registry { return registry }}
		binding := moduleHTTPRoute{
			owner: "test-module",
			route: route,
			handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				executed = true
				writer.WriteHeader(http.StatusNoContent)
			}),
		}
		mux := http.NewServeMux()
		mux.Handle(route.Pattern(), router.moduleHTTPRouteHandler(binding))
		handler := router.withActionAuthorization(mux, mux)
		request := httptest.NewRequest(http.MethodGet, "/module-resource", nil)
		if principal != nil {
			request = requestWithPrincipal(request, *principal)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response.Code, executed
	}

	known := moduleHTTPAuthorizationPrincipal()
	workspaceAdmin := moduleHTTPAuthorizationPrincipal("workspace.admin")
	read := moduleHTTPAuthorizationPrincipal("module.resource.read")
	for _, test := range []struct {
		name      string
		route     modulehttp.Route
		principal *principalmodel.Principal
		want      int
		executed  bool
	}{
		{name: "anonymous", route: modulehttp.Route{Action: moduleHTTPTestAction("module.resource.public", actioncontract.AuthorizationAnonymousProtocol)}, want: http.StatusNoContent, executed: true},
		{name: "authenticated missing principal", route: modulehttp.Route{Action: moduleHTTPTestAction("module.resource.self", actioncontract.AuthorizationAuthenticatedPrincipal)}, want: http.StatusUnauthorized},
		{name: "principal only", route: modulehttp.Route{Action: moduleHTTPTestAction("module.resource.self", actioncontract.AuthorizationAuthenticatedPrincipal)}, principal: &known, want: http.StatusNoContent, executed: true},
		{name: "required permission denied", route: modulehttp.Route{Action: moduleHTTPTestAction("module.resource.read", actioncontract.AuthorizationExactRolePermission)}, principal: &known, want: http.StatusForbidden},
		{name: "workspace admin is not an implicit module grant", route: modulehttp.Route{Action: moduleHTTPTestAction("module.resource.read", actioncontract.AuthorizationExactRolePermission)}, principal: &workspaceAdmin, want: http.StatusForbidden},
		{name: "required permission allowed", route: modulehttp.Route{Action: moduleHTTPTestAction("module.resource.read", actioncontract.AuthorizationExactRolePermission)}, principal: &read, want: http.StatusNoContent, executed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, executed := call(test.route, test.principal)
			if status != test.want || executed != test.executed {
				t.Fatalf("status=%d executed=%t want status=%d executed=%t", status, executed, test.want, test.executed)
			}
		})
	}
}

func moduleHTTPTestAction(key string, strategy actioncontract.AuthorizationStrategy) actioncontract.ActionDefinition {
	separator := strings.LastIndex(key, ".")
	action := actioncontract.ActionDefinition{
		Key: key, Owner: "module:test", SourceKind: "module_surface", CapabilityKey: "module.resource", CapabilityLabel: "Module resource",
		OperationKey: key[separator+1:], OperationLabel: key, Label: key, Exposures: []actioncontract.Exposure{actioncontract.ExposureTenantAdmin},
		Authorization: actioncontract.Authorization{Strategy: strategy}, HTTP: &actioncontract.HTTPBinding{Method: "GET", RouteTemplate: "/module-resource"},
		EffectClass: actioncontract.EffectRead, RiskLevel: actioncontract.RiskLow, IdempotencyDecision: "not_applicable", AuditClass: "module_resource_read", LifecycleStatus: actioncontract.LifecycleActive,
	}
	if strategy == actioncontract.AuthorizationExactRolePermission {
		action.Permission = &actioncontract.PermissionDefinition{Key: key, Owner: action.Owner, ResourceKey: key[:separator], ActionKey: key[separator+1:], Label: key, Category: "Module", LifecycleStatus: actioncontract.LifecycleActive}
	} else if strategy == actioncontract.AuthorizationAnonymousProtocol {
		action.Authorization.PolicyKey = "module.public_protocol"
	}
	return action
}

func moduleHTTPAuthorizationPrincipal(permissions ...string) principalmodel.Principal {
	grants := make([]identitysdk.FunctionGrant, 0, len(permissions))
	for _, permission := range permissions {
		separator := strings.LastIndexByte(permission, '.')
		if separator <= 0 || separator == len(permission)-1 {
			continue
		}
		grants = append(grants, identitysdk.FunctionGrant{
			Resource: identitysdk.ResourceType(permission[:separator]),
			Action:   identitysdk.Action(permission[separator+1:]),
			Effect:   identitysdk.EffectAllow,
		})
	}
	return principalmodel.Principal{Principal: identitysdk.Principal{
		Known: true,
		AccessBundle: &identitysdk.AccessBundle{
			FunctionGrants: grants,
		},
	}}
}
