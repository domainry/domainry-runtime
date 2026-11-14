package redisdiagnostics

import (
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
)

func TestOpenMetricsExposesBoundedApplicationRedisEvidence(t *testing.T) {
	runtimeCollector.mu.Lock()
	runtimeCollector.operations = map[operationKey]*operationMetric{}
	runtimeCollector.fallbacks = map[string]uint64{}
	runtimeCollector.decisions = map[string]uint64{}
	runtimeCollector.clients = map[string]redis.UniversalClient{}
	runtimeCollector.mu.Unlock()

	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	Install(client, ComponentPrincipalCache)
	if err := client.Set(t.Context(), "secret-user-key", "secret-value", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Get(t.Context(), "secret-user-key").Result(); err != nil {
		t.Fatal(err)
	}
	if err := client.Get(t.Context(), "missing-user-key").Err(); err != redis.Nil {
		t.Fatalf("missing result=%v", err)
	}
	ObserveFallback(ComponentPrincipalCache)
	ObserveRateLimitDecision(true)
	ObserveRateLimitDecision(false)

	output := OpenMetrics()
	for _, expected := range []string{
		`domainry_runtime_redis_operations_total{component="principal_cache",operation="set",outcome="success"} 1`,
		`component="principal_cache",operation="get",outcome="success"`,
		`component="principal_cache",operation="get",outcome="miss"`,
		`domainry_runtime_redis_fallbacks_total{component="principal_cache"} 1`,
		`domainry_runtime_redis_rate_limit_decisions_total{decision="allowed"} 1`,
		`domainry_runtime_redis_rate_limit_decisions_total{decision="rejected"} 1`,
		`domainry_runtime_redis_pool_connections{component="principal_cache",state="total"}`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("Redis metrics missing %q:\n%s", expected, output)
		}
	}
	for _, forbidden := range []string{"secret-user-key", "missing-user-key", "secret-value", server.Addr()} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("Redis metrics leaked %q: %s", forbidden, output)
		}
	}
}

func TestMetricLabelsRejectUnboundedValues(t *testing.T) {
	if boundedComponent("customer-123") != "other" || boundedOperation("GET customer-secret") != "other" || boundedOutcome("tenant-error") != "error" {
		t.Fatal("unbounded Redis label value was accepted")
	}
}

func TestNoScriptIsReportedAsCacheMissInsteadOfRedisFailure(t *testing.T) {
	runtimeCollector.mu.Lock()
	runtimeCollector.operations = map[operationKey]*operationMetric{}
	runtimeCollector.fallbacks = map[string]uint64{}
	runtimeCollector.decisions = map[string]uint64{}
	runtimeCollector.clients = map[string]redis.UniversalClient{}
	runtimeCollector.mu.Unlock()

	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	Install(client, ComponentRateLimit)
	if err := client.EvalSha(t.Context(), "missing-script-sha", nil).Err(); !redis.HasErrorPrefix(err, "NOSCRIPT") {
		t.Fatalf("EvalSha error=%v", err)
	}

	output := OpenMetrics()
	if !strings.Contains(output, `component="rate_limit",operation="evalsha",outcome="miss"`) {
		t.Fatalf("NOSCRIPT was not classified as a cache miss:\n%s", output)
	}
	if strings.Contains(output, `component="rate_limit",operation="evalsha",outcome="error"`) {
		t.Fatalf("NOSCRIPT was incorrectly classified as an error:\n%s", output)
	}
}
