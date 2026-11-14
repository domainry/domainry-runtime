package redisdiagnostics

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	redis "github.com/redis/go-redis/v9"
)

const (
	ComponentPrincipalCache = "principal_cache"
	ComponentRateLimit      = "rate_limit"
)

var redisDurationBuckets = []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}

type operationKey struct {
	component string
	operation string
	outcome   string
}

type operationMetric struct {
	count   uint64
	sum     float64
	buckets []uint64
}

type collector struct {
	mu         sync.RWMutex
	operations map[operationKey]*operationMetric
	fallbacks  map[string]uint64
	decisions  map[string]uint64
	clients    map[string]redis.UniversalClient
}

var runtimeCollector = collector{
	operations: map[operationKey]*operationMetric{},
	fallbacks:  map[string]uint64{},
	decisions:  map[string]uint64{},
	clients:    map[string]redis.UniversalClient{},
}

func registerClient(component string, client redis.UniversalClient) {
	runtimeCollector.mu.Lock()
	runtimeCollector.clients[boundedComponent(component)] = client
	runtimeCollector.mu.Unlock()
}

func observeOperation(component, operation, outcome string, duration time.Duration) {
	key := operationKey{component: boundedComponent(component), operation: boundedOperation(operation), outcome: boundedOutcome(outcome)}
	runtimeCollector.mu.Lock()
	metric := runtimeCollector.operations[key]
	if metric == nil {
		metric = &operationMetric{buckets: make([]uint64, len(redisDurationBuckets))}
		runtimeCollector.operations[key] = metric
	}
	metric.count++
	seconds := duration.Seconds()
	metric.sum += seconds
	for index, bound := range redisDurationBuckets {
		if seconds <= bound {
			metric.buckets[index]++
		}
	}
	runtimeCollector.mu.Unlock()
}

// ObserveFallback records a request that continued through the authoritative
// Identity/database path after the optional Redis cache failed.
func ObserveFallback(component string) {
	runtimeCollector.mu.Lock()
	runtimeCollector.fallbacks[boundedComponent(component)]++
	runtimeCollector.mu.Unlock()
}

// ObserveRateLimitDecision records only the bounded allow/reject outcome. The
// bucket identity may contain tenant information and is never a metric label.
func ObserveRateLimitDecision(allowed bool) {
	decision := "rejected"
	if allowed {
		decision = "allowed"
	}
	runtimeCollector.mu.Lock()
	runtimeCollector.decisions[decision]++
	runtimeCollector.mu.Unlock()
}

func dependencyOutcome(err error) string {
	if err != nil {
		return "error"
	}
	return "success"
}

func commandOperation(value string) string {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(value)))
	if len(fields) == 0 {
		return "other"
	}
	return boundedOperation(fields[0])
}

func boundedComponent(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ComponentPrincipalCache:
		return ComponentPrincipalCache
	case ComponentRateLimit:
		return ComponentRateLimit
	default:
		return "other"
	}
}

func boundedOperation(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "dial", "get", "set", "del", "sadd", "srem", "eval", "evalsha", "ping", "pipeline":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "other"
	}
}

func boundedOutcome(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "success", "miss", "error":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "error"
	}
}

// OpenMetrics exposes low-cardinality Redis client facts. Redis server memory,
// replication, persistence, eviction and blocked-client state come from
// redis_exporter and remain separate from these application-side facts.
func OpenMetrics() string {
	type operationSnapshot struct {
		key operationKey
		operationMetric
	}
	runtimeCollector.mu.RLock()
	operations := make([]operationSnapshot, 0, len(runtimeCollector.operations))
	for key, metric := range runtimeCollector.operations {
		operations = append(operations, operationSnapshot{key: key, operationMetric: operationMetric{count: metric.count, sum: metric.sum, buckets: append([]uint64(nil), metric.buckets...)}})
	}
	fallbacks := cloneCounters(runtimeCollector.fallbacks)
	decisions := cloneCounters(runtimeCollector.decisions)
	clients := make(map[string]redis.UniversalClient, len(runtimeCollector.clients))
	for component, client := range runtimeCollector.clients {
		clients[component] = client
	}
	runtimeCollector.mu.RUnlock()
	sort.Slice(operations, func(i, j int) bool {
		left, right := operations[i].key, operations[j].key
		if left.component != right.component {
			return left.component < right.component
		}
		if left.operation != right.operation {
			return left.operation < right.operation
		}
		return left.outcome < right.outcome
	})

	var output strings.Builder
	output.WriteString("# HELP domainry_runtime_redis_operations_total Redis client operations by bounded component, command and outcome.\n# TYPE domainry_runtime_redis_operations_total counter\n")
	output.WriteString("# HELP domainry_runtime_redis_operation_duration_seconds Redis client operation duration.\n# TYPE domainry_runtime_redis_operation_duration_seconds histogram\n")
	for _, snapshot := range operations {
		labels := fmt.Sprintf("component=%q,operation=%q,outcome=%q", snapshot.key.component, snapshot.key.operation, snapshot.key.outcome)
		fmt.Fprintf(&output, "domainry_runtime_redis_operations_total{%s} %d\n", labels, snapshot.count)
		for index, bound := range redisDurationBuckets {
			fmt.Fprintf(&output, "domainry_runtime_redis_operation_duration_seconds_bucket{%s,le=%q} %d\n", labels, strconv.FormatFloat(bound, 'f', -1, 64), snapshot.buckets[index])
		}
		fmt.Fprintf(&output, "domainry_runtime_redis_operation_duration_seconds_bucket{%s,le=\"+Inf\"} %d\n", labels, snapshot.count)
		fmt.Fprintf(&output, "domainry_runtime_redis_operation_duration_seconds_sum{%s} %.9f\n", labels, snapshot.sum)
		fmt.Fprintf(&output, "domainry_runtime_redis_operation_duration_seconds_count{%s} %d\n", labels, snapshot.count)
	}
	output.WriteString("# HELP domainry_runtime_redis_fallbacks_total Requests that fell back to an authoritative store after an optional Redis cache error.\n# TYPE domainry_runtime_redis_fallbacks_total counter\n")
	for _, component := range sortedCounterKeys(fallbacks) {
		fmt.Fprintf(&output, "domainry_runtime_redis_fallbacks_total{component=%q} %d\n", component, fallbacks[component])
	}
	output.WriteString("# HELP domainry_runtime_redis_rate_limit_decisions_total Shared Redis rate-limit decisions.\n# TYPE domainry_runtime_redis_rate_limit_decisions_total counter\n")
	for _, decision := range []string{"allowed", "rejected"} {
		fmt.Fprintf(&output, "domainry_runtime_redis_rate_limit_decisions_total{decision=%q} %d\n", decision, decisions[decision])
	}
	writePoolMetrics(&output, clients)
	return output.String()
}

func writePoolMetrics(output *strings.Builder, clients map[string]redis.UniversalClient) {
	output.WriteString("# HELP domainry_runtime_redis_pool_connections Redis client connection pool state.\n# TYPE domainry_runtime_redis_pool_connections gauge\n")
	output.WriteString("# HELP domainry_runtime_redis_pool_events_total Redis client connection pool events.\n# TYPE domainry_runtime_redis_pool_events_total counter\n")
	output.WriteString("# HELP domainry_runtime_redis_pool_wait_duration_seconds_total Time spent waiting for a Redis client connection.\n# TYPE domainry_runtime_redis_pool_wait_duration_seconds_total counter\n")
	for _, component := range sortedClientKeys(clients) {
		stats := clients[component].PoolStats()
		if stats == nil {
			continue
		}
		fmt.Fprintf(output, "domainry_runtime_redis_pool_connections{component=%q,state=\"total\"} %d\n", component, stats.TotalConns)
		fmt.Fprintf(output, "domainry_runtime_redis_pool_connections{component=%q,state=\"idle\"} %d\n", component, stats.IdleConns)
		fmt.Fprintf(output, "domainry_runtime_redis_pool_connections{component=%q,state=\"pending\"} %d\n", component, stats.PendingRequests)
		events := []struct {
			name  string
			value uint32
		}{{"hit", stats.Hits}, {"miss", stats.Misses}, {"timeout", stats.Timeouts}, {"wait", stats.WaitCount}, {"unusable", stats.Unusable}, {"stale", stats.StaleConns}}
		for _, event := range events {
			fmt.Fprintf(output, "domainry_runtime_redis_pool_events_total{component=%q,event=%q} %d\n", component, event.name, event.value)
		}
		fmt.Fprintf(output, "domainry_runtime_redis_pool_wait_duration_seconds_total{component=%q} %.9f\n", component, float64(stats.WaitDurationNs)/float64(time.Second))
	}
}

func cloneCounters(source map[string]uint64) map[string]uint64 {
	result := make(map[string]uint64, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func sortedCounterKeys(values map[string]uint64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedClientKeys(values map[string]redis.UniversalClient) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
