package frontendcapability

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	deploymentbusiness "github.com/domainry/domainry-runtime/runtime/application/deployment"
	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	deploymentrepository "github.com/domainry/domainry-runtime/runtime/domain/deployment/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type failingFrontendCapabilityRepository struct{}

func (failingFrontendCapabilityRepository) Get(context.Context, string) (deploymentmodel.DeploymentFrontendCapabilityState, bool, error) {
	return deploymentmodel.DeploymentFrontendCapabilityState{}, false, errors.New("get failed")
}

func (failingFrontendCapabilityRepository) Put(context.Context, string, []byte) (deploymentmodel.DeploymentFrontendCapabilityState, error) {
	return deploymentmodel.DeploymentFrontendCapabilityState{}, errors.New("put failed")
}

func frontendCapabilityTestManifest() deploymentmodel.FrontendCapabilityManifest {
	return deploymentmodel.FrontendCapabilityManifest{
		ManifestVersion:         deploymentmodel.FrontendCapabilityManifestVersion,
		FrontendVersion:         "frontend-1",
		RuntimeContractVersions: []string{"runtime-authoring-v1"},
		Entries: []deploymentmodel.FrontendCapabilitySupportEntry{{
			SupportKey: "metadata.field.editor.v1", CapabilityKeys: []string{"schema.field"}, Route: "/system/metadata",
			RequiredPermissions: []string{"workspace.admin", "*"}, FeatureModule: "features/metadata.tsx", AcceptanceTests: []string{"metadata.spec.ts"},
		}},
	}
}

func frontendCapabilityTestService(repository deploymentrepository.DeploymentFrontendCapabilityRepository) *deploymentbusiness.DeploymentFrontendCapabilityApplicationService {
	return deploymentbusiness.NewDeploymentFrontendCapabilityApplicationService(deploymentbusiness.FrontendCapabilityDependencies{
		Repository: repository, ContractVersion: "runtime-authoring-v1",
		Capabilities: func() []deploymentmodel.FrontendCapabilityDefinition {
			return []deploymentmodel.FrontendCapabilityDefinition{{Key: "schema.field", FrontendSupportKey: "metadata.field.editor.v1", Permissions: []string{"workspace.admin", "*"}}}
		},
	})
}

func frontendCapabilityPrincipal() principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin", "*"}})
}

func frontendCapabilityHandler(service *deploymentbusiness.DeploymentFrontendCapabilityApplicationService, principal principalmodel.Principal) *FrontendCapabilityHandler {
	return NewFrontendCapabilityHandler(FrontendCapabilityDependencies{
		Service: service, Principal: func(*http.Request) principalmodel.Principal { return principal },
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteError: func(w http.ResponseWriter, _ *http.Request, status int, code string, _ ...string) {
			w.Header().Set("X-Error-Code", code)
			w.WriteHeader(status)
		},
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, _ error) { w.WriteHeader(http.StatusUnprocessableEntity) },
		DecodeJSON: func(w http.ResponseWriter, r *http.Request, target any) bool {
			if err := json.NewDecoder(r.Body).Decode(target); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return false
			}
			return true
		},
		Admin: func(next http.HandlerFunc) http.HandlerFunc { return next },
	})
}

func TestFrontendCapabilityHandlers(t *testing.T) {
	admin := frontendCapabilityPrincipal()
	handler := frontendCapabilityHandler(frontendCapabilityTestService(nil), admin)
	manifestJSON, err := json.Marshal(frontendCapabilityTestManifest())
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.validateManifest(response, httptest.NewRequest(http.MethodPost, "/frontend-capability-manifest/validate", strings.NewReader("{")))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("invalid JSON status=%d", response.Code)
	}
	response = httptest.NewRecorder()
	handler.validateManifest(response, httptest.NewRequest(http.MethodPost, "/frontend-capability-manifest/validate", strings.NewReader(string(manifestJSON))))
	if response.Code != http.StatusOK {
		t.Fatalf("validate status=%d", response.Code)
	}

	response = httptest.NewRecorder()
	handler.getManifest(response, httptest.NewRequest(http.MethodGet, "/frontend-capability-manifest", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("snapshot status=%d", response.Code)
	}

	unknown := frontendCapabilityHandler(frontendCapabilityTestService(nil), principalmodel.Principal{})
	response = httptest.NewRecorder()
	unknown.getManifest(response, httptest.NewRequest(http.MethodGet, "/frontend-capability-manifest", nil))
	if response.Code != http.StatusForbidden || response.Header().Get("X-Error-Code") != "auth.permission_denied" {
		t.Fatalf("unknown snapshot status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}
	response = httptest.NewRecorder()
	unknown.validateManifest(response, httptest.NewRequest(http.MethodPost, "/frontend-capability-manifest/validate", strings.NewReader(string(manifestJSON))))
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown validation status=%d", response.Code)
	}
}

func TestFrontendCapabilityOpsStatusUsesDedicatedBackendPermission(t *testing.T) {
	principal := frontendCapabilityPrincipal()
	accessfixture.Set(&principal, accessfixture.Bundle{Permissions: []string{"runtime_ops.capability_status.read"}})
	handler := frontendCapabilityHandler(frontendCapabilityTestService(nil), principal)

	response := httptest.NewRecorder()
	handler.getOpsStatus(response, httptest.NewRequest(http.MethodGet, "/operations/capability-status", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("ops status=%d body=%s", response.Code, response.Body.String())
	}

	accessfixture.Set(&principal, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	handler = frontendCapabilityHandler(frontendCapabilityTestService(nil), principal)
	response = httptest.NewRecorder()
	handler.getOpsStatus(response, httptest.NewRequest(http.MethodGet, "/operations/capability-status", nil))
	if response.Code != http.StatusForbidden || response.Header().Get("X-Error-Code") != "auth.permission_denied" {
		t.Fatalf("workspace admin ops status=%d code=%q", response.Code, response.Header().Get("X-Error-Code"))
	}
	unknown := frontendCapabilityHandler(frontendCapabilityTestService(nil), principalmodel.Principal{})
	response = httptest.NewRecorder()
	unknown.getOpsStatus(response, httptest.NewRequest(http.MethodGet, "/operations/capability-status", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("unknown ops status=%d", response.Code)
	}
	knownNonAdmin := frontendCapabilityPrincipal()
	accessfixture.Set(&knownNonAdmin, accessfixture.Bundle{})
	response = httptest.NewRecorder()
	frontendCapabilityHandler(frontendCapabilityTestService(nil), knownNonAdmin).getManifest(response, httptest.NewRequest(http.MethodGet, "/frontend-capability-manifest", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("known non-admin manifest status=%d", response.Code)
	}
	ops := frontendCapabilityPrincipal()
	accessfixture.Set(&ops, accessfixture.Bundle{Permissions: []string{"runtime_ops.capability_status.read"}})
	response = httptest.NewRecorder()
	frontendCapabilityHandler(frontendCapabilityTestService(failingFrontendCapabilityRepository{}), ops).getOpsStatus(response, httptest.NewRequest(http.MethodGet, "/operations/capability-status", nil))
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("failing ops status=%d", response.Code)
	}
}

func TestFrontendCapabilityManifestRoutesUseAuthenticatedOwnerPermissionBoundary(t *testing.T) {
	handler := NewFrontendCapabilityHandler(FrontendCapabilityDependencies{
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
		{http.MethodGet, "/frontend-capability-manifest"},
		{http.MethodPost, "/frontend-capability-manifest/validate"},
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(route.method, route.path, nil))
		if response.Code != http.StatusNoContent {
			t.Fatalf("%s %s still uses Admin Console wrapper: status=%d", route.method, route.path, response.Code)
		}
	}
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/frontend-capability-manifest", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("retired online manifest registration status=%d", response.Code)
	}
}

func TestFrontendCapabilityServiceErrorsAndRoutes(t *testing.T) {
	admin := frontendCapabilityPrincipal()
	handler := frontendCapabilityHandler(frontendCapabilityTestService(failingFrontendCapabilityRepository{}), admin)
	manifestJSON, err := json.Marshal(frontendCapabilityTestManifest())
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.getManifest(response, httptest.NewRequest(http.MethodGet, "/frontend-capability-manifest", nil))
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("snapshot repository failure status=%d", response.Code)
	}

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/frontend-capability-manifest/validate", strings.NewReader(string(manifestJSON))))
	if response.Code != http.StatusOK {
		t.Fatalf("registered validate route status=%d", response.Code)
	}
}
