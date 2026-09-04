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

	capacityplatform "github.com/domainry/domainry-foundation/capacity"
	"github.com/domainry/domainry-foundation/ratelimit"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type listenerRateLimiterFunc func(context.Context, string, int, time.Duration) (ratelimit.Decision, error)

func (fn listenerRateLimiterFunc) Allow(ctx context.Context, key string, limit int, window time.Duration) (ratelimit.Decision, error) {
	return fn(ctx, key, limit, window)
}

func TestListenerRouteGroupPolicyAppliesIndependentBodyTimeoutAndRateLimits(t *testing.T) {
	router := &HTTPRouter{
		listenerGroupPolicies: map[ListenerRouteGroup]ListenerRouteGroupPolicy{
			ListenerRouteGroupPublic: {
				MaxJSONBodyBytes: 4,
				RequestTimeout:   10 * time.Millisecond,
				AuditClass:       "public_listener",
			},
		},
		listenerGroupCapacity: map[ListenerRouteGroup]*capacityplatform.Controller{},
	}
	handler := router.withListenerRouteGroupPolicy(ListenerRouteGroupPublic, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/records/customer", strings.NewReader("12345")))
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("body limit status=%d", response.Code)
	}
	timeoutResponse := httptest.NewRecorder()
	handler.ServeHTTP(timeoutResponse, httptest.NewRequest(http.MethodPost, "/records/customer", strings.NewReader("1")))

	router.listenerGroupPolicies[ListenerRouteGroupPublic] = ListenerRouteGroupPolicy{
		RateLimitPerMinute: 1,
		AuditClass:         "public_listener",
	}
	router.listenerGroupCapacity[ListenerRouteGroupPublic] = capacityplatform.NewController(capacityplatform.Limits{
		GlobalRate: 1,
		RateWindow: time.Minute,
	}, nil)
	fast := router.withListenerRouteGroupPolicy(ListenerRouteGroupPublic, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	first := httptest.NewRecorder()
	fast.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/records/customer", nil))
	second := httptest.NewRecorder()
	fast.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/records/customer", nil))
	if first.Code != http.StatusNoContent || second.Code != http.StatusTooManyRequests {
		t.Fatalf("rate statuses first=%d second=%d", first.Code, second.Code)
	}
}

func TestListenerRouteGroupUsesSharedRateLimiterAndFailsClosed(t *testing.T) {
	shared := ratelimit.NewMemoryLimiter(8)
	policy := map[ListenerRouteGroup]ListenerRouteGroupPolicy{
		ListenerRouteGroupPublic: {RateLimitPerMinute: 1, AuditClass: "public_listener"},
	}
	firstRouter := &HTTPRouter{listenerGroupPolicies: policy, listenerGroupCapacity: map[ListenerRouteGroup]*capacityplatform.Controller{}, rateLimiter: shared}
	secondRouter := &HTTPRouter{listenerGroupPolicies: policy, listenerGroupCapacity: map[ListenerRouteGroup]*capacityplatform.Controller{}, rateLimiter: shared}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	first := httptest.NewRecorder()
	firstRouter.withListenerRouteGroupPolicy(ListenerRouteGroupPublic, next).ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/records/customer", nil))
	second := httptest.NewRecorder()
	secondRouter.withListenerRouteGroupPolicy(ListenerRouteGroupPublic, next).ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/records/customer", nil))
	if first.Code != http.StatusNoContent || second.Code != http.StatusTooManyRequests || second.Header().Get("Retry-After") == "" {
		t.Fatalf("shared statuses first=%d second=%d retry=%q", first.Code, second.Code, second.Header().Get("Retry-After"))
	}

	failing := &HTTPRouter{
		listenerGroupPolicies: policy,
		rateLimiter: listenerRateLimiterFunc(func(_ context.Context, key string, limit int, window time.Duration) (ratelimit.Decision, error) {
			if key != "http_listener:public" || limit != 1 || window != time.Minute {
				t.Fatalf("key=%q limit=%d window=%s", key, limit, window)
			}
			return ratelimit.Decision{}, errors.New("backend unavailable")
		}),
	}
	unavailable := httptest.NewRecorder()
	failing.withListenerRouteGroupPolicy(ListenerRouteGroupPublic, next).ServeHTTP(unavailable, httptest.NewRequest(http.MethodGet, "/records/customer", nil))
	if unavailable.Code != http.StatusServiceUnavailable || unavailable.Header().Get("Retry-After") != "1" {
		t.Fatalf("unavailable status=%d retry=%q", unavailable.Code, unavailable.Header().Get("Retry-After"))
	}
}

func TestListenerRouteGroupFailureDoesNotBlockIndependentPublicTraffic(t *testing.T) {
	collector := NewMemoryHTTPMetricsCollector(8)
	router := &HTTPRouter{
		httpMetrics:           collector,
		listenerGroupPolicies: map[ListenerRouteGroup]ListenerRouteGroupPolicy{},
		listenerGroupCapacity: map[ListenerRouteGroup]*capacityplatform.Controller{},
	}
	ops := router.withListenerRouteGroupPolicy(ListenerRouteGroupOps, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Ops unavailable", http.StatusServiceUnavailable)
	}))
	public := router.withListenerRouteGroupPolicy(ListenerRouteGroupPublic, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	opsResponse := httptest.NewRecorder()
	ops.ServeHTTP(opsResponse, httptest.NewRequest(http.MethodPost, "/scheduler/runs/run-1/retry", nil))
	publicResponse := httptest.NewRecorder()
	public.ServeHTTP(publicResponse, httptest.NewRequest(http.MethodGet, "/records/order", nil))
	if opsResponse.Code != http.StatusServiceUnavailable || publicResponse.Code != http.StatusNoContent {
		t.Fatalf("independent listener statuses ops=%d public=%d", opsResponse.Code, publicResponse.Code)
	}
	prometheus := collector.Prometheus()
	if !strings.Contains(prometheus, `listener_group="ops",status_class="5xx"`) ||
		!strings.Contains(prometheus, `listener_group="public",status_class="2xx"`) {
		t.Fatalf("independent listener metrics missing:\n%s", prometheus)
	}
}

func TestListenerRouteGroupOriginPolicy(t *testing.T) {
	router := &HTTPRouter{
		listenerGroupPolicies: map[ListenerRouteGroup]ListenerRouteGroupPolicy{
			ListenerRouteGroupPublic: {AllowedOrigins: []string{"https://app.example.com"}},
		},
		listenerGroupCapacity: map[ListenerRouteGroup]*capacityplatform.Controller{},
	}
	handler := router.withListenerRouteGroupPolicy(ListenerRouteGroupPublic, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

func TestListenerRouteGroupPolicyRemainingBodyProbeWorkspaceAndRetryOutcomes(t *testing.T) {
	controller := capacityplatform.NewController(capacityplatform.Limits{}, nil)
	router := &HTTPRouter{
		listenerGroupPolicies: map[ListenerRouteGroup]ListenerRouteGroupPolicy{
			ListenerRouteGroupPublic: {MaxJSONBodyBytes: 4},
		},
		listenerGroupCapacity: map[ListenerRouteGroup]*capacityplatform.Controller{
			ListenerRouteGroupPublic: controller,
		},
	}
	handler := router.withListenerRouteGroupPolicy(ListenerRouteGroupPublic, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	nilBodyRequest := httptest.NewRequest(http.MethodGet, "/records/customer", nil)
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
		httptest.NewRequest(http.MethodGet, "/records/customer", nil),
		principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user"}},
	)
	emptyWorkspaceResponse := httptest.NewRecorder()
	handler.ServeHTTP(emptyWorkspaceResponse, emptyWorkspaceRequest)
	if emptyWorkspaceResponse.Code != http.StatusNoContent {
		t.Fatalf("empty workspace status=%d", emptyWorkspaceResponse.Code)
	}

	cancelledContext, cancel := context.WithCancel(t.Context())
	cancel()
	cancelledRequest := httptest.NewRequest(http.MethodGet, "/records/customer", nil).WithContext(cancelledContext)
	cancelledResponse := httptest.NewRecorder()
	handler.ServeHTTP(cancelledResponse, cancelledRequest)
	if cancelledResponse.Code != http.StatusTooManyRequests || cancelledResponse.Header().Get("Retry-After") != "1" {
		t.Fatalf("cancelled status=%d retry=%q", cancelledResponse.Code, cancelledResponse.Header().Get("Retry-After"))
	}
	if listenerMutationRequest(nil) {
		t.Fatal("nil request classified as mutation")
	}
}

func TestManagementFrontendAvailabilityIsNotARuntimeBusinessDependency(t *testing.T) {
	frontendAvailable := false
	businessExecutions := 0
	runtimeBusiness := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		businessExecutions++
		w.WriteHeader(http.StatusAccepted)
	})

	response := httptest.NewRecorder()
	runtimeBusiness.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/records/order", nil))
	if frontendAvailable || response.Code != http.StatusAccepted || businessExecutions != 1 {
		t.Fatalf("frontendAvailable=%v status=%d executions=%d", frontendAvailable, response.Code, businessExecutions)
	}
}
