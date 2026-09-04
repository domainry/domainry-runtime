package appschema

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRuntimeOnlyRegistersRuntimeOwnedApplicationSchemaRoutes(t *testing.T) {
	handler := NewApplicationSchemaHandler(ApplicationSchemaDependencies{
		Authenticated: func(http.HandlerFunc) http.HandlerFunc {
			return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
		},
	})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/application-schema/definitions/object/account/validate", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("Runtime authoring validation status=%d", response.Code)
	}
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/metadata/definitions/object"},
		{http.MethodGet, "/metadata/localized-texts"},
		{http.MethodGet, "/metadata/dictionaries/status/items"},
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(route.method, route.path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("Runtime still registers Metadata-owned route %s %s: status=%d", route.method, route.path, response.Code)
		}
	}
}

func TestRegisterRoutesWithoutProvisionHandler(t *testing.T) {
	handler := &ApplicationSchemaHandler{authenticated: func(handle http.HandlerFunc) http.HandlerFunc { return handle }}
	mux := http.NewServeMux()

	handler.RegisterRoutes(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/provision/manifests/validate", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("unexpected provision route status=%d", response.Code)
	}
}

func TestApplicationSchemaDoesNotOwnProvisionRoutes(t *testing.T) {
	handler := &ApplicationSchemaHandler{authenticated: func(handle http.HandlerFunc) http.HandlerFunc { return handle }}
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	for _, path := range []string{
		"/provision/manifests/validate",
		"/provision/manifests/review",
		"/provision/manifests/apply",
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("online manifest mutation route %s status=%d", path, response.Code)
		}
	}
	current := httptest.NewRecorder()
	mux.ServeHTTP(current, httptest.NewRequest(http.MethodGet, "/provision/manifests/current", nil))
	if current.Code != http.StatusNotFound {
		t.Fatalf("Application Schema still owns Provision current route: status=%d", current.Code)
	}
	legacy := httptest.NewRecorder()
	mux.ServeHTTP(legacy, httptest.NewRequest(http.MethodPost, "/provision/manifests/validate", nil))
	if legacy.Code != http.StatusNotFound {
		t.Fatalf("legacy provision route status=%d", legacy.Code)
	}
}
