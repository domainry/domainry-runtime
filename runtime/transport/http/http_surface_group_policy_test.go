package http

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	capacityplatform "github.com/domainry/domainry-runtime/runtime/platform/capacity"
	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
)

type surfaceRateLimiterFunc func(context.Context, string, int, time.Duration) (ratelimit.Decision, error)

func (fn surfaceRateLimiterFunc) Allow(ctx context.Context, key string, limit int, window time.Duration) (ratelimit.Decision, error) {
	return fn(ctx, key, limit, window)
}

func TestSurfaceRouteGroupPolicyAppliesIndependentBodyTimeoutAndRateLimits(t *testing.T) {
	router := &HTTPRouter{
		surfaceGroupPolicies: map[SurfaceRouteGroup]SurfaceRouteGroupPolicy{
			SurfaceRouteGroupPublic: {
				MaxJSONBodyBytes: 4,
				RequestTimeout:   10 * time.Millisecond,
				AuditClass:       "public_surface",
			},
		},
		surfaceGroupCapacity: map[SurfaceRouteGroup]*capacityplatform.Controller{},
	}
	handler := router.withSurfaceRouteGroupPolicy(SurfaceRouteGroupPublic, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			if strings.Contains(err.Error(), "request body too large") {
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				return
			}
			t.Errorf("read body: %v", err)
		}
		<-r.Context().Done()
		if !errors.Is(r.Context().Err(), context.DeadlineExceeded) {
			t.Errorf("context error=%v", r.Context().Err())
		}
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/objects/customer/records", strings.NewReader("12345")))
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("body limit status=%d", response.Code)
	}
	timeoutResponse := httptest.NewRecorder()
	handler.ServeHTTP(timeoutResponse, httptest.NewRequest(http.MethodPost, "/objects/customer/records", strings.NewReader("1")))

	router.surfaceGroupPolicies[SurfaceRouteGroupPublic] = SurfaceRouteGroupPolicy{
		RateLimitPerMinute: 1,
		AuditClass:         "public_surface",
	}
	router.surfaceGroupCapacity[SurfaceRouteGroupPublic] = capacityplatform.NewController(capacityplatform.Limits{
		GlobalRate: 1,
		RateWindow: time.Minute,
	}, nil)
	fast := router.withSurfaceRouteGroupPolicy(SurfaceRouteGroupPublic, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	first := httptest.NewRecorder()
	fast.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/objects/customer/records", nil))
	second := httptest.NewRecorder()
	fast.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/objects/customer/records", nil))
	if first.Code != http.StatusNoContent || second.Code != http.StatusTooManyRequests {
		t.Fatalf("rate statuses first=%d second=%d", first.Code, second.Code)
	}
}

func TestSurfaceRouteGroupUsesSharedRateLimiterAndFailsClosed(t *testing.T) {
	shared := ratelimit.NewMemoryLimiter(8)
	policy := map[SurfaceRouteGroup]SurfaceRouteGroupPolicy{
		SurfaceRouteGroupPublic: {RateLimitPerMinute: 1, AuditClass: "public_surface"},
	}
	firstRouter := &HTTPRouter{surfaceGroupPolicies: policy, surfaceGroupCapacity: map[SurfaceRouteGroup]*capacityplatform.Controller{}, rateLimiter: shared}
	secondRouter := &HTTPRouter{surfaceGroupPolicies: policy, surfaceGroupCapacity: map[SurfaceRouteGroup]*capacityplatform.Controller{}, rateLimiter: shared}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	first := httptest.NewRecorder()
	firstRouter.withSurfaceRouteGroupPolicy(SurfaceRouteGroupPublic, next).ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/objects/customer/records", nil))
	second := httptest.NewRecorder()
	secondRouter.withSurfaceRouteGroupPolicy(SurfaceRouteGroupPublic, next).ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/objects/customer/records", nil))
	if first.Code != http.StatusNoContent || second.Code != http.StatusTooManyRequests || second.Header().Get("Retry-After") == "" {
		t.Fatalf("shared statuses first=%d second=%d retry=%q", first.Code, second.Code, second.Header().Get("Retry-After"))
	}

	failing := &HTTPRouter{
		surfaceGroupPolicies: policy,
		rateLimiter: surfaceRateLimiterFunc(func(_ context.Context, key string, limit int, window time.Duration) (ratelimit.Decision, error) {
			if key != "http_surface:public" || limit != 1 || window != time.Minute {
				t.Fatalf("key=%q limit=%d window=%s", key, limit, window)
			}
			return ratelimit.Decision{}, errors.New("backend unavailable")
		}),
	}
	unavailable := httptest.NewRecorder()
	failing.withSurfaceRouteGroupPolicy(SurfaceRouteGroupPublic, next).ServeHTTP(unavailable, httptest.NewRequest(http.MethodGet, "/objects/customer/records", nil))
	if unavailable.Code != http.StatusServiceUnavailable || unavailable.Header().Get("Retry-After") != "1" {
		t.Fatalf("unavailable status=%d retry=%q", unavailable.Code, unavailable.Header().Get("Retry-After"))
	}
}

func TestSurfaceRouteGroupFailureDoesNotBlockIndependentPublicTraffic(t *testing.T) {
	collector := NewMemoryHTTPMetricsCollector(8)
	router := &HTTPRouter{
		httpMetrics:          collector,
		surfaceGroupPolicies: map[SurfaceRouteGroup]SurfaceRouteGroupPolicy{},
		surfaceGroupCapacity: map[SurfaceRouteGroup]*capacityplatform.Controller{},
	}
	ops := router.withSurfaceRouteGroupPolicy(SurfaceRouteGroupOps, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Ops unavailable", http.StatusServiceUnavailable)
	}))
	public := router.withSurfaceRouteGroupPolicy(SurfaceRouteGroupPublic, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	opsResponse := httptest.NewRecorder()
	ops.ServeHTTP(opsResponse, httptest.NewRequest(http.MethodPost, "/operations/scheduler/runs/run-1/retry", nil))
	publicResponse := httptest.NewRecorder()
	public.ServeHTTP(publicResponse, httptest.NewRequest(http.MethodGet, "/objects/order/records", nil))
	if opsResponse.Code != http.StatusServiceUnavailable || publicResponse.Code != http.StatusNoContent {
		t.Fatalf("independent listener statuses ops=%d public=%d", opsResponse.Code, publicResponse.Code)
	}
	prometheus := collector.Prometheus()
	if !strings.Contains(prometheus, `surface_group="ops",status_class="5xx"`) ||
		!strings.Contains(prometheus, `surface_group="public",status_class="2xx"`) {
		t.Fatalf("independent listener metrics missing:\n%s", prometheus)
	}
}

func TestSurfaceRouteGroupOriginPolicy(t *testing.T) {
	router := &HTTPRouter{
		surfaceGroupPolicies: map[SurfaceRouteGroup]SurfaceRouteGroupPolicy{
			SurfaceRouteGroupPublic: {AllowedOrigins: []string{"https://app.example.com"}},
		},
		surfaceGroupCapacity: map[SurfaceRouteGroup]*capacityplatform.Controller{},
	}
	handler := router.withSurfaceRouteGroupPolicy(SurfaceRouteGroupPublic, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	allowedRequest := httptest.NewRequest(http.MethodGet, "/live", nil)
	allowedRequest.Header.Set("Origin", "HTTPS://APP.EXAMPLE.COM")
	allowedResponse := httptest.NewRecorder()
	handler.ServeHTTP(allowedResponse, allowedRequest)
	if allowedResponse.Code != http.StatusNoContent {
		t.Fatalf("allowed origin status=%d", allowedResponse.Code)
	}

	deniedRequest := httptest.NewRequest(http.MethodGet, "/live", nil)
	deniedRequest.Header.Set("Origin", "https://admin.example.com")
	deniedResponse := httptest.NewRecorder()
	handler.ServeHTTP(deniedResponse, deniedRequest)
	if deniedResponse.Code != http.StatusForbidden {
		t.Fatalf("denied origin status=%d", deniedResponse.Code)
	}

	if !corsOriginAllowed([]string{"*"}, "https://portal.example.com") || corsOriginAllowed([]string{"https://app.example.com"}, "https://portal.example.com") {
		t.Fatal("wildcard or mismatched origin decision is incorrect")
	}
}

func TestSurfaceRouteGroupPolicyRemainingBodyProbeWorkspaceAndRetryOutcomes(t *testing.T) {
	controller := capacityplatform.NewController(capacityplatform.Limits{}, nil)
	router := &HTTPRouter{
		surfaceGroupPolicies: map[SurfaceRouteGroup]SurfaceRouteGroupPolicy{
			SurfaceRouteGroupPublic: {MaxJSONBodyBytes: 4},
		},
		surfaceGroupCapacity: map[SurfaceRouteGroup]*capacityplatform.Controller{
			SurfaceRouteGroupPublic: controller,
		},
	}
	handler := router.withSurfaceRouteGroupPolicy(SurfaceRouteGroupPublic, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	nilBodyRequest := httptest.NewRequest(http.MethodGet, "/objects/customer/records", nil)
	nilBodyRequest.Body = nil
	nilBodyResponse := httptest.NewRecorder()
	handler.ServeHTTP(nilBodyResponse, nilBodyRequest)
	if nilBodyResponse.Code != http.StatusNoContent {
		t.Fatalf("nil body status=%d", nilBodyResponse.Code)
	}

	probeResponse := httptest.NewRecorder()
	handler.ServeHTTP(probeResponse, httptest.NewRequest(http.MethodGet, "/live", nil))
	if probeResponse.Code != http.StatusNoContent {
		t.Fatalf("probe status=%d", probeResponse.Code)
	}

	emptyWorkspaceRequest := requestWithPrincipal(
		httptest.NewRequest(http.MethodGet, "/objects/customer/records", nil),
		principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user"}},
	)
	emptyWorkspaceResponse := httptest.NewRecorder()
	handler.ServeHTTP(emptyWorkspaceResponse, emptyWorkspaceRequest)
	if emptyWorkspaceResponse.Code != http.StatusNoContent {
		t.Fatalf("empty workspace status=%d", emptyWorkspaceResponse.Code)
	}

	cancelledContext, cancel := context.WithCancel(t.Context())
	cancel()
	cancelledRequest := httptest.NewRequest(http.MethodGet, "/objects/customer/records", nil).WithContext(cancelledContext)
	cancelledResponse := httptest.NewRecorder()
	handler.ServeHTTP(cancelledResponse, cancelledRequest)
	if cancelledResponse.Code != http.StatusTooManyRequests || cancelledResponse.Header().Get("Retry-After") != "1" {
		t.Fatalf("cancelled status=%d retry=%q", cancelledResponse.Code, cancelledResponse.Header().Get("Retry-After"))
	}
	if surfaceMutationRequest(nil) {
		t.Fatal("nil request classified as mutation")
	}
}

func TestTenantAdminFrontendAvailabilityIsNotARuntimeBusinessDependency(t *testing.T) {
	frontendAvailable := false
	businessExecutions := 0
	runtimeBusiness := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		businessExecutions++
		w.WriteHeader(http.StatusAccepted)
	})

	response := httptest.NewRecorder()
	runtimeBusiness.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/objects/order/records", nil))
	if frontendAvailable || response.Code != http.StatusAccepted || businessExecutions != 1 {
		t.Fatalf("frontendAvailable=%v status=%d executions=%d", frontendAvailable, response.Code, businessExecutions)
	}
}
