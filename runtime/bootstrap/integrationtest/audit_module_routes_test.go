package integrationtest

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulehttp"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityprincipal "github.com/domainry/domainry-identity-sdk/authorization/principal"
	identityhttpmiddleware "github.com/domainry/domainry-identity-sdk/httpmiddleware"
	bootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func auditModuleRoutes(t *testing.T, runtime *bootstrap.Runtime) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	found := false
	for _, adapter := range runtime.ModuleHTTPAdapters() {
		if adapter.Owner() != "audit" {
			continue
		}
		found = true
		if err := modulehttp.ValidateAdapter(adapter); err != nil {
			t.Fatal(err)
		}
		for _, route := range adapter.Routes() {
			next := adapter.Handler()
			mux.Handle(route.Pattern(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				principal := integrationAuditPrincipal(r.Header.Get("Authorization"))
				ctx := identitysdk.WithRequestIdentity(r.Context(), identitysdk.RequestIdentity{Principal: principal})
				next.ServeHTTP(w, r.WithContext(ctx))
			}))
		}
	}
	if !found {
		t.Fatal("Audit Module HTTP adapter is missing")
	}
	mux.Handle("/", runtime.Routes())
	return mux
}

// integrationModuleOwnerRoutes mirrors the process host's module-adapter
// attachment for owner HTTP tests that do not need to exercise the host's
// Identity middleware itself. The Runtime core router intentionally remains
// only the fallback for Runtime-owned routes.
func integrationModuleOwnerRoutes(t *testing.T, runtime *bootstrap.Runtime, owners ...string) http.Handler {
	t.Helper()
	identityBinding := newIntegrationIdentityBinding(t, config.Config{})
	resolver, err := identityprincipal.NewResolver(identityBinding, identityprincipal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	identityMiddleware, err := identityhttpmiddleware.New(resolver, identityhttpmiddleware.WithAuthorization(identityBinding.Authorization()))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	wanted := make(map[string]bool, len(owners))
	found := make(map[string]bool, len(owners))
	for _, owner := range owners {
		wanted[owner] = true
	}
	for _, adapter := range runtime.ModuleHTTPAdapters() {
		if adapter == nil || !wanted[adapter.Owner()] {
			continue
		}
		found[adapter.Owner()] = true
		if err := modulehttp.ValidateAdapter(adapter); err != nil {
			t.Fatal(err)
		}
		for _, route := range adapter.Routes() {
			surfaceHandler := adapter.Handler()
			next := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				identity, ok := identitysdk.RequestIdentityFromContext(r.Context())
				if !ok {
					http.Error(w, "authenticated module identity is missing", http.StatusUnauthorized)
					return
				}
				identity.Principal = integrationModulePrincipal(r.Header.Get("Authorization"))
				ctx := identitysdk.WithRequestIdentity(r.Context(), identity)
				surfaceHandler.ServeHTTP(w, r.WithContext(ctx))
			}))
			switch route.Action.Authorization.Strategy {
			case actioncontract.AuthorizationAuthenticated:
				if route.Action.Permission != nil && strings.TrimSpace(route.Action.Authorization.PolicyKey) == "" {
					next = identityMiddleware.RequirePermission(route.Action.Permission.Key, next)
				} else {
					next = identityMiddleware.RequireAuthenticated(next)
				}
			case actioncontract.AuthorizationAnonymous:
				mux.Handle(route.Pattern(), next)
				continue
			default:
				next = identityMiddleware.RequireAuthenticated(next)
			}
			mux.Handle(route.Pattern(), identityMiddleware.Authenticate(next))
		}
	}
	for _, owner := range owners {
		if !found[owner] {
			t.Fatalf("%s Module HTTP adapter is missing", owner)
		}
	}
	mux.Handle("/", runtime.Routes())
	return mux
}

func integrationModulePrincipal(authorization string) identitysdk.Principal {
	subject, role := integrationFixtureTokenSubjectRole(strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer ")))
	permissions := integrationIdentityRolePermissions(role)
	recordScope := "all"
	if role == "sales_rep" || role == "automation_business_tester" {
		recordScope = "owner"
	}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{
		ContractVersion: identitysdk.PrincipalContextContractVersion,
		Known:           true, WorkspaceID: "workspace-primary", UserID: subject, RoleKey: role,
	}}, accessfixture.Bundle{Key: role, Permissions: permissions, DataPolicies: accessfixture.DataPoliciesForPermissions(permissions, identitysdk.DataScope(recordScope))})
	principal.AccessBundle.Subject.TenantID = "tenant-primary"
	return principal.Principal
}

func integrationAuditPrincipal(authorization string) identitysdk.Principal {
	subject, role := integrationFixtureTokenSubjectRole(strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer ")))
	if subject == "" {
		subject = "admin"
	}
	if role == "" {
		role = "admin"
	}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{
		ContractVersion: identitysdk.PrincipalContextContractVersion, Known: true, WorkspaceID: "workspace-primary", UserID: subject, RoleKey: role,
	}}, accessfixture.Bundle{Key: role, Permissions: integrationIdentityRolePermissions(role), DataPolicies: accessfixture.DataPoliciesForPermissions(integrationIdentityRolePermissions(role), "all")})
	principal.AccessBundle.Subject.TenantID = "tenant-primary"
	return principal.Principal
}

func integrationFixtureTokenSubjectRole(token string) (string, string) {
	const prefix = "plane-testkit-token-v1."
	if !strings.HasPrefix(token, prefix) {
		return "", ""
	}
	parts := strings.Split(strings.TrimPrefix(token, prefix), ".")
	if len(parts) != 2 {
		return "", ""
	}
	decode := func(value string) string {
		raw, err := base64.RawURLEncoding.DecodeString(value)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(raw))
	}
	return decode(parts[0]), decode(parts[1])
}
