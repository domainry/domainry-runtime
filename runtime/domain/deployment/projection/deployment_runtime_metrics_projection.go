package projection

import (
	"fmt"
	"strings"
	"time"

	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

// DeploymentWorkflowMetrics is the pure deployment observability projection for
// workflow execution snapshots.
func DeploymentWorkflowMetrics(configured int, executions []workflowmodel.WorkflowExecution, now time.Time) map[string]any {
	counts := map[string]int{"pending": 0, "running": 0, "success": 0, "failed": 0, "skipped": 0, "dead_letter": 0, "resolved": 0, "duplicate": 0}
	retryCount, queueDepth, oldestPendingAge := 0, 0, 0
	var lastExecution time.Time
	duration := newDeploymentDurationAccumulator()
	for _, execution := range executions {
		status := strings.TrimSpace(execution.Status)
		if status == "" {
			status = "unknown"
		}
		counts[status]++
		if execution.Attempt > 1 || strings.HasPrefix(strings.TrimSpace(execution.Trigger), "retry:") {
			retryCount++
		}
		queued := status == "pending" || status == "running" || deploymentWorkflowRetryScheduled(execution)
		if queued {
			queueDepth++
		}
		createdAt, createdOK := deploymentParseMetricTime(execution.CreatedAt)
		updatedAt, updatedOK := deploymentParseMetricTime(execution.UpdatedAt)
		if createdOK && queued {
			if age := int(now.UTC().Sub(createdAt).Seconds()); age > oldestPendingAge {
				oldestPendingAge = age
			}
		}
		if createdOK && updatedOK && !updatedAt.Before(createdAt) {
			duration.add(int(updatedAt.Sub(createdAt).Milliseconds()))
		}
		if updatedOK && updatedAt.After(lastExecution) {
			lastExecution = updatedAt
		} else if createdOK && createdAt.After(lastExecution) {
			lastExecution = createdAt
		}
	}
	lastExecutionText := ""
	if !lastExecution.IsZero() {
		lastExecutionText = lastExecution.Format(time.RFC3339)
	}
	return map[string]any{
		"configured": configured, "status_counts": counts, "queue_depth": queueDepth,
		"failed": counts["failed"], "dead_letter": counts["dead_letter"], "resolved": counts["resolved"],
		"active_failure_count": counts["failed"] + counts["dead_letter"], "retry_count": retryCount,
		"oldest_pending_age_seconds": oldestPendingAge, "last_execution_at": lastExecutionText,
		"last_worker_run_at": lastExecutionText, "duration_ms": duration.metrics(),
	}
}

// DeploymentBusinessActionMetrics projects delivery invocation snapshots into
// action execution metrics without reading infrastructure state.
func DeploymentBusinessActionMetrics(configured int, invocations []integrationmodel.IntegrationInvocation, limit int) map[string]any {
	statusCounts, failureCounts := map[string]int{}, map[string]int{}
	actionCounts, invocationCounts, connectorCounts := map[string]int{}, map[string]int{}, map[string]int{}
	duration := newDeploymentDurationAccumulator()
	for _, invocation := range invocations {
		actionKey := deploymentMetadataString(invocation.Metadata, "action_key")
		invocationKey := deploymentMetadataString(invocation.Metadata, "invocation_key")
		if actionKey == "" && invocationKey == "" {
			continue
		}
		status := strings.TrimSpace(invocation.Status)
		if status == "" {
			status = "unknown"
		}
		statusCounts[status]++
		if actionKey != "" {
			actionCounts[actionKey]++
		}
		if invocationKey != "" {
			invocationCounts[invocationKey]++
		}
		if connectorKey := strings.TrimSpace(invocation.ConnectorKey); connectorKey != "" {
			connectorCounts[connectorKey]++
		}
		duration.add(int(invocation.DurationMS))
		if status == "failed" {
			key := strings.TrimSpace(invocation.Error)
			if key == "" {
				key = "unknown"
			}
			failureCounts[key]++
		}
	}
	return map[string]any{
		"configured": configured, "window_limit": limit, "recent_invocations": duration.count,
		"status_counts": statusCounts, "failure_counts": failureCounts, "actions": actionCounts,
		"invocations": invocationCounts, "connectors": connectorCounts, "invocation_duration_ms": duration.metrics(),
		"connector_latency_ms": duration.metrics(),
	}
}

func DeploymentBusinessActionConfiguredCount(actions []definitionmodel.ActionSchema) int {
	count := 0
	for _, action := range actions {
		if deploymentBusinessActionKind(action.Kind) {
			count++
		}
	}
	return count
}

func DeploymentAuditMetrics(events []auditmodel.AuditEvent, limit int) map[string]any {
	eventCounts, objectCounts, roleCounts := map[string]int{}, map[string]int{}, map[string]int{}
	var lastEvent time.Time
	for _, event := range events {
		eventKey := strings.TrimSpace(event.Event)
		if eventKey == "" {
			eventKey = "unknown"
		}
		eventCounts[eventKey]++
		if objectKey := strings.TrimSpace(event.ObjectKey); objectKey != "" {
			objectCounts[objectKey]++
		}
		if roleKey := strings.TrimSpace(event.RoleKey); roleKey != "" {
			roleCounts[roleKey]++
		}
		if createdAt, ok := deploymentParseMetricTime(event.CreatedAt); ok && createdAt.After(lastEvent) {
			lastEvent = createdAt
		}
	}
	lastEventText := ""
	if !lastEvent.IsZero() {
		lastEventText = lastEvent.Format(time.RFC3339)
	}
	return map[string]any{"window_limit": limit, "total_recent": len(events), "last_event_at": lastEventText, "events": eventCounts, "objects": objectCounts, "roles": roleCounts}
}

type deploymentDurationAccumulator struct{ count, total, max, le100, le1s, gt1s int }

func newDeploymentDurationAccumulator() *deploymentDurationAccumulator {
	return &deploymentDurationAccumulator{}
}
func (d *deploymentDurationAccumulator) add(value int) {
	if value < 0 {
		value = 0
	}
	d.count++
	d.total += value
	if value > d.max {
		d.max = value
	}
	switch {
	case value <= 100:
		d.le100++
	case value <= 1000:
		d.le1s++
	default:
		d.gt1s++
	}
}
func (d *deploymentDurationAccumulator) metrics() map[string]any {
	avg := 0
	if d.count > 0 {
		avg = d.total / d.count
	}
	return map[string]any{"count": d.count, "total": d.total, "avg": avg, "max": d.max, "le_100": d.le100, "le_1000": d.le1s, "gt_1000": d.gt1s}
}

func deploymentParseMetricTime(value string) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	return parsed.UTC(), err == nil
}
func deploymentWorkflowRetryScheduled(execution workflowmodel.WorkflowExecution) bool {
	status := strings.TrimSpace(execution.Status)
	if status != "failed" && status != "pending" {
		return false
	}
	if execution.MaxAttempts > 0 && execution.Attempt >= execution.MaxAttempts {
		return false
	}
	return strings.TrimSpace(execution.NextRunAt) != ""
}
func deploymentMetadataString(metadata map[string]any, key string) string {
	value := strings.TrimSpace(fmt.Sprint(metadata[key]))
	if value == "<nil>" {
		return ""
	}
	return value
}
func deploymentBusinessActionKind(kind string) bool {
	kind = strings.TrimSpace(kind)
	for _, allowed := range definitionmodel.ActionKindValues() {
		if kind == allowed {
			return true
		}
	}
	return false
}
func deploymentMapSlice(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return typed
	case []any:
		result := []map[string]any{}
		for _, item := range typed {
			if mapped, ok := item.(map[string]any); ok && len(mapped) > 0 {
				result = append(result, mapped)
			}
		}
		return result
	default:
		return nil
	}
}
