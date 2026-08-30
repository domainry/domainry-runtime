package integration

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/domainry/domainry-foundation/logging"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationruntime "github.com/domainry/domainry-runtime/runtime/domain/integration/runtime"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	capacityplatform "github.com/domainry/domainry-runtime/runtime/platform/capacity"
	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
	"github.com/domainry/domainry-runtime/runtime/platform/resilience"
	"go.uber.org/zap"
)

var integrationLatencyBuckets = [...]float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}

type integrationConnectorMetric struct {
	Connector string
	Outcome   string
	Count     uint64
	Sum       float64
	Buckets   []uint64
}

type integrationOperationalMetrics struct {
	mu                sync.RWMutex
	connectors        map[string]integrationConnectorMetric
	maxSeries         int
	droppedSeries     uint64
	queueDepth        int64
	queueLag          float64
	claimCount        uint64
	claimSeconds      float64
	claimBuckets      []uint64
	retries           uint64
	deadLetters       uint64
	leaseLost         uint64
	reconciliationLag float64
	inFlight          atomic.Int64
	queuePressure     atomic.Bool
	queueDepthLimit   int64
	queueLagLimit     float64
}

func newIntegrationOperationalMetrics() *integrationOperationalMetrics {
	return &integrationOperationalMetrics{connectors: map[string]integrationConnectorMetric{}, claimBuckets: make([]uint64, len(integrationLatencyBuckets)), maxSeries: 512, queueDepthLimit: 200, queueLagLimit: 300}
}

func (m *integrationOperationalMetrics) observeQueue(messages []integrationmodel.IntegrationOutboxMessage, now time.Time) (bool, bool) {
	if m == nil {
		return false, false
	}
	lag := 0.0
	for _, message := range messages {
		created, err := time.Parse(time.RFC3339, message.CreatedAt)
		if err == nil && now.After(created) && now.Sub(created).Seconds() > lag {
			lag = now.Sub(created).Seconds()
		}
	}
	m.mu.Lock()
	m.queueDepth, m.queueLag = int64(len(messages)), lag
	m.mu.Unlock()
	active := (m.queueDepthLimit > 0 && int64(len(messages)) >= m.queueDepthLimit) || (m.queueLagLimit > 0 && lag >= m.queueLagLimit)
	previous := m.queuePressure.Swap(active)
	return active, previous != active
}

func (m *integrationOperationalMetrics) observeClaim(duration time.Duration) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.claimCount++
	m.claimSeconds += duration.Seconds()
	for index, bound := range integrationLatencyBuckets {
		if duration.Seconds() <= bound {
			m.claimBuckets[index]++
		}
	}
	m.mu.Unlock()
}

func (m *integrationOperationalMetrics) beginWork() {
	if m != nil {
		m.inFlight.Add(1)
	}
}

func (m *integrationOperationalMetrics) endWork(bucket string) {
	if m == nil {
		return
	}
	m.inFlight.Add(-1)
	m.mu.Lock()
	defer m.mu.Unlock()
	switch bucket {
	case "retried":
		m.retries++
	case "dead_lettered":
		m.deadLetters++
	}
}

func (m *integrationOperationalMetrics) observeLeaseLost() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.leaseLost++
	m.mu.Unlock()
}

func (m *integrationOperationalMetrics) observeReconciliationLag(invocations []integrationmodel.IntegrationInvocation, now time.Time) {
	if m == nil {
		return
	}
	lag := 0.0
	for _, invocation := range invocations {
		created, err := time.Parse(time.RFC3339, invocation.CreatedAt)
		if err == nil && now.After(created) && now.Sub(created).Seconds() > lag {
			lag = now.Sub(created).Seconds()
		}
	}
	m.mu.Lock()
	m.reconciliationLag = lag
	m.mu.Unlock()
}

func (m *integrationOperationalMetrics) observeConnector(connector string, duration time.Duration, err error) {
	if m == nil {
		return
	}
	connector = boundedConnectorLabel(connector)
	outcome := connectorOutcome(err)
	key := connector + "\x00" + outcome
	m.mu.Lock()
	defer m.mu.Unlock()
	metric, exists := m.connectors[key]
	if !exists && len(m.connectors) >= m.maxSeries {
		m.droppedSeries++
		return
	}
	if !exists {
		metric = integrationConnectorMetric{Connector: connector, Outcome: outcome, Buckets: make([]uint64, len(integrationLatencyBuckets))}
	}
	metric.Count++
	metric.Sum += duration.Seconds()
	for index, bound := range integrationLatencyBuckets {
		if duration.Seconds() <= bound {
			metric.Buckets[index]++
		}
	}
	m.connectors[key] = metric
}

func (m *integrationOperationalMetrics) openMetrics() string {
	if m == nil {
		return ""
	}
	m.mu.RLock()
	series := make([]integrationConnectorMetric, 0, len(m.connectors))
	for _, metric := range m.connectors {
		metric.Buckets = append([]uint64(nil), metric.Buckets...)
		series = append(series, metric)
	}
	depth, lag, claims, claimSeconds := m.queueDepth, m.queueLag, m.claimCount, m.claimSeconds
	claimBuckets := append([]uint64(nil), m.claimBuckets...)
	retries, deadLetters, leaseLost, dropped, reconciliationLag := m.retries, m.deadLetters, m.leaseLost, m.droppedSeries, m.reconciliationLag
	depthLimit, lagLimit := m.queueDepthLimit, m.queueLagLimit
	m.mu.RUnlock()
	sort.Slice(series, func(i, j int) bool {
		if series[i].Connector != series[j].Connector {
			return series[i].Connector < series[j].Connector
		}
		return series[i].Outcome < series[j].Outcome
	})
	var output strings.Builder
	output.WriteString("# HELP domainry_runtime_worker_queue_depth Runnable integration outbox messages discovered by the bounded poll.\n# TYPE domainry_runtime_worker_queue_depth gauge\n")
	fmt.Fprintf(&output, "domainry_runtime_worker_queue_depth{owner=\"integration_outbox\"} %d\n", depth)
	output.WriteString("# HELP domainry_runtime_worker_queue_lag_seconds Oldest integration outbox message age.\n# TYPE domainry_runtime_worker_queue_lag_seconds gauge\n")
	fmt.Fprintf(&output, "domainry_runtime_worker_queue_lag_seconds{owner=\"integration_outbox\"} %.9f\n", lag)
	output.WriteString("# HELP domainry_runtime_worker_queue_backpressure Queue pressure state and configured thresholds.\n# TYPE domainry_runtime_worker_queue_backpressure gauge\n")
	pressure := 0
	if m.queuePressure.Load() {
		pressure = 1
	}
	fmt.Fprintf(&output, "domainry_runtime_worker_queue_backpressure{owner=\"integration_outbox\"} %d\ndomainry_runtime_worker_queue_threshold{owner=\"integration_outbox\",dimension=\"depth\"} %d\ndomainry_runtime_worker_queue_threshold{owner=\"integration_outbox\",dimension=\"oldest_age_seconds\"} %.0f\n", pressure, depthLimit, lagLimit)
	output.WriteString("# HELP domainry_runtime_worker_in_flight Current integration outbox work.\n# TYPE domainry_runtime_worker_in_flight gauge\n")
	fmt.Fprintf(&output, "domainry_runtime_worker_in_flight{owner=\"integration_outbox\"} %d\n", m.inFlight.Load())
	output.WriteString("# HELP domainry_runtime_worker_claims_total Integration outbox claims.\n# TYPE domainry_runtime_worker_claims_total counter\n")
	fmt.Fprintf(&output, "domainry_runtime_worker_claims_total{owner=\"integration_outbox\"} %d\n", claims)
	output.WriteString("# HELP domainry_runtime_worker_claim_duration_seconds Integration outbox claim latency.\n# TYPE domainry_runtime_worker_claim_duration_seconds histogram\n")
	for index, bound := range integrationLatencyBuckets {
		fmt.Fprintf(&output, "domainry_runtime_worker_claim_duration_seconds_bucket{owner=\"integration_outbox\",le=%q} %d\n", strconv.FormatFloat(bound, 'f', -1, 64), claimBuckets[index])
	}
	fmt.Fprintf(&output, "domainry_runtime_worker_claim_duration_seconds_bucket{owner=\"integration_outbox\",le=\"+Inf\"} %d\n", claims)
	fmt.Fprintf(&output, "domainry_runtime_worker_claim_duration_seconds_sum{owner=\"integration_outbox\"} %.9f\n", claimSeconds)
	fmt.Fprintf(&output, "domainry_runtime_worker_claim_duration_seconds_count{owner=\"integration_outbox\"} %d\n", claims)
	for _, outcome := range []struct {
		name  string
		value uint64
	}{{"retry", retries}, {"dead_letter", deadLetters}, {"lease_lost", leaseLost}} {
		fmt.Fprintf(&output, "domainry_runtime_worker_outcomes_total{owner=\"integration_outbox\",outcome=%q} %d\n", outcome.name, outcome.value)
	}
	output.WriteString("# HELP domainry_runtime_connector_request_duration_seconds Connector request latency by bounded connector and outcome.\n# TYPE domainry_runtime_connector_request_duration_seconds histogram\n")
	for _, metric := range series {
		labels := fmt.Sprintf("connector=%q,outcome=%q", metric.Connector, metric.Outcome)
		for index, bound := range integrationLatencyBuckets {
			fmt.Fprintf(&output, "domainry_runtime_connector_request_duration_seconds_bucket{%s,le=%q} %d\n", labels, strconv.FormatFloat(bound, 'f', -1, 64), metric.Buckets[index])
		}
		fmt.Fprintf(&output, "domainry_runtime_connector_request_duration_seconds_bucket{%s,le=\"+Inf\"} %d\n", labels, metric.Count)
		fmt.Fprintf(&output, "domainry_runtime_connector_request_duration_seconds_sum{%s} %.9f\n", labels, metric.Sum)
		fmt.Fprintf(&output, "domainry_runtime_connector_request_duration_seconds_count{%s} %d\n", labels, metric.Count)
	}
	output.WriteString("# HELP domainry_runtime_connector_reconciliation_lag_seconds Oldest prepared invocation awaiting an external receipt.\n# TYPE domainry_runtime_connector_reconciliation_lag_seconds gauge\n")
	fmt.Fprintf(&output, "domainry_runtime_connector_reconciliation_lag_seconds %.9f\n", reconciliationLag)
	fmt.Fprintf(&output, "domainry_runtime_telemetry_dropped_series_total{signal=\"connector\"} %d\n", dropped)
	return output.String()
}

func boundedConnectorLabel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || len(value) > 64 {
		return "unknown"
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '_' || character == '-' {
			continue
		}
		return "unknown"
	}
	return value
}

func connectorOutcome(err error) string {
	if err == nil {
		return "success"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "rate_limit") || strings.Contains(text, "rate limited") || strings.Contains(text, "http_429") {
		return "rate_limited"
	}
	return "provider_error"
}

func (s *IntegrationApplicationService) ProcessDueIntegrationOutbox(ctx context.Context, limit int, principal principalmodel.Principal) (OutboxProcessBatchResult, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return OutboxProcessBatchResult{}, err
	}
	if !HasPermission(principal, PermissionRetry) {
		return OutboxProcessBatchResult{}, forbidden("auth.permission_denied")
	}
	if limit <= 0 {
		limit = 25
	} else if limit > 200 {
		limit = 200
	}
	due, err := s.listDueIntegrationOutbox(ctx, limit)
	if err != nil {
		return OutboxProcessBatchResult{}, err
	}
	result := OutboxProcessBatchResult{Messages: []integrationmodel.IntegrationOutboxMessage{}}
	for _, message := range due {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		processed, bucket := s.processDueOutboxMessage(ctx, message, integrationruntime.IntegrationWorkerPrincipal(message.WorkspaceID))
		result.Messages = append(result.Messages, processed)
		observeOutboxBatchBucket(&result, bucket)
	}
	return result, nil
}

func (s *IntegrationApplicationService) listDueIntegrationOutbox(ctx context.Context, limit int) ([]integrationmodel.IntegrationOutboxMessage, error) {
	workerScope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "poll due integration outbox")
	due, err := s.publicationWorkerRepo.ListDueOutbox(ctx, workerScope, capacityplatform.OverscanLimit(limit, 4, 800), s.worker.Clock.Now().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	if s.operationalMetrics != nil {
		active, changed := s.operationalMetrics.observeQueue(due, s.worker.Clock.Now())
		if changed && active {
			logging.FromContext(ctx).Warn("integration queue backpressure activated", zap.Int("observed_depth", len(due)))
		} else if changed {
			logging.FromContext(ctx).Info("integration queue backpressure recovered", zap.Int("observed_depth", len(due)))
		}
	}
	// Recovery enters each workspace before reading owner rows. Fairness is
	// applied again here before the rows are reduced to exact executor locators.
	due = capacityplatform.FairOrder(due, limit, func(message integrationmodel.IntegrationOutboxMessage) string {
		return message.WorkspaceID
	})
	return due, nil
}

func observeOutboxBatchBucket(result *OutboxProcessBatchResult, bucket string) {
	switch bucket {
	case "sent":
		result.Sent++
	case "retried":
		result.Retried++
	case "dead_lettered":
		result.DeadLettered++
	case "reconciliation_required":
		result.ReconciliationRequired++
	default:
		result.Skipped++
	}
}

func (s *IntegrationApplicationService) OperationalMetricsOpenMetrics(_ context.Context) string {
	if s == nil {
		return ""
	}
	return s.operationalMetrics.openMetrics() + integrationMemoryStateOpenMetrics(s.apiLimiter, s.policyStore) + connectorCapacityOpenMetrics(s.connectorCapacity)
}

func (s *IntegrationApplicationService) UseQueueBackpressureThresholds(_ context.Context, depth int, oldestAge time.Duration) {
	if s == nil || s.operationalMetrics == nil {
		return
	}
	if depth > 0 {
		s.operationalMetrics.queueDepthLimit = int64(depth)
	}
	if oldestAge > 0 {
		s.operationalMetrics.queueLagLimit = oldestAge.Seconds()
	}
}

func (s *IntegrationApplicationService) QueueBackpressureActive(context.Context) bool {
	return s != nil && s.operationalMetrics != nil && s.operationalMetrics.queuePressure.Load()
}

func connectorCapacityOpenMetrics(controller *capacityplatform.Controller) string {
	if controller == nil {
		return ""
	}
	snapshot := controller.Snapshot()
	return fmt.Sprintf("# HELP domainry_runtime_connector_capacity_in_flight Current connector calls admitted by the provider bulkhead.\n# TYPE domainry_runtime_connector_capacity_in_flight gauge\ndomainry_runtime_connector_capacity_in_flight %d\n# HELP domainry_runtime_connector_capacity_limit Configured global connector call limit.\n# TYPE domainry_runtime_connector_capacity_limit gauge\ndomainry_runtime_connector_capacity_limit %d\n# HELP domainry_runtime_connector_capacity_rejected_total Connector calls rejected before provider execution.\n# TYPE domainry_runtime_connector_capacity_rejected_total counter\ndomainry_runtime_connector_capacity_rejected_total %d\n", snapshot.GlobalInFlight, snapshot.GlobalLimit, snapshot.RejectedTotal)
}

func integrationMemoryStateOpenMetrics(limiter ratelimit.Limiter, policy resilience.Store) string {
	var output strings.Builder
	output.WriteString("# HELP domainry_runtime_bounded_state_entries Current reconstructible in-memory state entries.\n# TYPE domainry_runtime_bounded_state_entries gauge\n")
	output.WriteString("# HELP domainry_runtime_bounded_state_capacity Configured in-memory state capacity.\n# TYPE domainry_runtime_bounded_state_capacity gauge\n")
	output.WriteString("# HELP domainry_runtime_bounded_state_evictions_total Capacity and TTL evictions of reconstructible in-memory state.\n# TYPE domainry_runtime_bounded_state_evictions_total counter\n")
	if provider, ok := limiter.(interface{ Stats() ratelimit.Stats }); ok {
		stats := provider.Stats()
		fmt.Fprintf(&output, "domainry_runtime_bounded_state_entries{owner=\"rate_limit\"} %d\ndomainry_runtime_bounded_state_capacity{owner=\"rate_limit\"} %d\ndomainry_runtime_bounded_state_evictions_total{owner=\"rate_limit\",reason=\"capacity\"} %d\ndomainry_runtime_bounded_state_evictions_total{owner=\"rate_limit\",reason=\"ttl\"} %d\n", stats.Entries, stats.Capacity, stats.Evictions, stats.Expirations)
	}
	if provider, ok := policy.(interface{ Stats() resilience.Stats }); ok {
		stats := provider.Stats()
		fmt.Fprintf(&output, "domainry_runtime_bounded_state_entries{owner=\"resilience\"} %d\ndomainry_runtime_bounded_state_capacity{owner=\"resilience\"} %d\ndomainry_runtime_bounded_state_evictions_total{owner=\"resilience\",reason=\"capacity\"} %d\ndomainry_runtime_bounded_state_evictions_total{owner=\"resilience\",reason=\"ttl\"} %d\n", stats.Entries, stats.Capacity, stats.Evictions, stats.Expirations)
	}
	return output.String()
}
