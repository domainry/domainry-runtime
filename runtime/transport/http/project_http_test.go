package http

import (
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
