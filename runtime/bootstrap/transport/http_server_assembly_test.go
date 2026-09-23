package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	workerplatform "github.com/domainry/domainry-foundation/worker"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
)

func TestAssembleRuntimeHTTPServerRequiresConstructionContext(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered == nil {
			t.Fatal("nil construction context did not panic")
		}
	}()
	AssembleRuntimeHTTPServer(nil, HTTPServerDependencies{})
}

func TestAssembleRuntimeHTTPServerMinimalGraphWiresRoutesAndFallbackDependencies(t *testing.T) {
	services := composition.NewRuntimeServices(t.Context(), composition.RuntimeServicesConfig{})
	releaseIdentity := runtimehttp.RuntimeReleaseIdentity{ContractVersion: "domainry-runtime-release-identity-v1", CombinationSHA256: strings.Repeat("a", 64)}
	server := AssembleRuntimeHTTPServer(t.Context(), HTTPServerDependencies{
		Records:         services,
		IdentityBinding: transportIdentityBindingStub{},
		ReleaseIdentity: releaseIdentity,
		Config: config.Config{
			RuntimeAllowDevIdentityHeaders: true,
			UploadDir:                      " ",
			IdentityWorkspaceID:            "workspace-primary",
			IdentityAudience:               "domainry-runtime",
		},
	})
	if server == nil {
		t.Fatal("server is nil")
	}

	response := httptest.NewRecorder()
	server.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/live", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("liveness status=%d body=%s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	server.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if response.Code != http.StatusServiceUnavailable || response.Body.String() != "{\"status\":\"unavailable\"}\n" {
		t.Fatalf("readiness identity status=%d body=%s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("X-User-ID", "operator")
	request.Header.Set("X-Role", "operator")
	server.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("metrics status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestListenerRouteGroupsRegisterOnlyTheirCompiledEndpointInventory(t *testing.T) {
	services := composition.NewRuntimeServices(t.Context(), composition.RuntimeServicesConfig{})
	server := AssembleRuntimeHTTPServer(t.Context(), HTTPServerDependencies{
		Records:         services,
		IdentityBinding: transportIdentityBindingStub{},
		Config: config.Config{
			RuntimeAllowDevIdentityHeaders: true,
			IdentityWorkspaceID:            "workspace-primary",
			IdentityAudience:               "domainry-runtime",
			HTTPPublicOrigins:              []string{"https://app.example.com"},
			HTTPOpsOrigins:                 []string{"https://ops.example.com"},
			HTTPManagementOrigins:          []string{"https://admin.example.com"},
		},
	})
	assertStatus := func(group runtimehttp.ListenerRouteGroup, method, path string, headers map[string]string, want int) {
		t.Helper()
		request := httptest.NewRequest(method, path, nil)
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response := httptest.NewRecorder()
		server.RoutesForListenerGroup(group).ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("group=%s %s %s status=%d body=%s want=%d", group, method, path, response.Code, response.Body.String(), want)
		}
	}

	assertStatus(runtimehttp.ListenerRouteGroupPublic, http.MethodGet, "/live", nil, http.StatusOK)
	assertStatus(runtimehttp.ListenerRouteGroupPublic, http.MethodGet, "/metrics", nil, http.StatusNotFound)
	assertStatus(runtimehttp.ListenerRouteGroupManagement, http.MethodGet, "/metrics", nil, http.StatusUnauthorized)
	assertStatus(runtimehttp.ListenerRouteGroupOps, http.MethodGet, "/identity/users", nil, http.StatusNotFound)
	assertStatus(runtimehttp.ListenerRouteGroupPublic, http.MethodGet, "/live", map[string]string{
		"Origin": "https://admin.example.com",
	}, http.StatusForbidden)

	for _, group := range []runtimehttp.ListenerRouteGroup{
		runtimehttp.ListenerRouteGroupPublic,
		runtimehttp.ListenerRouteGroupManagement,
		runtimehttp.ListenerRouteGroupOps,
	} {
		if count, all := runtimehttp.ListenerRouteGroupEndpointCount(group), runtimehttp.ListenerRouteGroupEndpointCount(runtimehttp.ListenerRouteGroupAll); count == 0 || count >= all {
			t.Fatalf("group=%s endpoint count=%d", group, count)
		}
	}
}

func TestAgentOptionalTransportOwnersHandleAbsentDependencies(t *testing.T) {
	allowed, err := (agentRecordVisibilityAdapter{}).CanReadAgentRecord(t.Context(), "customer", "record-1", principalmodel.Principal{})
	if allowed || err != nil {
		t.Fatalf("allowed=%v err=%v", allowed, err)
	}
	services := composition.NewRuntimeServices(t.Context(), composition.RuntimeServicesConfig{})
	_, _ = (agentRecordVisibilityAdapter{records: services.Applications().Records}).CanReadAgentRecord(t.Context(), "missing", "record-1", principalmodel.Principal{})
	if workerMetrics := runtimeOptionalWorkerMetrics(nil); workerMetrics != "" {
		t.Fatalf("metrics=%q", workerMetrics)
	}
}

func TestAssembleHTTPServerEntrypointCopiesCallerConfiguration(t *testing.T) {
	services := composition.NewRuntimeServices(context.Background(), composition.RuntimeServicesConfig{})
	origins := []string{"https://app.example.test"}
	server := AssembleHTTPServer(context.Background(), services, transportIdentityBindingStub{}, t.TempDir(), origins, true)
	origins[0] = "mutated"
	if server == nil {
		t.Fatal("entrypoint server is nil")
	}
	preflight := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodOptions, "/live", nil)
	request.Header.Set("Origin", "https://app.example.test")
	request.Header.Set("Access-Control-Request-Method", http.MethodGet)
	server.Routes().ServeHTTP(preflight, request)
	if preflight.Code != http.StatusNoContent || preflight.Header().Get("Access-Control-Allow-Origin") != "https://app.example.test" {
		t.Fatalf("copied CORS configuration status=%d origin=%q", preflight.Code, preflight.Header().Get("Access-Control-Allow-Origin"))
	}
}

func TestAssembleRuntimeHTTPServerPersistentGraphWiresOptionalOwners(t *testing.T) {
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "transport.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	services := runtimetestkit.NewRuntimeServices(t.Context(), runtimetestkit.RuntimeServicesConfig{
		Store: store,
	})
	server := AssembleRuntimeHTTPServer(t.Context(), HTTPServerDependencies{
		Records:         services,
		IdentityBinding: transportIdentityBindingStub{},
		Store:           store,
		Config: config.Config{
			UploadDir:                      "uploads",
			RuntimeAllowDevIdentityHeaders: true,
			IdentityWorkspaceID:            "workspace-primary",
			IdentityAudience:               "domainry-runtime",
		},
		WorkerControl: workerplatform.NewController(),
	})
	if server == nil {
		t.Fatal("persistent server is nil")
	}
	workerMetrics := runtimeOptionalWorkerMetrics(workerplatform.NewController())
	if workerMetrics == "" {
		t.Fatalf("worker metrics missing: %q", workerMetrics)
	}
	response := httptest.NewRecorder()
	server.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/live", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("liveness status=%d body=%s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("X-User-ID", "operator")
	request.Header.Set("X-Role", "operator")
	server.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.Len() == 0 {
		t.Fatalf("metrics status=%d body=%s", response.Code, response.Body.String())
	}

	nilControl := runtimeOperationsControlState(nil)
	if active, found, err := nilControl(t.Context(), "maintenance", "runtime"); active || found || err != nil {
		t.Fatalf("nil control active=%v found=%v err=%v", active, found, err)
	}
	storeControl := runtimeOperationsControlState(store)
	_, _, _ = storeControl(t.Context(), "maintenance", "runtime")
}
