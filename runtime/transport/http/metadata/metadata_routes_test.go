package metadata

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTenantMetadataRoutesUseAuthenticatedOwnerPermissionBoundary(t *testing.T) {
	handler := NewMetadataHandler(MetadataDependencies{
		Admin: func(http.HandlerFunc) http.HandlerFunc {
			return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }
		},
		Authenticated: func(http.HandlerFunc) http.HandlerFunc {
			return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
		},
	})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/tenant-admin/metadata/definitions/object"},
		{http.MethodGet, "/tenant-admin/metadata/localized-texts"},
		{http.MethodPost, "/tenant-admin/metadata/reload"},
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(route.method, route.path, nil))
		if response.Code != http.StatusNoContent {
			t.Fatalf("%s %s still uses Admin Console wrapper: status=%d", route.method, route.path, response.Code)
		}
	}
}

func TestRegisterRoutesWithoutProvisionHandler(t *testing.T) {
	handler := &MetadataHandler{admin: func(handle http.HandlerFunc) http.HandlerFunc { return handle }}
	mux := http.NewServeMux()

	handler.RegisterRoutes(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/tenant-admin/metadata/manifests/validate", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("unexpected provision route status=%d", response.Code)
	}
}

func TestRegisterRoutesWithProvisionHandler(t *testing.T) {
	handler := &MetadataHandler{admin: func(handle http.HandlerFunc) http.HandlerFunc { return handle }, provisionRequired: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }}
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/tenant-admin/metadata/manifests/validate", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("provision route status=%d", response.Code)
	}
	legacy := httptest.NewRecorder()
	mux.ServeHTTP(legacy, httptest.NewRequest(http.MethodPost, "/metadata/manifests/validate", nil))
	if legacy.Code != http.StatusNotFound {
		t.Fatalf("legacy provision route status=%d", legacy.Code)
	}
}
