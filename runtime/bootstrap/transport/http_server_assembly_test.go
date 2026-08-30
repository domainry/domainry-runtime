package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agent"
	agentruntime "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	agentpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/agent"
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
			IdentityWorkspaceID:            "default",
			IdentityAudience:               "domainry-runtime",
		},
	})
	if server == nil {
		t.Fatal("server is nil")
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	request.Header.Set("X-User-ID", "admin")
	request.Header.Set("X-Role", "admin")
	request.Header.Set("X-Domainry-Product-Surface", "admin_console")
	server.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("openapi status=%d body=%s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
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
	request = httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("X-User-ID", "operator")
	request.Header.Set("X-Role", "operator")
	request.Header.Set("X-Domainry-Product-Surface", "admin_console")
	server.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("metrics status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestSurfaceRouteGroupsRegisterOnlyTheirCompiledEndpointInventory(t *testing.T) {
	services := composition.NewRuntimeServices(t.Context(), composition.RuntimeServicesConfig{})
	server := AssembleRuntimeHTTPServer(t.Context(), HTTPServerDependencies{
		Records:         services,
		IdentityBinding: transportIdentityBindingStub{},
		Config: config.Config{
			RuntimeAllowDevIdentityHeaders: true,
			IdentityWorkspaceID:            "default",
			IdentityAudience:               "domainry-runtime",
			SurfaceBusinessOrigins:         []string{"https://app.example.com"},
			SurfacePortalOrigins:           []string{"https://portal.example.com"},
			SurfaceAdminOrigins:            []string{"https://admin.example.com"},
		},
	})
	assertStatus := func(group runtimehttp.SurfaceRouteGroup, method, path string, headers map[string]string, want int) {
		t.Helper()
		request := httptest.NewRequest(method, path, nil)
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response := httptest.NewRecorder()
		server.RoutesForSurfaceGroup(group).ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("group=%s %s %s status=%d body=%s want=%d", group, method, path, response.Code, response.Body.String(), want)
		}
	}

	assertStatus(runtimehttp.SurfaceRouteGroupPublic, http.MethodGet, "/live", nil, http.StatusOK)
	assertStatus(runtimehttp.SurfaceRouteGroupPublic, http.MethodGet, "/openapi.json", nil, http.StatusOK)
	publicOpenAPI := httptest.NewRecorder()
	server.RoutesForSurfaceGroup(runtimehttp.SurfaceRouteGroupPublic).ServeHTTP(
		publicOpenAPI,
		httptest.NewRequest(http.MethodGet, "/openapi.json", nil),
	)
	if body := publicOpenAPI.Body.String(); !strings.Contains(body, `"/objects/{objectKey}/records"`) ||
		!strings.Contains(body, `"/automation-rules"`) ||
		strings.Contains(body, `"/identity/`) ||
		strings.Contains(body, `"/auth/`) ||
		strings.Contains(body, `"/operations"`) {
		t.Fatalf("public OpenAPI leaked or omitted Surface paths: %s", body)
	}
	assertStatus(runtimehttp.SurfaceRouteGroupPublic, http.MethodGet, "/metrics", nil, http.StatusNotFound)
	assertStatus(runtimehttp.SurfaceRouteGroupTenantAdmin, http.MethodGet, "/metrics", nil, http.StatusUnauthorized)
	assertStatus(runtimehttp.SurfaceRouteGroupOps, http.MethodGet, "/identity/users", nil, http.StatusNotFound)
	assertStatus(runtimehttp.SurfaceRouteGroupTenantAdmin, http.MethodGet, "/openapi.json", map[string]string{
		"X-User-ID": "admin", "X-Role": "admin", "X-Domainry-Product-Surface": "admin_console",
	}, http.StatusOK)
	assertStatus(runtimehttp.SurfaceRouteGroupPublic, http.MethodGet, "/live", map[string]string{
		"Origin": "https://admin.example.com",
	}, http.StatusForbidden)

	for _, group := range []runtimehttp.SurfaceRouteGroup{
		runtimehttp.SurfaceRouteGroupPublic,
		runtimehttp.SurfaceRouteGroupTenantAdmin,
		runtimehttp.SurfaceRouteGroupOps,
	} {
		if count, all := runtimehttp.SurfaceRouteGroupEndpointCount(group), runtimehttp.SurfaceRouteGroupEndpointCount(runtimehttp.SurfaceRouteGroupAll); count == 0 || count >= all {
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
	if service := newAgentInteractiveExecution(nil, nil); service != nil {
		t.Fatalf("interactive=%#v", service)
	}
	called := false
	if service := newAgentInteractiveExecution(func(_ *agentruntime.AgentToolGateway) *agentruntime.AgentInteractiveExecutionApplicationService {
		called = true
		return nil
	}, nil); service != nil || !called {
		t.Fatalf("interactive=%#v called=%v", service, called)
	}
	workerMetrics, taskMetrics, interactiveMetrics := runtimeOptionalWorkerMetrics(t.Context(), nil, nil, nil)
	if workerMetrics != "" || taskMetrics != "" || interactiveMetrics != "" {
		t.Fatalf("metrics=%q %q %q", workerMetrics, taskMetrics, interactiveMetrics)
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
	if err := agentpersistence.NewAgentSchemaMigration(store).EnsureSchema(t.Context()); err != nil {
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
			IdentityWorkspaceID:            "default",
			IdentityAudience:               "domainry-runtime",
		},
		WorkerControl: workerplatform.NewController(),
	})
	if server == nil {
		t.Fatal("persistent server is nil")
	}
	workerMetrics, taskMetrics, interactiveMetrics := runtimeOptionalWorkerMetrics(t.Context(), workerplatform.NewController(), &agentruntime.AgentTaskWorker{}, &agentruntime.AgentInteractiveRunApplicationService{})
	if workerMetrics == "" {
		t.Fatalf("worker metrics missing: %q", workerMetrics)
	}
	_ = taskMetrics
	_ = interactiveMetrics
	response := httptest.NewRecorder()
	server.Routes().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/live", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("liveness status=%d body=%s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	request.Header.Set("X-User-ID", "operator")
	request.Header.Set("X-Role", "operator")
	request.Header.Set("X-Domainry-Product-Surface", "admin_console")
	server.Routes().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.Len() == 0 {
		t.Fatalf("metrics status=%d body=%s", response.Code, response.Body.String())
	}

	dependencies := HTTPServerDependencies{Records: services, Store: store}
	state, proposals := assembleAgentApplicationPorts(dependencies)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "operator"}}, accessfixture.Bundle{Key: "admin", Permissions: []string{"workspace.admin"}})
	normalized := proposals.NormalizeGuardedWrite(t.Context(), map[string]any{"tool_binding": map[string]any{"tool_name": "updateRecord", "object_key": "customer", "record_id": "customer-1", "data": map[string]any{"name": "updated"}}}, principal)
	if normalized["tool_binding"] == nil {
		t.Fatalf("proposal unexpectedly lost tool binding: %v", normalized)
	}
	for _, proposal := range []agentapplication.AgentProposal{
		{ProposalID: "action", WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Role: principal.RoleKey, Proposed: map[string]any{"action_binding": map[string]any{"object_key": "customer", "record_id": "customer-1", "action_key": "update_customer"}}},
		{ProposalID: "workflow", WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Role: principal.RoleKey, Proposed: map[string]any{"workflow_binding": map[string]any{"workflow_key": "missing", "payload": map[string]any{}}}},
	} {
		if _, err := state.StoreProposal(t.Context(), proposal); err != nil {
			t.Fatal(err)
		}
		if _, err := proposals.Decide(t.Context(), proposal.ProposalID, "approved", "test", nil, principal); apperror.CodeOf(err) != "agent.authorization.resolver_unavailable" {
			t.Fatalf("approval without identity owner err=%v", err)
		}
	}

	nilControl := runtimeOperationsControlState(nil)
	if active, found, err := nilControl(t.Context(), "maintenance", "runtime"); active || found || err != nil {
		t.Fatalf("nil control active=%v found=%v err=%v", active, found, err)
	}
	storeControl := runtimeOperationsControlState(store)
	_, _, _ = storeControl(t.Context(), "maintenance", "runtime")
}
