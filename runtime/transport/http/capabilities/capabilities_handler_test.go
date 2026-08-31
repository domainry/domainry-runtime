package capabilities

import (
	"context"
	"encoding/json"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	capabilityapplication "github.com/domainry/domainry-runtime/runtime/application/capability"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestPlatformCapabilitiesUsesAuthenticatedPrincipal(t *testing.T) {
	var captured principalmodel.Principal
	authenticated := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})
	service := capabilityapplication.NewCapabilityAuthoringApplicationService(func(_ context.Context, principal principalmodel.Principal) capabilitycontract.CapabilityInstanceSchema {
		captured = principal
		return capabilitycontract.CapabilityInstanceSchema{}
	})
	handler := NewCapabilitiesHandler(CapabilitiesDependencies{
		Service:   service,
		Principal: func(*http.Request) principalmodel.Principal { return authenticated },
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteServiceError: func(http.ResponseWriter, *http.Request, error) { t.Fatal("unexpected service error") },
	})
	response := httptest.NewRecorder()
	handler.platformCapabilities(response, httptest.NewRequest(http.MethodGet, "/tenant-admin/platform-capabilities", nil))
	if response.Code != http.StatusOK || captured.UserID != authenticated.UserID || !captured.Known {
		t.Fatalf("status=%d captured principal=%#v", response.Code, captured)
	}
}

func TestPlatformCapabilitiesWritesServiceErrorForNonAdministrator(t *testing.T) {
	service := capabilityapplication.NewCapabilityAuthoringApplicationService(func(context.Context, principalmodel.Principal) capabilitycontract.CapabilityInstanceSchema {
		return capabilitycontract.CapabilityInstanceSchema{}
	})
	serviceErrorCalled := false
	handler := NewCapabilitiesHandler(CapabilitiesDependencies{
		Service: service,
		Principal: func(*http.Request) principalmodel.Principal {
			return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"record.read"}})
		},
		WriteJSON: func(http.ResponseWriter, int, any) { t.Fatal("unexpected JSON response") },
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, err error) {
			serviceErrorCalled = err != nil
			w.WriteHeader(http.StatusForbidden)
		},
	})
	response := httptest.NewRecorder()
	handler.platformCapabilities(response, httptest.NewRequest(http.MethodGet, "/tenant-admin/platform-capabilities", nil))
	if response.Code != http.StatusForbidden || !serviceErrorCalled {
		t.Fatalf("status=%d serviceErrorCalled=%v", response.Code, serviceErrorCalled)
	}
}

func TestCapabilityDiscoveryRoutesLoadIndexDomainDetailAndReferences(t *testing.T) {
	service := capabilityapplication.NewCapabilityAuthoringApplicationService(func(context.Context, principalmodel.Principal) capabilitycontract.CapabilityInstanceSchema {
		return capabilitycontract.CapabilityInstanceSchema{
			Objects:      []definitionmodel.ObjectSchema{{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "status"}}}},
			Integrations: connectormodel.IntegrationSchema{Connectors: []connectormodel.ConnectorSchema{{Key: "webhook", Providers: []connectormodel.ConnectorProviderSchema{{Key: "generic", ConfigFields: []definitionmodel.FieldSchema{{Key: "url", Name: "URL", Type: "text", Required: true}}}}}}},
		}
	})
	handler := NewCapabilitiesHandler(CapabilitiesDependencies{
		Service: service, Principal: func(*http.Request) principalmodel.Principal {
			return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})
		},
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, _ error) { w.WriteHeader(http.StatusBadRequest) },
	})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	for _, path := range []string{
		"/tenant-admin/platform-capabilities/index",
		"/tenant-admin/platform-capabilities/domains/schema?status=supported",
		"/tenant-admin/platform-capabilities/capabilities/schema.object",
		"/tenant-admin/platform-capabilities/capabilities/integration.connection?connector_key=webhook&provider_key=generic",
		"/tenant-admin/platform-capabilities/references/field_key?scope=order",
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK || response.Header().Get("ETag") == "" {
			t.Fatalf("path=%s status=%d etag=%q body=%s", path, response.Code, response.Header().Get("ETag"), response.Body.String())
		}
		if strings.Contains(path, "integration.connection") && !strings.Contains(response.Body.String(), `"connector_key":"webhook"`) {
			t.Fatalf("path=%s response did not include specialization: %s", path, response.Body.String())
		}
		cached := httptest.NewRequest(http.MethodGet, path, nil)
		cached.Header.Set("If-None-Match", response.Header().Get("ETag"))
		cachedResponse := httptest.NewRecorder()
		mux.ServeHTTP(cachedResponse, cached)
		if cachedResponse.Code != http.StatusNotModified {
			t.Fatalf("path=%s cached status=%d", path, cachedResponse.Code)
		}
	}
}

func TestCapabilityDiscoveryHandlersMapAuthorizationAndHashFailures(t *testing.T) {
	service := capabilityapplication.NewCapabilityAuthoringApplicationService(func(context.Context, principalmodel.Principal) capabilitycontract.CapabilityInstanceSchema {
		return capabilitycontract.CapabilityInstanceSchema{}
	})
	serviceErrors := 0
	handler := NewCapabilitiesHandler(CapabilitiesDependencies{
		Service: service,
		Principal: func(*http.Request) principalmodel.Principal {
			return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"record.read"}})
		},
		WriteJSON: func(http.ResponseWriter, int, any) { t.Fatal("unexpected JSON response") },
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, err error) {
			if err == nil {
				t.Fatal("nil service error")
			}
			serviceErrors++
			w.WriteHeader(http.StatusForbidden)
		},
	})
	for _, test := range []struct {
		path string
		call func(http.ResponseWriter, *http.Request)
	}{
		{path: "/tenant-admin/platform-capabilities/index", call: handler.capabilityIndex},
		{path: "/tenant-admin/platform-capabilities/domains/schema", call: handler.capabilityDomain},
		{path: "/tenant-admin/platform-capabilities/capabilities/schema.object", call: handler.capabilityDetail},
		{path: "/tenant-admin/platform-capabilities/references/object_key", call: handler.capabilityReferences},
	} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		request.SetPathValue("domainKey", "schema")
		request.SetPathValue("capabilityKey", "schema.object")
		request.SetPathValue("kind", "object_key")
		response := httptest.NewRecorder()
		test.call(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("path=%s status=%d", test.path, response.Code)
		}
	}

	handler.principal = func(*http.Request) principalmodel.Principal {
		return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})
	}
	request := httptest.NewRequest(http.MethodGet, "/tenant-admin/platform-capabilities/index", nil)
	request.Header.Set("If-Match-Contract-Hash", "stale-contract")
	response := httptest.NewRecorder()
	handler.capabilityIndex(response, request)
	if response.Code != http.StatusForbidden || serviceErrors != 5 {
		t.Fatalf("hash status=%d service errors=%d", response.Code, serviceErrors)
	}
}
