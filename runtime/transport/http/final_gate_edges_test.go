package http

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	surfacemodel "github.com/domainry/domainry-runtime/runtime/domain/surface/model"
	capacityplatform "github.com/domainry/domainry-runtime/runtime/platform/capacity"
	partyhttp "github.com/domainry/domainry-runtime/runtime/transport/http/party"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFinalHighRiskAndSurfacePolicyEdges(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /unclassified", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := (&HTTPRouter{}).withHighRiskOperationPolicy(mux, mux)
	for _, method := range []string{http.MethodOptions, http.MethodPost} {
		request := httptest.NewRequest(method, "/unclassified", nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if (method == http.MethodOptions && response.Code != http.StatusMethodNotAllowed) || (method == http.MethodPost && response.Code != http.StatusNoContent) {
			t.Fatalf("method=%s status=%d", method, response.Code)
		}
	}
	if _, err := decodeOperationReasonHeader("UTF-8''%FF"); err == nil {
		t.Fatal("invalid UTF-8 reason was accepted")
	}
	if errInvalidOperationReasonEncoding.Error() == "" {
		t.Fatal("reason encoding error has no message")
	}
	if normalized := normalizeSurfaceGroup("invalid"); normalized != "unknown" {
		t.Fatalf("normalized group=%q", normalized)
	}

	router := &HTTPRouter{surfaceGroupPolicies: map[SurfaceRouteGroup]SurfaceRouteGroupPolicy{SurfaceRouteGroupPublic: {}}, surfaceGroupCapacity: map[SurfaceRouteGroup]*capacityplatform.Controller{}}
	policyHandler := router.withSurfaceRouteGroupPolicy(SurfaceRouteGroupPublic, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	request := httptest.NewRequest(http.MethodGet, "/live", nil)
	request.Header.Set("Origin", "https://unrestricted.example")
	response := httptest.NewRecorder()
	policyHandler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("unrestricted origin status=%d", response.Code)
	}
}

func TestFinalAuthenticationAndBuilderPrincipalEdges(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /records", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	router := &HTTPRouter{integrationAuth: routerIntegrationAuthStub{}}
	request := httptest.NewRequest(http.MethodGet, "/records", nil)
	request.Header.Set("X-API-Key", "inactive")
	response := httptest.NewRecorder()
	router.withAuth(mux, mux).ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("inactive API principal status=%d", response.Code)
	}
	bearerRouter := routerWithIdentitySDK(identitysdk.Principal{Known: false})
	bearerRequest := httptest.NewRequest(http.MethodGet, "/records", nil)
	bearerRequest.Header.Set("Authorization", "Bearer inactive")
	bearerResponse := httptest.NewRecorder()
	bearerRouter.withAuth(mux, mux).ServeHTTP(bearerResponse, bearerRequest)
	if bearerResponse.Code != http.StatusUnauthorized {
		t.Fatalf("inactive bearer principal status=%d", bearerResponse.Code)
	}
	builderAuthRequest := httptest.NewRequest(http.MethodGet, "/records", nil)
	builderAuthRequest = builderAuthRequest.WithContext(operationscontract.WithBuilderTaskID(builderAuthRequest.Context(), "task-1"))
	builderAuthResponse := httptest.NewRecorder()
	(&HTTPRouter{}).withAuth(mux, mux).ServeHTTP(builderAuthResponse, builderAuthRequest)
	if builderAuthResponse.Code != http.StatusNoContent {
		t.Fatalf("builder auth status=%d", builderAuthResponse.Code)
	}

	builderRequest := httptest.NewRequest(http.MethodGet, "/records", nil)
	builderRequest = builderRequest.WithContext(operationscontract.WithBuilderTaskID(builderRequest.Context(), "task-1"))
	builderRequest.Header.Set("X-User-ID", "developer")
	devRouter := &HTTPRouter{allowDevAuthHeaders: true, identityAuthorization: routerIdentityAuthorizationStub{principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "developer"}}}}
	if principal := devRouter.principalFromRequest(builderRequest); principal.UserID == "runtime-builder:task-1" {
		t.Fatal("explicit user header was replaced by builder principal")
	}
	userRoleRequest := httptest.NewRequest(http.MethodGet, "/records", nil)
	userRoleRequest = userRoleRequest.WithContext(operationscontract.WithBuilderTaskID(userRoleRequest.Context(), "task-2"))
	userRoleRequest.Header.Set("X-User-Role", "admin")
	_ = (&HTTPRouter{}).principalFromRequest(userRoleRequest)

	roleRequest := httptest.NewRequest(http.MethodGet, "/records", nil)
	roleRequest.Header.Set("X-User-ID", "developer")
	roleRequest.Header.Set("X-Role", "admin")
	knownRoleRouter := &HTTPRouter{allowDevAuthHeaders: true, identityAuthorization: routerIdentityAuthorizationStub{principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "developer"}}}}
	if principal := knownRoleRouter.principalFromRequest(roleRequest); !principal.Known {
		t.Fatal("known role principal was discarded")
	}
	failedRoleRouter := &HTTPRouter{allowDevAuthHeaders: true, identityAuthorization: routerIdentityAuthorizationStub{error: errInvalidOperationReasonEncoding}}
	if principal := failedRoleRouter.principalFromRequest(roleRequest); principal.Known || principal.UserID != "developer" {
		t.Fatalf("failed Identity role resolution did not fail closed: %#v", principal)
	}
}

func TestFinalAdmissionMetricsAndHandlerWiringEdges(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /records", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	router := &HTTPRouter{runtimeReleaseAdmission: func() error { return nil }}
	response := httptest.NewRecorder()
	router.withAdmission(mux, mux).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/records", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("admission status=%d", response.Code)
	}
	metrics := NewMemoryHTTPMetricsCollector(1)
	metrics.RegisterSurfaceGroup(string(SurfaceRouteGroupPublic), 1)
	metrics.RegisterSurfaceGroup(string(SurfaceRouteGroupPublic), 2)
	UseHandlers(&HTTPRouter{}, HTTPRouterHandlers{Party: &partyhttp.PartyHandler{}})
}

func TestFinalCompiledEndpointContractFailures(t *testing.T) {
	originalContracts, originalPolicies := runtimeEndpointSurfaceContracts, runtimeSurfaceRoutePolicies
	t.Cleanup(func() {
		runtimeEndpointSurfaceContracts, runtimeSurfaceRoutePolicies = originalContracts, originalPolicies
	})

	var route string
	var base surfacemodel.RuntimeEndpointContractV1
	for candidate, contract := range originalContracts {
		if len(contract.Projections) > 0 {
			route, base = candidate, contract
			break
		}
	}
	if route == "" {
		t.Fatal("no projected endpoint contract")
	}
	cloneContracts := func() map[string]surfacemodel.RuntimeEndpointContractV1 {
		values := make(map[string]surfacemodel.RuntimeEndpointContractV1, len(originalContracts))
		for key, value := range originalContracts {
			values[key] = value
		}
		return values
	}
	clonePolicies := func() map[string][]surfacemodel.ProductSurface {
		values := make(map[string][]surfacemodel.ProductSurface, len(originalPolicies))
		for key, value := range originalPolicies {
			values[key] = append([]surfacemodel.ProductSurface(nil), value...)
		}
		return values
	}
	assertFailure := func(t *testing.T) {
		t.Helper()
		if err := validateCompiledEndpointSurfaceContracts(); err == nil {
			t.Fatal("invalid compiled contract was accepted")
		}
	}

	runtimeEndpointSurfaceContracts, runtimeSurfaceRoutePolicies = cloneContracts(), clonePolicies()
	changed := base
	changed.EndpointIdentity = "GET /different"
	runtimeEndpointSurfaceContracts[route] = changed
	assertFailure(t)

	runtimeEndpointSurfaceContracts, runtimeSurfaceRoutePolicies = cloneContracts(), clonePolicies()
	changed = base
	changed.ContractVersion = "invalid"
	runtimeEndpointSurfaceContracts[route] = changed
	assertFailure(t)

	runtimeEndpointSurfaceContracts, runtimeSurfaceRoutePolicies = cloneContracts(), clonePolicies()
	delete(runtimeSurfaceRoutePolicies, route)
	runtimeSurfaceRoutePolicies["GET /extra-policy"] = []surfacemodel.ProductSurface{base.Projections[0].Surface}
	assertFailure(t)

	runtimeEndpointSurfaceContracts, runtimeSurfaceRoutePolicies = cloneContracts(), clonePolicies()
	runtimeSurfaceRoutePolicies[route] = append(runtimeSurfaceRoutePolicies[route], base.Projections[0].Surface)
	assertFailure(t)

	runtimeEndpointSurfaceContracts, runtimeSurfaceRoutePolicies = cloneContracts(), clonePolicies()
	runtimeSurfaceRoutePolicies[route][0] = surfacemodel.ProductSurface("invalid")
	assertFailure(t)
}
