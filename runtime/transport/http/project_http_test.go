package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestProjectHTTPUsesRuntimeAuthenticationWithoutCompiledActionRoute(t *testing.T) {
	called := false
	project := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		called = true
		principal, ok := (&HTTPRouter{}).PrincipalFromContext(request.Context())
		if !ok || principal.UserID != "developer" {
			t.Fatalf("principal=%+v ok=%v", principal, ok)
		}
		identity, ok := identitysdk.RequestIdentityFromContext(request.Context())
		if !ok || !identity.Principal.Known || identity.Principal.UserID != "developer" {
			t.Fatalf("request identity=%+v ok=%v", identity, ok)
		}
		response.WriteHeader(http.StatusNoContent)
	})
	known := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "developer"}}
	router := &HTTPRouter{projectHTTP: project, allowDevAuthHeaders: true, identityAuthorization: routerIdentityAuthorizationStub{principal: known}}
	mux := http.NewServeMux()
	mux.Handle("/api/", project)
	handler := router.withAuth(mux, router.withActionAuthorization(mux, mux))

	request := httptest.NewRequest(http.MethodPost, "/api/crm/opportunities/one/win", nil)
	request.Header.Set("X-User-ID", "developer")
	request.Header.Set("X-Role", "sales_manager")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent || !called {
		t.Fatalf("status=%d called=%v body=%s", response.Code, called, response.Body.String())
	}
}

func TestProjectHTTPReceivesResolvedSessionIdentityAndAccessToken(t *testing.T) {
	bundle := &identitysdk.AccessBundle{
		FunctionGrants: []identitysdk.FunctionGrant{{Resource: "opportunity", Action: "win", Effect: identitysdk.EffectAllow}},
		DataPolicies: []identitysdk.DataPolicy{{
			Key: "opportunity.win", Resource: "opportunity", Action: "win", Effect: identitysdk.EffectAllow,
			DataScopes: []identitysdk.DataScope{identitysdk.DataScopeAll},
		}},
	}
	principal := identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "seller", AccessBundle: bundle}
	project := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		resolved, ok := (&HTTPRouter{}).PrincipalFromContext(request.Context())
		if !ok || resolved.EffectiveAuthorizationRevision() != "business-auth" {
			t.Fatalf("resolved principal=%+v ok=%v", resolved, ok)
		}
		identity, ok := identitysdk.RequestIdentityFromContext(request.Context())
		if !ok || identity.AccessToken != "session-token" || !identity.Principal.HasPermission("opportunity.win") {
			t.Fatalf("request identity=%+v ok=%v", identity, ok)
		}
		response.WriteHeader(http.StatusNoContent)
	})
	router := routerWithIdentitySDK(principal)
	router.businessPrincipal = projectBusinessPrincipalResolver{authorizationRevision: "business-auth"}
	router.projectHTTP = project
	mux := http.NewServeMux()
	mux.Handle("/api/", project)
	handler := router.withAuth(mux, router.withActionAuthorization(mux, mux))

	request := httptest.NewRequest(http.MethodPost, "/api/crm/opportunities/one/win", nil)
	request.Header.Set("Authorization", "Bearer session-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

type projectBusinessPrincipalResolver struct {
	authorizationRevision string
}

func (resolver projectBusinessPrincipalResolver) ResolveBusinessPrincipal(_ context.Context, principal principalmodel.Principal, _, _ string) (principalmodel.Principal, error) {
	principal.BusinessAuthorizationRevision = resolver.authorizationRevision
	return principal, nil
}

func TestUseProjectHTTPStoresOnlyExplicitHandler(t *testing.T) {
	router := &HTTPRouter{}
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	if result := UseProjectHTTP(router, handler); result != router || result.projectHTTP == nil {
		t.Fatal("project HTTP handler was not installed")
	}
	UseProjectHTTP(nil, handler)
}

func TestProjectHTTPReplacesGenericRecordHTTPRoutes(t *testing.T) {
	router := completeRouterForListenerGroupTests(HTTPRouterConfig{})
	recordRegistrations := 0
	router.recordHTTP = routerCountingRegistrar{calls: &recordRegistrations}
	projectCalls := 0
	router.projectHTTP = http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		projectCalls++
		response.WriteHeader(http.StatusNoContent)
	})
	router.allowDevAuthHeaders = true
	router.identityAuthorization = routerIdentityAuthorizationStub{principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "developer"}}}

	handler := router.Routes()
	if recordRegistrations != 0 {
		t.Fatalf("generic record routes registered %d times", recordRegistrations)
	}
	projectRequest := httptest.NewRequest(http.MethodGet, "/api/crm/dashboard", nil)
	projectRequest.Header.Set("X-User-ID", "developer")
	projectResponse := httptest.NewRecorder()
	handler.ServeHTTP(projectResponse, projectRequest)
	if projectResponse.Code != http.StatusNoContent || projectCalls != 1 {
		t.Fatalf("project status=%d calls=%d body=%s", projectResponse.Code, projectCalls, projectResponse.Body.String())
	}

	recordResponse := httptest.NewRecorder()
	handler.ServeHTTP(recordResponse, httptest.NewRequest(http.MethodGet, "/records/customer", nil))
	if recordResponse.Code != http.StatusNotFound {
		t.Fatalf("generic record status=%d body=%s", recordResponse.Code, recordResponse.Body.String())
	}
}
