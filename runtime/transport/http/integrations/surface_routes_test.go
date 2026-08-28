package integrations

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTenantIntegrationRoutesUseAuthenticatedOwnerPermissionBoundary(t *testing.T) {
	handler := NewIntegrationsHandler(IntegrationsDependencies{
		Admin: func(http.HandlerFunc) http.HandlerFunc {
			return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }
		},
		Authenticated: func(http.HandlerFunc) http.HandlerFunc {
			return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
		},
		Entrypoint: func(next http.HandlerFunc) http.HandlerFunc { return next },
	})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/tenant-admin/integrations/catalog"},
		{http.MethodPut, "/tenant-admin/integrations/connections/main"},
		{http.MethodPost, "/tenant-admin/integrations/connections/main/oauth/google/start"},
		{http.MethodPost, "/tenant-admin/integrations/api-keys"},
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(route.method, route.path, nil))
		if response.Code != http.StatusNoContent {
			t.Fatalf("%s %s still uses Admin Console wrapper: status=%d", route.method, route.path, response.Code)
		}
	}
}

func TestIntegrationRoutesDoNotExposeBusinessConnectionManagement(t *testing.T) {
	handler := &IntegrationsHandler{
		admin:         func(next http.HandlerFunc) http.HandlerFunc { return next },
		authenticated: func(next http.HandlerFunc) http.HandlerFunc { return next },
		entrypoint:    func(next http.HandlerFunc) http.HandlerFunc { return next },
	}
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	for _, path := range []string{
		"/business/tenant-admin/integrations/connections",
		"/business/tenant-admin/integrations/secrets",
		"/portal/tenant-admin/integrations/connections",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s returned %d, want 404", path, response.Code)
		}
	}
}

func TestWebPushSelfServiceUsesAuthenticatedBusinessBoundaryAndCleanupStaysAdminOnly(t *testing.T) {
	handler := NewIntegrationsHandler(IntegrationsDependencies{
		Admin: func(http.HandlerFunc) http.HandlerFunc {
			return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }
		},
		Authenticated: func(http.HandlerFunc) http.HandlerFunc {
			return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
		},
		Entrypoint: func(next http.HandlerFunc) http.HandlerFunc { return next },
	})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/business/notifications/web-push/readiness"},
		{http.MethodGet, "/business/notifications/web-push/subscriptions"},
		{http.MethodPut, "/business/notifications/web-push/subscriptions/sub"},
		{http.MethodPost, "/business/notifications/web-push/subscriptions/sub/revoke"},
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(route.method, route.path, nil))
		if response.Code != http.StatusNoContent {
			t.Fatalf("%s %s status=%d, want authenticated boundary", route.method, route.path, response.Code)
		}
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/integrations/web-push/subscriptions/cleanup-expired", nil))
	if response.Code != http.StatusTeapot {
		t.Fatalf("cleanup status=%d, want Admin-only boundary", response.Code)
	}

	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/integrations/web-push/subscriptions"},
		{http.MethodPut, "/integrations/web-push/subscriptions/sub"},
		{http.MethodPost, "/integrations/web-push/subscriptions/sub/revoke"},
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(route.method, route.path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("legacy route %s %s status=%d, want 404", route.method, route.path, response.Code)
		}
	}
}
