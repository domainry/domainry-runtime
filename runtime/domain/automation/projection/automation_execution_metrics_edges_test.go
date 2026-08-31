package projection

import (
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	"testing"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
)

func TestBuildAutomationExecutionMetricsBoundaries(t *testing.T) {
	if got := BuildAutomationExecutionMetrics(nil, nil, nil); got.Total != 0 || got.AverageMS != 0 || len(got.ByStatus) != 0 {
		t.Fatalf("%#v", got)
	}
	items := []automationmodel.AutomationRuleExecution{
		{Status: "blocked", DurationMS: 30, EventID: " e1 ", Trace: map[string]any{"actions": []map[string]any{{"invocation_id": " i1 "}, {"invocation_id": nil}}}},
		{Status: "failed", DurationMS: 10, Trace: map[string]any{"actions": []any{"bad", map[string]any{"invocation_id": "i2"}, map[string]any{"invocation_id": ""}}}},
		{Status: "other", DurationMS: 5},
	}
	invocations := []integrationsdk.Invocation{{ID: "i1", Status: "success", DurationMS: 8}, {ID: "i2", Status: "sent", DurationMS: 12}, {ID: "missing", Status: "failed", DurationMS: 99}}
	outbox := []publicationmodel.Message{{EventID: "e1", Status: "dead_lettered", AttemptCount: 2}, {EventID: "e1", Status: "retry", AttemptCount: 1}, {EventID: "missing", AttemptCount: 4}}
	got := BuildAutomationExecutionMetrics(items, invocations, outbox)
	if got.Blocked != 1 || got.Failed != 1 || got.ConnectorCalls != 2 || got.ConnectorSucceeded != 2 || got.ConnectorFailed != 0 || got.MaxMS != 30 || got.MaxConnectorLatencyMS != 12 || got.RetryCount != 3 || got.DeadLetterCount != 1 {
		t.Fatalf("%#v", got)
	}
	failed := BuildAutomationExecutionMetrics([]automationmodel.AutomationRuleExecution{{Trace: map[string]any{"actions": []any{map[string]any{"invocation_id": "i"}}}}}, []integrationsdk.Invocation{{ID: "i", Status: "failed"}}, nil)
	if failed.ConnectorFailed != 1 {
		t.Fatalf("%#v", failed)
	}
	if got := executionTraceActions(map[string]any{"actions": "bad"}); len(got) != 0 {
		t.Fatalf("%#v", got)
	}
}
