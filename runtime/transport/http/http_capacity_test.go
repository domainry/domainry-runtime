package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	capacityplatform "github.com/domainry/domainry-foundation/capacity"
)

func initializedCapacityRequest(method, target string) *http.Request {
	request := httptest.NewRequest(method, target, nil)
	request.Header.Set("X-Workspace-ID", "workspace-primary")
	return request
}

func TestHTTPAdmissionReturnsStableOverloadContract(t *testing.T) {
	controller := capacityplatform.NewController(capacityplatform.Limits{GlobalInFlight: 1, WorkspaceInFlight: 1, UseCaseInFlight: 1, RetryAfter: 3 * time.Second}, nil)
	router := &HTTPRouter{capacityController: controller, requestTimeout: time.Second}
	entered, release := make(chan struct{}), make(chan struct{})
	handler := router.withAdmission(nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		handler.ServeHTTP(httptest.NewRecorder(), initializedCapacityRequest(http.MethodPost, "/records"))
	}()
	<-entered
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, initializedCapacityRequest(http.MethodPost, "/records"))
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "3" || response.Header().Get("X-Capacity-Dimension") != "process" {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	close(release)
	<-firstDone
}

func TestHTTPAdmissionPropagatesRequestDeadline(t *testing.T) {
	router := &HTTPRouter{capacityController: capacityplatform.NewController(capacityplatform.Limits{}, nil), requestTimeout: 10 * time.Millisecond}
	handler := router.withAdmission(nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		w.WriteHeader(http.StatusGatewayTimeout)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, initializedCapacityRequest(http.MethodGet, "/reports/sales"))
	if response.Code != http.StatusGatewayTimeout {
		t.Fatalf("deadline did not propagate: %d", response.Code)
	}
}

func TestHTTPAdmissionShedsNonessentialWorkDuringQueueBackpressure(t *testing.T) {
	router := &HTTPRouter{capacityController: capacityplatform.NewController(capacityplatform.Limits{}, nil), requestTimeout: time.Second, backpressure: func(context.Context) bool { return true }}
	handler := router.withAdmission(nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, initializedCapacityRequest(http.MethodGet, "/reports/sales"))
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "5" || response.Header().Get("X-Capacity-Dimension") != "queue" {
		t.Fatalf("queue pressure status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, initializedCapacityRequest(http.MethodPost, "/objects/customer/records"))
	if response.Code != http.StatusNoContent {
		t.Fatalf("essential mutation was shed: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestHTTPAdmissionClassifiesRegisteredRouteNotPathParameter(t *testing.T) {
	router := &HTTPRouter{capacityController: capacityplatform.NewController(capacityplatform.Limits{}, nil), requestTimeout: time.Second, backpressure: func(context.Context) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /objects/{objectKey}/records", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := router.withAdmission(mux, mux)

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, initializedCapacityRequest(http.MethodPost, "/objects/reports/records"))
	if response.Code != http.StatusNoContent {
		t.Fatalf("business object key changed capacity policy: status=%d body=%s", response.Code, response.Body.String())
	}
}
