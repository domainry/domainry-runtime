package http

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const defaultHTTPMetricsMaxSeries = 2048

var httpDurationBuckets = [...]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type HTTPMetricsCollector interface {
	Begin()
	Observe(method, route string, status int, duration time.Duration)
	ObserveBodyRejection(reason string)
	RegisterSurfaceGroup(group string, endpointCount int)
	ObserveSurfaceGroup(group string, status int, highRisk bool)
	Snapshot() []HTTPRequestMetric
	Summary() map[string]int64
	Prometheus() string
}

type MemoryHTTPMetricsCollector struct {
	mu             sync.RWMutex
	series         map[string]HTTPRequestMetric
	maxSeries      int
	inFlight       atomic.Int64
	requestCount   int64
	errorCount     int64
	bodyRejections map[string]int64
	surfaceGroups  map[string]surfaceGroupMetric
	droppedSeries  int64
}

type surfaceGroupMetric struct {
	EndpointCount int
	Requests      map[string]int64
	Rejections    map[string]int64
	HighRisk      map[string]int64
}

func NewMemoryHTTPMetricsCollector(maxSeries int) *MemoryHTTPMetricsCollector {
	if maxSeries <= 0 {
		maxSeries = defaultHTTPMetricsMaxSeries
	}
	return &MemoryHTTPMetricsCollector{series: map[string]HTTPRequestMetric{}, maxSeries: maxSeries, bodyRejections: map[string]int64{}, surfaceGroups: map[string]surfaceGroupMetric{}}
}

func (c *MemoryHTTPMetricsCollector) Begin() { c.inFlight.Add(1) }

func (c *MemoryHTTPMetricsCollector) Observe(method, route string, status int, duration time.Duration) {
	c.inFlight.Add(-1)
	method, route, statusClass := normalizeMetricMethod(method), normalizeMetricRoute(route), statusClass(status)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requestCount++
	if status >= 500 {
		c.errorCount++
	}
	key := method + " " + route + " " + statusClass
	metric, exists := c.series[key]
	if !exists && len(c.series) >= c.maxSeries {
		c.droppedSeries++
		return
	}
	if !exists {
		metric = HTTPRequestMetric{Method: method, Route: route, StatusClass: statusClass, DurationBuckets: make([]uint64, len(httpDurationBuckets))}
	}
	metric.Count++
	if status >= 500 {
		metric.ErrorCount++
	}
	seconds := duration.Seconds()
	metric.DurationSecondsSum += seconds
	for index, upperBound := range httpDurationBuckets {
		if seconds <= upperBound {
			metric.DurationBuckets[index]++
		}
	}
	c.series[key] = metric
}

func (c *MemoryHTTPMetricsCollector) ObserveBodyRejection(reason string) {
	reason = normalizeMetricLabel(reason, "unknown")
	c.mu.Lock()
	c.bodyRejections[reason]++
	c.mu.Unlock()
}

func (c *MemoryHTTPMetricsCollector) RegisterSurfaceGroup(group string, endpointCount int) {
	group = normalizeSurfaceGroup(group)
	c.mu.Lock()
	metric := c.surfaceGroups[group]
	metric.EndpointCount = endpointCount
	if metric.Requests == nil {
		metric.Requests, metric.Rejections, metric.HighRisk = map[string]int64{}, map[string]int64{}, map[string]int64{}
	}
	c.surfaceGroups[group] = metric
	c.mu.Unlock()
}

func (c *MemoryHTTPMetricsCollector) ObserveSurfaceGroup(group string, status int, highRisk bool) {
	group, class := normalizeSurfaceGroup(group), statusClass(status)
	c.mu.Lock()
	metric := c.surfaceGroups[group]
	if metric.Requests == nil {
		metric.Requests, metric.Rejections, metric.HighRisk = map[string]int64{}, map[string]int64{}, map[string]int64{}
	}
	metric.Requests[class]++
	if status >= http.StatusBadRequest {
		metric.Rejections[class]++
	}
	if highRisk {
		metric.HighRisk[class]++
	}
	c.surfaceGroups[group] = metric
	c.mu.Unlock()
}

func (c *MemoryHTTPMetricsCollector) Snapshot() []HTTPRequestMetric {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]HTTPRequestMetric, 0, len(c.series))
	for _, metric := range c.series {
		metric.DurationBuckets = append([]uint64(nil), metric.DurationBuckets...)
		out = append(out, metric)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Method != out[j].Method {
			return out[i].Method < out[j].Method
		}
		if out[i].Route != out[j].Route {
			return out[i].Route < out[j].Route
		}
		return out[i].StatusClass < out[j].StatusClass
	})
	return out
}

func (c *MemoryHTTPMetricsCollector) Summary() map[string]int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return map[string]int64{
		"request_count":        c.requestCount,
		"error_count":          c.errorCount,
		"in_flight":            c.inFlight.Load(),
		"series_count":         int64(len(c.series)),
		"series_budget":        int64(c.maxSeries),
		"dropped_series_count": c.droppedSeries,
	}
}

func (c *MemoryHTTPMetricsCollector) Prometheus() string {
	snapshot, summary := c.Snapshot(), c.Summary()
	c.mu.RLock()
	bodyRejections := make(map[string]int64, len(c.bodyRejections))
	for reason, count := range c.bodyRejections {
		bodyRejections[reason] = count
	}
	surfaceGroups := make(map[string]surfaceGroupMetric, len(c.surfaceGroups))
	for group, metric := range c.surfaceGroups {
		surfaceGroups[group] = metric
	}
	c.mu.RUnlock()
	var output strings.Builder
	writeMetricHeader(&output, "domainry_runtime_http_requests_total", "HTTP requests by bounded route and status class.", "counter")
	for _, metric := range snapshot {
		labels := metricLabels(metric)
		fmt.Fprintf(&output, "domainry_runtime_http_requests_total{%s} %d\n", labels, metric.Count)
	}
	writeMetricHeader(&output, "domainry_runtime_http_request_duration_seconds", "HTTP request duration histogram.", "histogram")
	for _, metric := range snapshot {
		labels := metricLabels(metric)
		for index, bound := range httpDurationBuckets {
			fmt.Fprintf(&output, "domainry_runtime_http_request_duration_seconds_bucket{%s,le=%q} %d\n", labels, strconv.FormatFloat(bound, 'f', -1, 64), metric.DurationBuckets[index])
		}
		fmt.Fprintf(&output, "domainry_runtime_http_request_duration_seconds_bucket{%s,le=\"+Inf\"} %d\n", labels, metric.Count)
		fmt.Fprintf(&output, "domainry_runtime_http_request_duration_seconds_sum{%s} %s\n", labels, strconv.FormatFloat(metric.DurationSecondsSum, 'f', 9, 64))
		fmt.Fprintf(&output, "domainry_runtime_http_request_duration_seconds_count{%s} %d\n", labels, metric.Count)
	}
	writeMetricHeader(&output, "domainry_runtime_http_in_flight", "Current in-flight HTTP requests.", "gauge")
	fmt.Fprintf(&output, "domainry_runtime_http_in_flight %d\n", summary["in_flight"])
	writeMetricHeader(&output, "domainry_runtime_http_body_rejections_total", "Rejected HTTP request bodies by stable reason.", "counter")
	reasons := make([]string, 0, len(bodyRejections))
	for reason := range bodyRejections {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	for _, reason := range reasons {
		fmt.Fprintf(&output, "domainry_runtime_http_body_rejections_total{reason=%q} %d\n", reason, bodyRejections[reason])
	}
	writeSurfaceGroupMetrics(&output, surfaceGroups)
	writeMetricHeader(&output, "domainry_runtime_telemetry_dropped_series_total", "Metric series dropped after the cardinality budget was exhausted.", "counter")
	fmt.Fprintf(&output, "domainry_runtime_telemetry_dropped_series_total{signal=\"http\"} %d\n", summary["dropped_series_count"])
	writeMetricHeader(&output, "domainry_runtime_telemetry_series", "Current metric series and configured budget.", "gauge")
	fmt.Fprintf(&output, "domainry_runtime_telemetry_series{signal=\"http\",kind=\"current\"} %d\n", summary["series_count"])
	fmt.Fprintf(&output, "domainry_runtime_telemetry_series{signal=\"http\",kind=\"budget\"} %d\n", summary["series_budget"])
	return output.String()
}

func writeSurfaceGroupMetrics(output *strings.Builder, groups map[string]surfaceGroupMetric) {
	names := make([]string, 0, len(groups))
	for group := range groups {
		names = append(names, group)
	}
	sort.Strings(names)
	writeMetricHeader(output, "domainry_runtime_surface_listener_info", "Configured Runtime Surface listener groups.", "gauge")
	writeMetricHeader(output, "domainry_runtime_surface_listener_endpoints", "Compiled endpoints attached to each Surface listener.", "gauge")
	writeMetricHeader(output, "domainry_runtime_surface_requests_total", "Requests handled by Surface listener and status class.", "counter")
	writeMetricHeader(output, "domainry_runtime_surface_rejections_total", "Rejected requests by Surface listener and status class.", "counter")
	writeMetricHeader(output, "domainry_runtime_high_risk_operations_total", "High-risk mutation requests by Surface listener and status class.", "counter")
	for _, group := range names {
		metric := groups[group]
		fmt.Fprintf(output, "domainry_runtime_surface_listener_info{surface_group=%q} 1\n", group)
		fmt.Fprintf(output, "domainry_runtime_surface_listener_endpoints{surface_group=%q} %d\n", group, metric.EndpointCount)
		for _, class := range sortedMetricKeys(metric.Requests) {
			fmt.Fprintf(output, "domainry_runtime_surface_requests_total{surface_group=%q,status_class=%q} %d\n", group, class, metric.Requests[class])
		}
		for _, class := range sortedMetricKeys(metric.Rejections) {
			fmt.Fprintf(output, "domainry_runtime_surface_rejections_total{surface_group=%q,status_class=%q} %d\n", group, class, metric.Rejections[class])
		}
		for _, class := range sortedMetricKeys(metric.HighRisk) {
			fmt.Fprintf(output, "domainry_runtime_high_risk_operations_total{surface_group=%q,status_class=%q} %d\n", group, class, metric.HighRisk[class])
		}
	}
}

func sortedMetricKeys(values map[string]int64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func normalizeSurfaceGroup(group string) string {
	group = strings.TrimSpace(group)
	switch group {
	case string(SurfaceRouteGroupPublic), string(SurfaceRouteGroupTenantAdmin), string(SurfaceRouteGroupOps), string(SurfaceRouteGroupAll):
		return group
	default:
		return "unknown"
	}
}

func writeMetricHeader(output *strings.Builder, name, help, kind string) {
	fmt.Fprintf(output, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, kind)
}

func metricLabels(metric HTTPRequestMetric) string {
	return fmt.Sprintf("method=%q,route=%q,status_class=%q", metric.Method, metric.Route, metric.StatusClass)
}

func normalizeMetricMethod(method string) string {
	method = strings.ToUpper(strings.TrimSpace(method))
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD":
		return method
	default:
		return "OTHER"
	}
}

func normalizeMetricRoute(route string) string {
	route = strings.TrimSpace(route)
	if route == "" || !strings.HasPrefix(route, "/") {
		return "unmatched"
	}
	if len(route) > 256 {
		return "unmatched"
	}
	return route
}

func normalizeMetricLabel(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return fallback
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || character == '_' {
			continue
		}
		return fallback
	}
	return value
}

func statusClass(status int) string {
	if status < 100 || status > 599 {
		return "unknown"
	}
	return strconv.Itoa(status/100) + "xx"
}
