package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	localization "github.com/domainry/domainry-runtime/runtime/platform/localization"
)

func TestRequestLocalePrecedenceAndFallback(t *testing.T) {
	if got := requestLocale(nil); got != localization.DefaultLocale {
		t.Fatalf("nil locale=%q", got)
	}
	tests := []struct {
		rawURL   string
		headers  map[string]string
		expected string
	}{
		{rawURL: "/?locale=zh-CN", headers: map[string]string{"X-Locale": "en-US"}, expected: "zh-CN"},
		{rawURL: "/", headers: map[string]string{"X-Locale": "zh_cn"}, expected: "zh-CN"},
		{rawURL: "/", headers: map[string]string{"Accept-Language": "zh-CN;q=0.9, en-US;q=0.8"}, expected: "zh-CN"},
		{rawURL: "/?locale=unsupported", headers: map[string]string{"X-Locale": "zh-CN"}, expected: localization.DefaultLocale},
		{rawURL: "/", headers: map[string]string{"Accept-Language": " ;q=0.9"}, expected: localization.DefaultLocale},
	}
	for _, test := range tests {
		request := httptest.NewRequest(http.MethodGet, test.rawURL, nil)
		for key, value := range test.headers {
			request.Header.Set(key, value)
		}
		if got := requestLocale(request); got != test.expected {
			t.Fatalf("url=%s headers=%v locale=%q want %q", test.rawURL, test.headers, got, test.expected)
		}
	}
}

func TestCORSOriginPolicyAndPreflight(t *testing.T) {
	if got := normalizeCORSOrigins([]string{" https://app.example ", "", "  "}); len(got) != 1 || got[0] != "https://app.example" {
		t.Fatalf("origins=%v", got)
	}
	router := &HTTPRouter{corsAllowedOrigins: []string{"https://app.example"}}
	if origin, ok := router.allowedCORSOrigin(" HTTPS://APP.EXAMPLE "); !ok || origin != "HTTPS://APP.EXAMPLE" {
		t.Fatalf("origin=%q ok=%v", origin, ok)
	}
	if _, ok := router.allowedCORSOrigin(""); ok {
		t.Fatal("empty origin accepted")
	}
	if _, ok := router.allowedCORSOrigin("https://evil.example"); ok {
		t.Fatal("unlisted origin accepted")
	}
	wildcard := &HTTPRouter{corsAllowedOrigins: []string{"*"}}
	if origin, ok := wildcard.allowedCORSOrigin("https://any.example"); !ok || origin != "*" {
		t.Fatalf("wildcard origin=%q ok=%v", origin, ok)
	}

	called := false
	handler := router.withCORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusCreated)
	}))
	preflight := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodOptions, "/records", nil)
	request.Header.Set("Origin", "https://app.example")
	handler.ServeHTTP(preflight, request)
	if preflight.Code != http.StatusNoContent || called || preflight.Header().Get("Access-Control-Allow-Origin") != "https://app.example" || preflight.Header().Get("Vary") != "Origin" || !strings.Contains(preflight.Header().Get("Access-Control-Allow-Headers"), "Last-Event-ID") {
		t.Fatalf("preflight status=%d called=%v headers=%v", preflight.Code, called, preflight.Header())
	}
	for _, header := range []string{"If-Match", "Builder-Task-ID", "Idempotency-Key", "Expected-Schema-Hash", "X-Operation-Reason", "X-Operation-Confirmation"} {
		if !strings.Contains(preflight.Header().Get("Access-Control-Allow-Headers"), header) {
			t.Fatalf("authoring header %s is not allowed: %s", header, preflight.Header().Get("Access-Control-Allow-Headers"))
		}
	}
	for _, header := range []string{"X-Resource-Hash", "Operation-ID", "Operation-Location", "Idempotency-Replayed"} {
		if !strings.Contains(preflight.Header().Get("Access-Control-Expose-Headers"), header) {
			t.Fatalf("response header %s is not exposed: %s", header, preflight.Header().Get("Access-Control-Expose-Headers"))
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/records", nil))
	if response.Code != http.StatusCreated || !called || response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("response status=%d called=%v headers=%v", response.Code, called, response.Header())
	}
}

func TestStatusRecorderRecoveryAndMetricsDefaults(t *testing.T) {
	response := httptest.NewRecorder()
	recorder := &statusRecorder{ResponseWriter: response}
	if _, err := recorder.Write([]byte("ok")); err != nil || recorder.status != http.StatusOK || !recorder.wroteHeader {
		t.Fatalf("recorder=%+v err=%v", recorder, err)
	}
	recorder.WriteHeader(http.StatusCreated)
	if response.Code != http.StatusOK || recorder.Unwrap() != response {
		t.Fatalf("duplicate header changed response: %d", response.Code)
	}

	router := &HTTPRouter{}
	panicResponse := httptest.NewRecorder()
	router.withRecovery(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })).ServeHTTP(panicResponse, httptest.NewRequest(http.MethodGet, "/", nil))
	if panicResponse.Code != http.StatusInternalServerError || !strings.Contains(panicResponse.Body.String(), "backend.internal") {
		t.Fatalf("panic status=%d body=%s", panicResponse.Code, panicResponse.Body.String())
	}
	writtenResponse := httptest.NewRecorder()
	writtenRecorder := &statusRecorder{ResponseWriter: writtenResponse}
	router.withRecovery(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted); panic("after write") })).ServeHTTP(writtenRecorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if writtenResponse.Code != http.StatusAccepted {
		t.Fatalf("written panic status=%d", writtenResponse.Code)
	}
	if snapshot := router.httpMetricsSnapshot(); len(snapshot) != 0 {
		t.Fatalf("nil metrics snapshot=%v", snapshot)
	}
	if summary := router.httpMetricsSummary(); summary["request_count"] != 0 || summary["series_count"] != 0 {
		t.Fatalf("nil metrics summary=%v", summary)
	}
	collector := NewMemoryHTTPMetricsCollector(4)
	UseHTTPMetricsCollector(router, collector)
	router.observeHTTPRequest(httptest.NewRequest(http.MethodGet, "/", nil), "/", http.StatusOK, time.Millisecond)
	if len(router.httpMetricsSnapshot()) != 1 || router.httpMetricsSummary()["request_count"] != 1 {
		t.Fatal("installed metrics collector did not receive observation")
	}
	UseHTTPMetricsCollector(router, nil)
	if router.httpMetrics != collector {
		t.Fatal("nil collector replaced the active collector")
	}
}

func TestFallbackAndRootEndpointResponses(t *testing.T) {
	router := &HTTPRouter{serviceKind: BusinessRuntimeServiceKind, productBrandName: "Acme", runtimeVersion: "test", apiContractVersion: "v1", apiContractHash: "hash", manifestTemplateID: "template", manifestHash: "manifest"}
	tests := []struct {
		name       string
		call       func(http.ResponseWriter, *http.Request)
		path       string
		wantStatus int
		contains   string
	}{
		{name: "provision entrypoint", call: router.manifestProvisionEntrypointRequired, path: "/provision", wantStatus: http.StatusServiceUnavailable, contains: "runtime_provision_entrypoint_required"},
		{name: "not found", call: router.notFound, path: "/missing", wantStatus: http.StatusNotFound, contains: "route_not_found"},
		{name: "api info", call: router.apiInfo, path: "/", wantStatus: http.StatusOK, contains: "api_contract_version"},
		{name: "api info rejects nested", call: router.apiInfo, path: "/nested", wantStatus: http.StatusNotFound, contains: "route_not_found"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			test.call(response, httptest.NewRequest(http.MethodGet, test.path, nil))
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), test.contains) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
	brandResponse := httptest.NewRecorder()
	router.apiInfo(brandResponse, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(brandResponse.Body.String(), `"service":"Acme Business Runtime"`) || strings.Contains(brandResponse.Body.String(), "Domainry") {
		t.Fatalf("product brand service identity=%s", brandResponse.Body.String())
	}
	mux := http.NewServeMux()
	router.registerFallbackRoutes(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/unknown", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("fallback status=%d", response.Code)
	}

	router.healthRegistry = newRuntimeHealthRegistry()
	router.SetMaintenance(true)
	if !router.healthRegistry.Maintenance() {
		t.Fatal("maintenance state was not set")
	}
	(*HTTPRouter)(nil).SetMaintenance(true)
}
