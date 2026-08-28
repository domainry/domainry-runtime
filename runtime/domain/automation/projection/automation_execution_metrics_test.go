package projection

import (
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestBuildExecutionMetricsCorrelatesAutomationEvidence(t *testing.T) {
	items := []automationmodel.AutomationRuleExecution{{Status: "succeeded", DurationMS: 20, EventID: "event-1", Trace: map[string]any{"actions": []any{map[string]any{"invocation_id": "invocation-1"}}}}}
	invocations := []integrationmodel.IntegrationInvocation{{ID: "invocation-1", Status: "succeeded", DurationMS: 8}, {ID: "unrelated", Status: "failed", DurationMS: 99}}
	outbox := []integrationmodel.IntegrationOutboxMessage{{EventID: "event-1", Status: "dead_letter", AttemptCount: 3}, {EventID: "unrelated", AttemptCount: 7}}

	metrics := BuildAutomationExecutionMetrics(items, invocations, outbox)
	if metrics.Total != 1 || metrics.Succeeded != 1 || metrics.AverageMS != 20 || metrics.ConnectorCalls != 1 || metrics.ConnectorSucceeded != 1 || metrics.AverageConnectorLatencyMS != 8 || metrics.RetryCount != 3 || metrics.DeadLetterCount != 1 {
		t.Fatalf("metrics=%#v", metrics)
	}
}
