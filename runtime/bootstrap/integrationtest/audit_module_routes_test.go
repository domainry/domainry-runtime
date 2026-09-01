package integrationtest

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

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
	for _, surface := range runtime.ModuleHTTPSurfaces() {
		if surface.Owner() != "audit" {
			continue
		}
		found = true
		if err := modulehttp.ValidateSurface(surface); err != nil {
			t.Fatal(err)
		}
		for _, route := range surface.Routes() {
			next := surface.Handler()
			mux.Handle(route.Pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				principal := integrationAuditPrincipal(r.Header.Get("Authorization"))
				ctx := identitysdk.WithRequestIdentity(r.Context(), identitysdk.RequestIdentity{Principal: principal})
				next.ServeHTTP(w, r.WithContext(ctx))
			}))
		}
	}
	if !found {
		t.Fatal("Audit Module HTTP surface is missing")
	}
	mux.Handle("/", runtime.Routes())
	return mux
}

// integrationModuleOwnerRoutes mirrors the process host's module-surface
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
	for _, surface := range runtime.ModuleHTTPSurfaces() {
		if surface == nil || !wanted[surface.Owner()] {
			continue
		}
		found[surface.Owner()] = true
		if err := modulehttp.ValidateSurface(surface); err != nil {
			t.Fatal(err)
		}
		for _, route := range surface.Routes() {
			surfaceHandler := surface.Handler()
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
			switch {
			case len(route.AnyPermissions) != 0:
				next = identityMiddleware.RequireAnyPermission(route.AnyPermissions, next)
			case strings.TrimSpace(route.Permission) != "":
				next = identityMiddleware.RequirePermission(route.Permission, next)
			default:
				next = identityMiddleware.RequireAuthenticated(next)
			}
			mux.Handle(route.Pattern, identityMiddleware.Authenticate(next))
		}
	}
	for _, owner := range owners {
		if !found[owner] {
			t.Fatalf("%s Module HTTP surface is missing", owner)
		}
	}
	mux.Handle("/", runtime.Routes())
	return mux
}

func integrationModulePrincipal(authorization string) identitysdk.Principal {
	subject, role := integrationFixtureTokenSubjectRole(strings.TrimSpace(strings.TrimPrefix(authorization, "Bearer ")))
	permissions := []string{"*"}
	recordScope := "all_records"
	switch role {
	case "sales_rep":
		permissions = []string{"business.access", "customer.read", "customer.update", "contact.read", "contact.create", "lead.read", "lead.update", "opportunity.read", "opportunity.update", "activity.read", "activity.create", "activity.update", "contract.read"}
		recordScope = "owned_records"
	case "finance_reviewer":
		permissions = []string{"business.access", "customer.read", "opportunity.read", "contract.read", "payment.read", "payment.update", "payment.export"}
	case "restricted":
		permissions = []string{"business.access", "customer.read"}
	}
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{
		ContractVersion: identitysdk.PrincipalContextContractVersion,
		Known:           true, WorkspaceID: "workspace-primary", UserID: subject, RoleKey: role,
	}}, accessfixture.Bundle{Key: role, Permissions: permissions, RecordScope: recordScope}).Principal
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
	}}, accessfixture.Bundle{Key: role, Permissions: []string{"*"}, RecordScope: "all_records"})
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
