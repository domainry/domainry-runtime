package transport

import (
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEntrypointMuxKeepsProvisionAndBusinessTransportsSeparate(t *testing.T) {
	handler := &EntrypointMux{}
	handler.SetProvision(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	provisionResponse := httptest.NewRecorder()
	handler.ServeHTTP(provisionResponse, httptest.NewRequest(http.MethodPost, "/provision/v1", nil))
	if provisionResponse.Code != http.StatusAccepted {
		t.Fatalf("provision status=%d", provisionResponse.Code)
	}

	handler.SetBusiness(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	businessResponse := httptest.NewRecorder()
	handler.ServeHTTP(businessResponse, httptest.NewRequest(http.MethodGet, "/health", nil))
	if businessResponse.Code != http.StatusNoContent {
		t.Fatalf("business status=%d", businessResponse.Code)
	}

	manifestResponse := httptest.NewRecorder()
	handler.ServeHTTP(manifestResponse, httptest.NewRequest(http.MethodPut, "/metadata/manifests/current", nil))
	if manifestResponse.Code != http.StatusAccepted {
		t.Fatalf("manifest provision status=%d", manifestResponse.Code)
	}
}

func TestEntrypointMuxHandlesMissingTransports(t *testing.T) {
	handler := &EntrypointMux{}

	businessResponse := httptest.NewRecorder()
	handler.ServeHTTP(businessResponse, httptest.NewRequest(http.MethodGet, "/health", nil))
	if businessResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing business status=%d", businessResponse.Code)
	}

	provisionResponse := httptest.NewRecorder()
	handler.ServeHTTP(provisionResponse, httptest.NewRequest(http.MethodPost, "/provision/v1", nil))
	if provisionResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing provision status=%d", provisionResponse.Code)
	}

	handler.SetProvision(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) }))
	missingBusinessResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingBusinessResponse, httptest.NewRequest(http.MethodGet, "/health", nil))
	if missingBusinessResponse.Code != http.StatusAccepted {
		t.Fatalf("provision-only business status=%d", missingBusinessResponse.Code)
	}
}

func TestEntrypointMuxProvisionPathWithoutProvisionHandler(t *testing.T) {
	handler := &EntrypointMux{}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metadata/manifests/current", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", response.Code)
	}
	handler.SetConfiguring("task-1")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("configuring health status=%d", response.Code)
	}
}

func TestEntrypointMuxHidesConfiguringRuntimeFromNonBuilderTraffic(t *testing.T) {
	handler := &EntrypointMux{}
	handler.SetProvision(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) }))
	handler.SetBusiness(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if operationscontract.BuilderTaskID(r.Context()) != "task-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	handler.SetConfiguring("task-1")

	blocked := httptest.NewRecorder()
	handler.ServeHTTP(blocked, httptest.NewRequest(http.MethodGet, "/tenant-admin/platform-capabilities/index", nil))
	if blocked.Code != http.StatusLocked {
		t.Fatalf("non-builder status=%d body=%s", blocked.Code, blocked.Body.String())
	}
	blockedReady := httptest.NewRecorder()
	handler.ServeHTTP(blockedReady, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if blockedReady.Code != http.StatusLocked {
		t.Fatalf("configuring Runtime leaked technical readiness to non-builder traffic: status=%d body=%s", blockedReady.Code, blockedReady.Body.String())
	}
	allowedRequest := httptest.NewRequest(http.MethodGet, "/tenant-admin/platform-capabilities/index", nil)
	allowedRequest.Header.Set("Builder-Task-ID", "task-1")
	allowed := httptest.NewRecorder()
	handler.ServeHTTP(allowed, allowedRequest)
	if allowed.Code != http.StatusNoContent {
		t.Fatalf("builder status=%d", allowed.Code)
	}
	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/health", nil))
	if health.Code != http.StatusAccepted {
		t.Fatalf("configuring health must come from lifecycle transport: %d", health.Code)
	}
	handler.SetProvisioning()
	afterAbandon := httptest.NewRecorder()
	handler.ServeHTTP(afterAbandon, allowedRequest)
	if afterAbandon.Code != http.StatusAccepted {
		t.Fatalf("abandoned Runtime business handler remained reachable: %d", afterAbandon.Code)
	}
}
