package discovery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"

	metadataapplication "github.com/domainry/domainry-runtime/runtime/application/metadata"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type discoverySchemaProvider struct{}

func (discoverySchemaProvider) SchemaForPrincipal(context.Context, principalmodel.Principal) metadatamodel.ApplicationSchemaSnapshot {
	return metadatamodel.ApplicationSchemaSnapshot{TemplateID: "discovery", SchemaHash: "schema-hash"}
}

func discoveryTestHandler() *DiscoveryHandler {
	return NewDiscoveryHandler(DiscoveryDependencies{
		Schema: metadataapplication.NewMetadataSchemaApplicationService(discoverySchemaProvider{}, nil),
		Principal: func(*http.Request) principalmodel.Principal {
			return principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}
		},
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteError: func(w http.ResponseWriter, _ *http.Request, status int, code string, _ ...string) {
			w.Header().Set("X-Error-Code", code)
			w.WriteHeader(status)
		},
	})
}

func TestDiscoveryLocalesAndResources(t *testing.T) {
	handler := discoveryTestHandler()
	response := httptest.NewRecorder()
	handler.i18nLocales(response, httptest.NewRequest(http.MethodGet, "/i18n/locales", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("locales status=%d", response.Code)
	}

	response = httptest.NewRecorder()
	handler.i18nResources(response, httptest.NewRequest(http.MethodGet, "/i18n/resources?locale=unsupported", nil))
	if response.Code != http.StatusBadRequest || response.Header().Get("X-Error-Code") != "localization.unsupported_locale" {
		t.Fatalf("unsupported locale status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}

	request := httptest.NewRequest(http.MethodGet, "/i18n/resources?locale=en-US", nil)
	response = httptest.NewRecorder()
	handler.i18nResources(response, request)
	if response.Code != http.StatusOK || response.Header().Get("ETag") == "" {
		t.Fatalf("resources status=%d etag=%q", response.Code, response.Header().Get("ETag"))
	}
	etag := response.Header().Get("ETag")
	request = httptest.NewRequest(http.MethodGet, "/i18n/resources?locale=en-US", nil)
	request.Header.Set("If-None-Match", "  "+etag+" ")
	response = httptest.NewRecorder()
	handler.i18nResources(response, request)
	if response.Code != http.StatusNotModified {
		t.Fatalf("cached resources status=%d", response.Code)
	}
}

func TestDiscoverySchemaAndRoutes(t *testing.T) {
	handler := discoveryTestHandler()
	request := httptest.NewRequest(http.MethodGet, "/schema", nil)
	response := httptest.NewRecorder()
	handler.getSchema(response, request)
	if response.Code != http.StatusOK || response.Header().Get("ETag") != `"schema-hash"` {
		t.Fatalf("schema status=%d etag=%q", response.Code, response.Header().Get("ETag"))
	}
	request = httptest.NewRequest(http.MethodGet, "/schema", nil)
	request.Header.Set("If-None-Match", `"schema-hash"`)
	response = httptest.NewRecorder()
	handler.getSchema(response, request)
	if response.Code != http.StatusNotModified {
		t.Fatalf("cached schema status=%d", response.Code)
	}

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/i18n/locales", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("registered locale route status=%d", response.Code)
	}
}

func TestDiscoveryPublishedRuntimeAndSurfaceRoutes(t *testing.T) {
	handler := discoveryTestHandler()
	response := httptest.NewRecorder()
	handler.getBusinessRuntimeSchema(response, httptest.NewRequest(http.MethodGet, "/runtime-schema", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("unscoped runtime schema status=%d", response.Code)
	}
	handler.principal = func(*http.Request) principalmodel.Principal {
		return principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "user"}, SurfaceKey: "business_workspace"}
	}
	response = httptest.NewRecorder()
	handler.getBusinessRuntimeSchema(response, httptest.NewRequest(http.MethodGet, "/runtime-schema", nil))
	if response.Code != http.StatusOK || response.Header().Get("ETag") != `"schema-hash"` {
		t.Fatalf("runtime schema status=%d etag=%q", response.Code, response.Header().Get("ETag"))
	}
	request := httptest.NewRequest(http.MethodGet, "/runtime-schema", nil)
	request.Header.Set("If-None-Match", ` "schema-hash" `)
	response = httptest.NewRecorder()
	handler.getPortalRuntimeSchema(response, request)
	if response.Code != http.StatusNotModified {
		t.Fatalf("cached runtime schema status=%d", response.Code)
	}

	response = httptest.NewRecorder()
	handler.getBusinessSurfaceContext(response, httptest.NewRequest(http.MethodGet, "/surface-context", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("business surface status=%d", response.Code)
	}
	response = httptest.NewRecorder()
	handler.getPortalSurfaceContext(response, httptest.NewRequest(http.MethodGet, "/surface-context", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("portal surface mismatch status=%d", response.Code)
	}
}
