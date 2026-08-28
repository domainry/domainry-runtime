package projection

import (
	"reflect"
	"testing"
	"time"

	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestDeploymentWorkflowMetrics(t *testing.T) {
	now := time.Date(2026, 1, 2, 0, 0, 10, 0, time.UTC)
	executions := []workflowmodel.WorkflowExecution{
		{Status: "", CreatedAt: "2026-01-01T23:59:58Z", UpdatedAt: "2026-01-01T23:59:59Z"},
		{Status: "pending", Attempt: 1, MaxAttempts: 3, NextRunAt: "later", CreatedAt: "2026-01-01T23:59:50Z", UpdatedAt: "2026-01-01T23:59:51Z"},
		{Status: "running", Attempt: 2, CreatedAt: "2026-01-01T23:59:55Z", UpdatedAt: "2026-01-01T23:59:54Z"},
		{Status: "failed", Trigger: " retry:event ", Attempt: 1, MaxAttempts: 3, NextRunAt: "later", CreatedAt: "bad", UpdatedAt: "2026-01-02T00:00:01Z"},
		{Status: "failed", Attempt: 3, MaxAttempts: 3, NextRunAt: "later", CreatedAt: "2026-01-02T00:00:02Z", UpdatedAt: "bad"},
		{Status: "failed", Attempt: 1, MaxAttempts: 0, NextRunAt: "", CreatedAt: "", UpdatedAt: ""},
	}
	metrics := DeploymentWorkflowMetrics(4, executions, now)
	counts := metrics["status_counts"].(map[string]int)
	if counts["unknown"] != 1 || counts["failed"] != 3 || metrics["queue_depth"] != 3 || metrics["retry_count"] != 3 || metrics["oldest_pending_age_seconds"] != 20 || metrics["last_execution_at"] != "2026-01-02T00:00:02Z" {
		t.Fatalf("metrics = %#v", metrics)
	}
	empty := DeploymentWorkflowMetrics(0, nil, now)
	if empty["last_execution_at"] != "" || empty["queue_depth"] != 0 {
		t.Fatalf("empty metrics = %#v", empty)
	}
}

func TestDeploymentBusinessActionMetrics(t *testing.T) {
	invocations := []integrationmodel.IntegrationInvocation{
		{Metadata: nil},
		{Metadata: map[string]any{"action_key": "action"}, Status: "", ConnectorKey: " c ", DurationMS: -1},
		{Metadata: map[string]any{"invocation_key": "send"}, Status: "failed", Error: "", DurationMS: 50},
		{Metadata: map[string]any{"action_key": "action", "invocation_key": "send"}, Status: "failed", Error: " boom ", DurationMS: 500},
		{Metadata: map[string]any{"action_key": "other"}, Status: "success", DurationMS: 1500},
	}
	metrics := DeploymentBusinessActionMetrics(2, invocations, 10)
	if metrics["recent_invocations"] != 4 || !reflect.DeepEqual(metrics["status_counts"], map[string]int{"unknown": 1, "failed": 2, "success": 1}) || !reflect.DeepEqual(metrics["failure_counts"], map[string]int{"unknown": 1, "boom": 1}) {
		t.Fatalf("metrics = %#v", metrics)
	}
	durations := metrics["invocation_duration_ms"].(map[string]any)
	if durations["avg"] != 512 || durations["max"] != 1500 || durations["le_100"] != 2 || durations["le_1000"] != 1 || durations["gt_1000"] != 1 {
		t.Fatalf("durations = %#v", durations)
	}
	if got := DeploymentBusinessActionMetrics(0, nil, 0)["invocation_duration_ms"].(map[string]any)["avg"]; got != 0 {
		t.Fatalf("empty avg = %v", got)
	}
}

func TestDeploymentConfiguredAuditAndHelpers(t *testing.T) {
	actions := []definitionmodel.ActionSchema{
		{Kind: "object_create"},
		{Kind: " object_operation "},
		{Kind: "bulk_operation"},
		{Kind: "record_update"},
		{Kind: "record_delete"},
		{Kind: "record_operation"},
		{Kind: "other"},
	}
	if got := DeploymentBusinessActionConfiguredCount(actions); got != 6 {
		t.Fatalf("configured = %d", got)
	}
	events := []auditmodel.AuditEvent{
		{Event: "", CreatedAt: "bad"},
		{Event: " created ", ObjectKey: " order ", RoleKey: " sales ", CreatedAt: "2026-01-01T00:00:00Z"},
		{Event: "created", ObjectKey: "", RoleKey: "", CreatedAt: "2025-01-01T00:00:00Z"},
	}
	metrics := DeploymentAuditMetrics(events, 3)
	if metrics["last_event_at"] != "2026-01-01T00:00:00Z" || !reflect.DeepEqual(metrics["events"], map[string]int{"unknown": 1, "created": 2}) {
		t.Fatalf("audit metrics = %#v", metrics)
	}
	if DeploymentAuditMetrics(nil, 0)["last_event_at"] != "" {
		t.Fatal("empty audit timestamp must be blank")
	}

	for _, status := range []workflowmodel.WorkflowExecution{
		{Status: "success", NextRunAt: "later"},
		{Status: "failed", MaxAttempts: 1, Attempt: 1, NextRunAt: "later"},
		{Status: "pending", MaxAttempts: 2, Attempt: 1, NextRunAt: "later"},
		{Status: "failed", MaxAttempts: 0, Attempt: 99, NextRunAt: "later"},
	} {
		_ = deploymentWorkflowRetryScheduled(status)
	}
	if deploymentMetadataString(map[string]any{"value": " x "}, "value") != "x" || deploymentMetadataString(nil, "missing") != "" {
		t.Fatal("metadata helper mismatch")
	}
	for _, kind := range definitionmodel.ActionKindValues() {
		if !deploymentBusinessActionKind(kind) {
			t.Fatalf("business kind %q rejected", kind)
		}
	}
	if deploymentBusinessActionKind("unknown") {
		t.Fatal("unknown kind accepted")
	}
	first := map[string]any{"id": "one"}
	if got := deploymentMapSlice([]map[string]any{first}); len(got) != 1 || got[0]["id"] != "one" {
		t.Fatalf("typed map slice=%#v", got)
	}
	if got := deploymentMapSlice([]any{map[string]any{}, "ignored", first}); len(got) != 1 || got[0]["id"] != "one" {
		t.Fatalf("generic map slice=%#v", got)
	}
	if got := deploymentMapSlice("ignored"); got != nil {
		t.Fatalf("unsupported map slice=%#v", got)
	}
}
