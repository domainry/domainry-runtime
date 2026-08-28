package integration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	capacityplatform "github.com/domainry/domainry-runtime/runtime/platform/capacity"
	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
	"github.com/domainry/domainry-runtime/runtime/platform/resilience"
)

func TestIntegrationOperationalMetricsExposeBoundedOpenMetrics(t *testing.T) {
	metrics := newIntegrationOperationalMetrics()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	metrics.observeQueue([]integrationmodel.IntegrationOutboxMessage{
		{CreatedAt: now.Add(-3 * time.Second).Format(time.RFC3339)},
		{CreatedAt: now.Add(-9 * time.Second).Format(time.RFC3339)},
		{CreatedAt: now.Add(-2 * time.Second).Format(time.RFC3339)},
	}, now)
	metrics.observeClaim(25 * time.Millisecond)
	metrics.beginWork()
	metrics.endWork("retried")
	metrics.beginWork()
	metrics.endWork("dead_lettered")
	metrics.observeLeaseLost()
	metrics.observeReconciliationLag([]integrationmodel.IntegrationInvocation{{CreatedAt: now.Add(-12 * time.Second).Format(time.RFC3339)}}, now)
	metrics.observeConnector("Slack-Webhook", 75*time.Millisecond, nil)
	metrics.observeConnector("Slack-Webhook", time.Second, context.DeadlineExceeded)
	metrics.observeConnector("unsafe/tenant/42", time.Second, errors.New("HTTP_429"))

	output := metrics.openMetrics()
	for _, expected := range []string{
		`domainry_runtime_worker_queue_depth{owner="integration_outbox"} 3`,
		`domainry_runtime_worker_queue_lag_seconds{owner="integration_outbox"} 9.000000000`,
		`domainry_runtime_worker_in_flight{owner="integration_outbox"} 0`,
		`domainry_runtime_worker_claim_duration_seconds_count{owner="integration_outbox"} 1`,
		`domainry_runtime_worker_outcomes_total{owner="integration_outbox",outcome="retry"} 1`,
		`domainry_runtime_worker_outcomes_total{owner="integration_outbox",outcome="dead_letter"} 1`,
		`domainry_runtime_worker_outcomes_total{owner="integration_outbox",outcome="lease_lost"} 1`,
		`connector="slack-webhook",outcome="success"`,
		`connector="slack-webhook",outcome="timeout"`,
		`connector="unknown",outcome="rate_limited"`,
		`domainry_runtime_connector_reconciliation_lag_seconds 12.000000000`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected metrics to contain %q, got:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "tenant/42") {
		t.Fatalf("unbounded connector label leaked into metrics: %s", output)
	}
}

func TestIntegrationOperationalMetricsEnforceSeriesBudget(t *testing.T) {
	metrics := newIntegrationOperationalMetrics()
	metrics.maxSeries = 1
	metrics.observeConnector("first", time.Millisecond, nil)
	metrics.observeConnector("second", time.Millisecond, nil)

	output := metrics.openMetrics()
	if !strings.Contains(output, `domainry_runtime_telemetry_dropped_series_total{signal="connector"} 1`) {
		t.Fatalf("expected dropped-series accounting, got:\n%s", output)
	}
}

func TestIntegrationQueuePressureActivatesAndRecoversAtThresholds(t *testing.T) {
	metrics := newIntegrationOperationalMetrics()
	metrics.queueDepthLimit = 1
	active, changed := metrics.observeQueue([]integrationmodel.IntegrationOutboxMessage{{CreatedAt: time.Now().Add(-time.Minute).Format(time.RFC3339)}}, time.Now())
	if !active || !changed || !metrics.queuePressure.Load() {
		t.Fatalf("queue pressure did not activate: active=%v changed=%v", active, changed)
	}
	active, changed = metrics.observeQueue(nil, time.Now())
	if active || !changed || metrics.queuePressure.Load() {
		t.Fatalf("queue pressure did not recover: active=%v changed=%v", active, changed)
	}
}

func TestConnectorOutcomeUsesStableBuckets(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{nil, "success"},
		{context.DeadlineExceeded, "timeout"},
		{errors.New("provider rate limited request"), "rate_limited"},
		{errors.New("provider rate_limit request"), "rate_limited"},
		{errors.New("credential rejected: secret-value"), "provider_error"},
	}
	for _, testCase := range cases {
		if got := connectorOutcome(testCase.err); got != testCase.want {
			t.Fatalf("connectorOutcome(%v) = %q, want %q", testCase.err, got, testCase.want)
		}
	}
}

func TestIntegrationMemoryStateOpenMetrics(t *testing.T) {
	limiter := ratelimit.NewMemoryLimiter(3)
	policy := resilience.NewMemoryStore(4)
	_, _ = limiter.Allow(t.Context(), "one", 2, time.Minute)
	if err := policy.Before(t.Context(), "one", resilience.Config{RateLimit: 2}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	metrics := integrationMemoryStateOpenMetrics(limiter, policy)
	for _, expected := range []string{
		`domainry_runtime_bounded_state_entries{owner="rate_limit"} 1`,
		`domainry_runtime_bounded_state_capacity{owner="rate_limit"} 3`,
		`domainry_runtime_bounded_state_entries{owner="resilience"} 1`,
		`domainry_runtime_bounded_state_capacity{owner="resilience"} 4`,
	} {
		if !strings.Contains(metrics, expected) {
			t.Fatalf("metrics missing %q:\n%s", expected, metrics)
		}
	}
}

func TestConnectorCapacityOpenMetrics(t *testing.T) {
	controller := capacityplatform.NewController(capacityplatform.Limits{GlobalInFlight: 1, WorkspaceInFlight: 1, UseCaseInFlight: 1}, nil)
	lease, _ := controller.Acquire(t.Context(), capacityplatform.Request{WorkspaceID: "one", UseCase: "provider"})
	_, _ = controller.Acquire(t.Context(), capacityplatform.Request{WorkspaceID: "two", UseCase: "provider"})
	metrics := connectorCapacityOpenMetrics(controller)
	lease.Release()
	for _, expected := range []string{"domainry_runtime_connector_capacity_in_flight 1", "domainry_runtime_connector_capacity_limit 1", "domainry_runtime_connector_capacity_rejected_total 1"} {
		if !strings.Contains(metrics, expected) {
			t.Fatalf("connector capacity metrics missing %q:\n%s", expected, metrics)
		}
	}
}

func TestIntegrationOperationalMetricsNilAndBoundaryEdges(t *testing.T) {
	var metrics *integrationOperationalMetrics
	if active, changed := metrics.observeQueue(nil, time.Now()); active || changed {
		t.Fatalf("nil queue metrics=%t %t", active, changed)
	}
	metrics.observeClaim(time.Second)
	metrics.beginWork()
	metrics.endWork("retried")
	metrics.observeLeaseLost()
	metrics.observeReconciliationLag(nil, time.Now())
	metrics.observeConnector("connector", time.Second, nil)
	if got := metrics.openMetrics(); got != "" {
		t.Fatalf("nil open metrics=%q", got)
	}
	if got := connectorCapacityOpenMetrics(nil); got != "" {
		t.Fatalf("nil capacity metrics=%q", got)
	}
	var service *IntegrationApplicationService
	service.UseQueueBackpressureThresholds(t.Context(), 1, time.Second)
	withoutMetrics := NewIntegrationApplicationService(ApplicationDependencies{})
	withoutMetrics.operationalMetrics = nil
	withoutMetrics.UseQueueBackpressureThresholds(t.Context(), 1, time.Second)

	metrics = newIntegrationOperationalMetrics()
	metrics.queueDepthLimit = 0
	metrics.queueLagLimit = 1
	now := time.Now().UTC()
	active, _ := metrics.observeQueue([]integrationmodel.IntegrationOutboxMessage{{CreatedAt: "invalid"}, {CreatedAt: now.Add(time.Minute).Format(time.RFC3339)}, {CreatedAt: now.Add(-2 * time.Second).Format(time.RFC3339)}}, now)
	if !active {
		t.Fatal("lag pressure did not activate")
	}
	metrics.beginWork()
	metrics.endWork("sent")
	metrics.observeReconciliationLag([]integrationmodel.IntegrationInvocation{{CreatedAt: "invalid"}, {CreatedAt: now.Add(time.Minute).Format(time.RFC3339)}}, now)
	metrics.observeConnector("provider", 40*time.Second, errors.New("provider failed"))
	if output := metrics.openMetrics(); !strings.Contains(output, `domainry_runtime_worker_queue_backpressure{owner="integration_outbox"} 1`) {
		t.Fatalf("pressure metric missing:\n%s", output)
	}
	if boundedConnectorLabel("") != "unknown" || boundedConnectorLabel(strings.Repeat("a", 65)) != "unknown" || boundedConnectorLabel("bad.label") != "unknown" || boundedConnectorLabel("a{b") != "unknown" || boundedConnectorLabel("OK_name-1") != "ok_name-1" {
		t.Fatal("connector label bounds mismatch")
	}
}
