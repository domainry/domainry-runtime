package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type integrationAuthenticationStub struct {
	principal principalmodel.Principal
	key       integrationsdk.APIKey
	err       error
	calls     *int
}

func (s integrationAuthenticationStub) PrincipalFromIntegrationAPIKey(context.Context, string, string, string) (principalmodel.Principal, integrationsdk.APIKey, error) {
	if s.calls != nil {
		*s.calls++
	}
	return s.principal, s.key, s.err
}

type apiAuthenticationAuditEntry struct {
	event     string
	principal principalmodel.Principal
	metadata  map[string]any
}

type apiAuthenticationAuditStub struct {
	entries []apiAuthenticationAuditEntry
}

func (s *apiAuthenticationAuditStub) AppendWithMetadata(_ context.Context, _ string, event, _, _ string, principal principalmodel.Principal, _ string, _, _, metadata map[string]any) {
	s.entries = append(s.entries, apiAuthenticationAuditEntry{event: event, principal: principal, metadata: metadata})
}

func TestAPIKeyAuthenticationPropagatesPrincipalAndAuditsSuccess(t *testing.T) {
	audit := &apiAuthenticationAuditStub{}
	calls := 0
	bundle := &identitysdk.AccessBundle{
		FunctionGrants: []identitysdk.FunctionGrant{{Resource: "account", Action: "read", Effect: identitysdk.EffectAllow}},
		DataPolicies: []identitysdk.DataPolicy{{
			Key: "account.read", Resource: "account", Action: "read", Effect: identitysdk.EffectAllow,
			DataScopes: []identitysdk.DataScope{identitysdk.DataScopeAll},
		}},
	}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "service-a", RoleKey: "sales", AccessBundle: bundle}, RequestID: "request-a"}
	router := NewHTTPRouter(HTTPRouterConfig{}, HTTPRouterDependencies{
		IntegrationAuthentication: integrationAuthenticationStub{
			principal: principal,
			key:       integrationsdk.APIKey{Key: "sales-reader", Scopes: []string{"account.read"}},
			calls:     &calls,
		},
		SecurityAudit: audit,
	})
	routes := http.NewServeMux()
	routes.HandleFunc("GET /records/{objectKey}", func(response http.ResponseWriter, request *http.Request) {
		resolved, ok := principalFromContext(request)
		if !ok || !resolved.Known || resolved.UserID != "service-a" || resolved.WorkspaceID != "workspace-a" || !resolved.HasPermission("account.read") {
			t.Fatalf("API key principal was not propagated: %#v", resolved)
		}
		requestIdentity, ok := identitysdk.RequestIdentityFromContext(request.Context())
		if !ok || !requestIdentity.Principal.HasPermission("account.read") {
			t.Fatalf("API key SDK request identity was not propagated: %#v", requestIdentity)
		}
		response.WriteHeader(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/records/account", nil)
	request.Header.Set("X-API-Key", "itg_private")
	request.Header.Set("X-Workspace-ID", "workspace-a")
	response := httptest.NewRecorder()

	router.withAuth(routes, routes).ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(audit.entries) != 1 || audit.entries[0].event != "auth_api_authenticated" || audit.entries[0].principal.UserID != "service-a" {
		t.Fatalf("unexpected authentication audit: %#v", audit.entries)
	}
	if audit.entries[0].metadata["api_key"] != "sales-reader" || audit.entries[0].metadata["scope_count"] != 1 || audit.entries[0].metadata["permission_count"] != 1 {
		t.Fatalf("unexpected authentication audit metadata: %#v", audit.entries[0].metadata)
	}
	if calls != 1 {
		t.Fatalf("API key authentication calls=%d, want exactly one", calls)
	}
}

func TestAPIKeyAuthenticationFailsClosedWhenProviderIsUnavailable(t *testing.T) {
	audit := &apiAuthenticationAuditStub{}
	router := NewHTTPRouter(HTTPRouterConfig{}, HTTPRouterDependencies{SecurityAudit: audit})
	routes := http.NewServeMux()
	routes.HandleFunc("GET /records/{objectKey}", func(http.ResponseWriter, *http.Request) {
		t.Fatal("request reached handler without API key authentication")
	})
	request := httptest.NewRequest(http.MethodGet, "/records/account", nil)
	request.Header.Set("X-API-Key", "itg_private")
	request.Header.Set("X-Workspace-ID", "workspace-a")
	response := httptest.NewRecorder()

	router.withAuth(routes, routes).ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable || len(audit.entries) != 1 || audit.entries[0].event != "auth_api_denied" {
		t.Fatalf("status=%d body=%s audit=%#v", response.Code, response.Body.String(), audit.entries)
	}
}

func TestAPIKeyAuthenticationReturnsServiceUnavailableForProviderFailure(t *testing.T) {
	audit := &apiAuthenticationAuditStub{}
	calls := 0
	router := NewHTTPRouter(HTTPRouterConfig{}, HTTPRouterDependencies{
		IntegrationAuthentication: integrationAuthenticationStub{err: apperror.New(apperror.KindUnavailable, "backend.integration.api_key_authentication_unavailable", nil, nil), calls: &calls},
		SecurityAudit:             audit,
	})
	routes := http.NewServeMux()
	routes.HandleFunc("GET /records/{objectKey}", func(http.ResponseWriter, *http.Request) {
		t.Fatal("request reached handler while API key provider was unavailable")
	})
	request := httptest.NewRequest(http.MethodGet, "/records/account", nil)
	request.Header.Set("X-API-Key", "itg_private")
	request.Header.Set("X-Workspace-ID", "workspace-a")
	response := httptest.NewRecorder()

	router.withAuth(routes, routes).ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable || len(audit.entries) != 1 || audit.entries[0].metadata["reason"] != "api_key_authentication_unavailable" {
		t.Fatalf("status=%d body=%s audit=%#v", response.Code, response.Body.String(), audit.entries)
	}
	if calls != 1 {
		t.Fatalf("API key authentication calls=%d, want exactly one", calls)
	}
}
