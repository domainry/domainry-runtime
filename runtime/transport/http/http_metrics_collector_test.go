package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMemoryHTTPMetricsCollectorIsConcurrentAndBoundsSeries(t *testing.T) {
	collector := NewMemoryHTTPMetricsCollector(2)
	var group sync.WaitGroup
	for index := 0; index < 100; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			collector.Begin()
			status := 200
			if index%2 == 0 {
				status = 500
			}
			collector.Observe("GET", "/objects/{objectKey}/records", status, 10*time.Millisecond)
		}(index)
	}
	group.Wait()
	collector.Begin()
	collector.Observe("POST", "/unbounded-label", 201, time.Millisecond)

	summary := collector.Summary()
	if summary["request_count"] != 101 || summary["error_count"] != 50 {
		t.Fatalf("unexpected totals: %+v", summary)
	}
	if summary["series_count"] != 2 || summary["dropped_series_count"] != 1 {
		t.Fatalf("series bound not enforced: %+v", summary)
	}
	if snapshot := collector.Snapshot(); len(snapshot) != 2 || snapshot[0].Route != "/objects/{objectKey}/records" || len(snapshot[0].DurationBuckets) != len(httpDurationBuckets) {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
	if summary["in_flight"] != 0 || summary["series_budget"] != 2 {
		t.Fatalf("unexpected lifecycle summary: %+v", summary)
	}
	prometheus := collector.Prometheus()
	for _, required := range []string{"domainry_runtime_http_requests_total", "domainry_runtime_http_request_duration_seconds_bucket", "domainry_runtime_http_in_flight", "domainry_runtime_telemetry_dropped_series_total"} {
		if !strings.Contains(prometheus, required) {
			t.Fatalf("Prometheus output missing %s: %s", required, prometheus)
		}
	}
}

func TestMetricsEndpointAppendsTechnicalMetricsAndOpenMetricsEOF(t *testing.T) {
	router := &HTTPRouter{httpMetrics: NewMemoryHTTPMetricsCollector(8), technicalMetrics: func(context.Context) string { return "# TYPE runtime_probe gauge\nruntime_probe 1\n" }}
	response := httptest.NewRecorder()
	router.metrics(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "runtime_probe 1") || !strings.HasSuffix(response.Body.String(), "# EOF\n") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMemoryHTTPMetricsCollectorUsesBoundedLabels(t *testing.T) {
	collector := NewMemoryHTTPMetricsCollector(8)
	collector.Begin()
	collector.Observe("invented", "", 999, time.Millisecond)
	metric := collector.Snapshot()[0]
	if metric.Method != "OTHER" || metric.Route != "unmatched" || metric.StatusClass != "unknown" {
		t.Fatalf("unbounded labels were retained: %#v", metric)
	}
}

func TestSurfaceListenerMetricsSeparateGroupsRejectionsAndMutations(t *testing.T) {
	collector := NewMemoryHTTPMetricsCollector(8)
	collector.RegisterListenerGroup(string(ListenerRouteGroupPublic), 120)
	collector.RegisterListenerGroup(string(ListenerRouteGroupOps), 44)
	collector.ObserveListenerGroup(string(ListenerRouteGroupPublic), http.StatusOK, false)
	collector.ObserveListenerGroup(string(ListenerRouteGroupOps), http.StatusForbidden, true)

	prometheus := collector.Prometheus()
	for _, required := range []string{
		`domainry_runtime_listener_info{listener_group="public"} 1`,
		`domainry_runtime_listener_endpoints{listener_group="ops"} 44`,
		`domainry_runtime_listener_requests_total{listener_group="public",status_class="2xx"} 1`,
		`domainry_runtime_listener_rejections_total{listener_group="ops",status_class="4xx"} 1`,
		`domainry_runtime_high_risk_operations_total{listener_group="ops",status_class="4xx"} 1`,
	} {
		if !strings.Contains(prometheus, required) {
			t.Fatalf("surface metrics missing %q:\n%s", required, prometheus)
		}
	}
}
