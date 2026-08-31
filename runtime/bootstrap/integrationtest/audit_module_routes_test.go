package integrationtest

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/modulehttp"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	bootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
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
