package scheduler

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	schedulerpolicy "github.com/domainry/domainry-runtime/runtime/domain/scheduler/policy"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"

	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func (s *SchedulerApplicationService) compileSchedulerRunNotification(ctx context.Context, workspaceID string, run recordmodel.Record, status string, now time.Time) (notificationmodel.NotificationEvent, bool, error) {
	if s.compileNotification == nil || s.definitions == nil {
		return notificationmodel.NotificationEvent{}, false, nil
	}
	definitionID := strings.TrimSpace(fmt.Sprint(run.Data["scheduler_definition_key"]))
	definition, found, err := s.definitions.GetSchedulerDefinition(ctx, definitionID)
	if err != nil {
		return notificationmodel.NotificationEvent{}, false, internalError("get scheduler notification definition", err)
	}
	if !found {
		return notificationmodel.NotificationEvent{}, false, nil
	}
	recipients := schedulerNotificationRecipients(definition)
	if len(recipients) == 0 {
		return notificationmodel.NotificationEvent{}, false, nil
	}
	eventType, severity, actionState, alertState := "", "warning", "", notificationmodel.NotificationAlertFiring
	switch status {
	case "retrying":
		eventType = "scheduler.job.failed"
		if schedulerpolicy.SchedulerInt(run.Data["attempt"], 1) >= 3 {
			eventType = "scheduler.job.repeated_failure"
		}
	case "dead_letter":
		eventType, severity = "scheduler.job.repeated_failure", "critical"
	case "succeeded":
		missedDeadline := schedulerRunMissedDeadline(definition, run)
		cursorObject, objectErr := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "scheduler_cursor")
		if objectErr != nil {
			return notificationmodel.NotificationEvent{}, false, objectErr
		}
		cursor, cursorFound, cursorErr := s.repository.GetRecord(ctx, workspaceID, cursorObject, definitionID)
		if cursorErr != nil {
			return notificationmodel.NotificationEvent{}, false, internalError("get scheduler notification cursor", cursorErr)
		}
		previousAlert := cursorFound && (schedulerFailureStatus(strings.TrimSpace(fmt.Sprint(cursor.Data["last_run_status"]))) || strings.EqualFold(strings.TrimSpace(fmt.Sprint(cursor.Data["last_run_missed_deadline"])), "true"))
		if missedDeadline || !previousAlert {
			return notificationmodel.NotificationEvent{}, false, nil
		}
		eventType, severity, actionState, alertState = "scheduler.job.recovered", "info", notificationmodel.NotificationActionCompleted, notificationmodel.NotificationAlertResolved
	default:
		return notificationmodel.NotificationEvent{}, false, nil
	}
	name := valueOrDefault(strings.TrimSpace(fmt.Sprint(definition.Data["name"])), definitionID)
	event, err := s.compileNotification(notificationmodel.NotificationIntent{
		ID: "notification_scheduler_" + schedulerpolicy.SchedulerSlug(run.ID+"_"+status), WorkspaceID: workspaceID,
		SourceEventID: run.ID + ":" + status + ":" + run.UpdatedAt, EventType: eventType, Severity: severity, Surface: "business_workspace",
		RecipientUserIDs: recipients, SubjectType: "scheduler_job", SubjectID: definitionID, SubjectVersion: definition.UpdatedAt,
		GroupKey: "scheduler_job:" + definitionID, ActionState: actionState, AlertState: alertState, OccurredAt: now.Format(time.RFC3339Nano),
		Variables: map[string]any{"job_name": name, "scheduled_for": valueOrDefault(existingStringBefore(run, "scheduled_for"), "not_available"), "status": status, "error_code": valueOrDefault(existingStringBefore(run, "error_category"), "backend.scheduler.run_failed"), "occurrence_count": schedulerpolicy.SchedulerInt(run.Data["attempt"], 1)},
	})
	return event, err == nil, err
}

func (s *SchedulerApplicationService) compileSchedulerMissedDeadlineNotification(ctx context.Context, workspaceID string, run recordmodel.Record, status string, now time.Time) (notificationmodel.NotificationEvent, bool, error) {
	if status != "succeeded" || s.compileNotification == nil || s.definitions == nil {
		return notificationmodel.NotificationEvent{}, false, nil
	}
	definitionID := strings.TrimSpace(fmt.Sprint(run.Data["scheduler_definition_key"]))
	definition, found, err := s.definitions.GetSchedulerDefinition(ctx, definitionID)
	if err != nil {
		return notificationmodel.NotificationEvent{}, false, internalError("get scheduler missed-deadline definition", err)
	}
	if !found || !schedulerRunMissedDeadline(definition, run) {
		return notificationmodel.NotificationEvent{}, false, nil
	}
	recipients := schedulerNotificationRecipients(definition)
	if len(recipients) == 0 {
		return notificationmodel.NotificationEvent{}, false, nil
	}
	scheduledFor := existingStringBefore(run, "scheduled_for")
	event, err := s.compileNotification(notificationmodel.NotificationIntent{
		ID: "notification_scheduler_" + schedulerpolicy.SchedulerSlug(run.ID+"_missed_deadline"), WorkspaceID: workspaceID,
		SourceEventID: run.ID + ":missed_deadline:" + scheduledFor, EventType: "scheduler.job.missed_deadline", Severity: "warning", Surface: "business_workspace",
		RecipientUserIDs: recipients, SubjectType: "scheduler_job", SubjectID: definitionID, SubjectVersion: definition.UpdatedAt,
		GroupKey: "scheduler_job:" + definitionID, AlertState: notificationmodel.NotificationAlertFiring, OccurredAt: now.Format(time.RFC3339Nano),
		Variables: map[string]any{"job_name": valueOrDefault(strings.TrimSpace(fmt.Sprint(definition.Data["name"])), definitionID), "scheduled_for": scheduledFor, "status": "missed_deadline", "error_code": "backend.scheduler.missed_deadline", "occurrence_count": 1},
	})
	return event, err == nil, err
}

func schedulerRunMissedDeadline(definition, run recordmodel.Record) bool {
	if strings.TrimSpace(fmt.Sprint(run.Data["triggered_by"])) == "manual_run" {
		return false
	}
	scheduledFor, scheduledErr := time.Parse(time.RFC3339, existingStringBefore(run, "scheduled_for"))
	startedAt, startedErr := time.Parse(time.RFC3339, existingStringBefore(run, "started_at"))
	if scheduledErr != nil || startedErr != nil || startedAt.Before(scheduledFor) {
		return false
	}
	return !startedAt.Before(schedulerNextRunAt(definition, scheduledFor))
}

func schedulerFailureStatus(status string) bool {
	return status == "failed" || status == "retrying" || status == "dead_letter"
}

func schedulerNotificationRecipients(definition recordmodel.Record) []string {
	values := []string{}
	for _, key := range []string{"notification_recipient_user_ids", "owner_user_ids"} {
		switch raw := definition.Data[key].(type) {
		case []string:
			values = append(values, raw...)
		case []any:
			for _, value := range raw {
				values = append(values, fmt.Sprint(value))
			}
		}
	}
	for _, key := range []string{"owner_user_id", "owner", "created_by"} {
		value := strings.TrimSpace(fmt.Sprint(definition.Data[key]))
		if value != "" && value != "<nil>" {
			values = append(values, value)
		}
	}
	sort.Strings(values)
	result := values[:0]
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" && (len(result) == 0 || result[len(result)-1] != value) {
			result = append(result, value)
		}
	}
	return result
}

func (s *SchedulerApplicationService) Status(ctx context.Context, scope principalmodel.SystemScope) (map[string]any, error) {
	if _, err := principalmodel.NewSystemQueryScope(scope); err != nil {
		return nil, schedulerError(apperror.KindForbidden, "backend.system_scope_required", err)
	}
	s.configMu.RLock()
	cfg := NormalizeWorkerConfig(s.config)
	s.configMu.RUnlock()
	available := s.RuntimeAvailable(ctx, workflowWorkerPrincipal())
	status := map[string]any{
		"enabled": cfg.Enabled, "runtime_available": available,
		"poll_interval_ms": int(cfg.PollInterval / time.Millisecond), "batch_size": cfg.BatchSize,
		"lease_ttl_ms": int(cfg.LeaseTTL / time.Millisecond), "max_catchup_windows": cfg.MaxCatchupWindows,
	}
	if !available {
		return status, nil
	}
	now := s.worker.Clock.Now()
	objects := schemaObjectMap(s.schema.SchemaForPrincipal(ctx, principalmodel.Principal{}).Objects)
	{
		definitions, err := s.definitions.ListSchedulerDefinitions(ctx)
		if err != nil {
			status["definition_error"] = err.Error()
		} else {
			due, enabled := 0, 0
			for _, definition := range definitions {
				if strings.TrimSpace(fmt.Sprint(definition.Data["status"])) == "enabled" {
					enabled++
					if schedulerDefinitionDue(definition, now) {
						due++
					}
				}
			}
			status["definitions"], status["enabled_definitions"], status["due_definitions"] = len(definitions), enabled, due
		}
	}
	{
		object := objects["job_run"]
		runs, err := s.repository.ListRecords(ctx, principalmodel.InstallationWorkspaceID, object, recordmodel.RecordListQuery{Page: 1, PageSize: 500})
		if err != nil {
			status["run_error"] = err.Error()
		} else {
			statusCounts := map[string]int{}
			leaseExpired := 0
			duration := newSchedulerDurationAccumulator()
			for _, run := range runs.Items {
				runStatus := strings.TrimSpace(fmt.Sprint(run.Data["status"]))
				if runStatus == "" || runStatus == "<nil>" {
					runStatus = "unknown"
				}
				statusCounts[runStatus]++
				if runStatus == "leased" && schedulerpolicy.SchedulerLeaseExpired(run, now) {
					leaseExpired++
				}
				startedAt, startedOK := parseSchedulerMetricTime(strings.TrimSpace(fmt.Sprint(run.Data["started_at"])))
				finishedAt, finishedOK := parseSchedulerMetricTime(strings.TrimSpace(fmt.Sprint(run.Data["finished_at"])))
				if startedOK && finishedOK && !finishedAt.Before(startedAt) {
					duration.add(int(finishedAt.Sub(startedAt).Milliseconds()))
				}
			}
			status["runs"], status["status_counts"] = runs.Total, statusCounts
			status["claimed_runs"] = statusCounts["leased"] + statusCounts["running"]
			status["succeeded_runs"], status["failed_runs"] = statusCounts["succeeded"], statusCounts["failed"]
			status["retrying_runs"], status["dead_letter_runs"] = statusCounts["retrying"], statusCounts["dead_letter"]
			durationMetrics := duration.metrics()
			status["lease_expirations"], status["run_duration_ms"], status["avg_run_duration_ms"] = leaseExpired, durationMetrics, durationMetrics["avg"]
		}
	}
	{
		object := objects["job_dead_letter"]
		deadLetters, err := s.repository.ListRecords(ctx, principalmodel.InstallationWorkspaceID, object, recordmodel.RecordListQuery{Page: 1, PageSize: 500})
		if err != nil {
			status["dead_letter_error"] = err.Error()
		} else {
			unresolved := 0
			for _, item := range deadLetters.Items {
				if strings.TrimSpace(fmt.Sprint(item.Data["status"])) != "resolved" {
					unresolved++
				}
			}
			status["dead_letters"], status["unresolved_dead_letters"] = deadLetters.Total, unresolved
		}
	}
	return status, nil
}

func schedulerOperationAllowed(principal principalmodel.Principal) error {
	if err := schedulerAuthorizeCommand(principal); err != nil {
		return err
	}
	if principal.HasPermission("workspace.admin") || principal.HasExactPermission("scheduler.command") || principal.HasExactPermission("job_run.update") {
		return nil
	}
	return forbidden("backend.scheduler.permission_required")
}

func schedulerAuthorizeQuery(principal principalmodel.Principal) error {
	if _, err := principalmodel.QueryScopeForPrincipal(principal); err != nil {
		return schedulerError(apperror.KindForbidden, "backend.workspace_scope_required", err)
	}
	return nil
}

func schedulerAuthorizeCommand(principal principalmodel.Principal) error {
	if _, err := principalmodel.CommandScopeForPrincipal(principal); err != nil {
		return schedulerError(apperror.KindForbidden, "backend.workspace_scope_required", err)
	}
	return nil
}

func (s *SchedulerApplicationService) objectForPrincipal(ctx context.Context, principal principalmodel.Principal, objectKey string) (definitionmodel.ObjectSchema, error) {
	for _, object := range s.schema.SchemaForPrincipal(ctx, principal).Objects {
		if object.Key == objectKey {
			return object, nil
		}
	}
	return definitionmodel.ObjectSchema{}, notFound("backend.scheduler.runtime_object_not_found", "object_key", objectKey)
}

func schemaObjectMap(objects []definitionmodel.ObjectSchema) map[string]definitionmodel.ObjectSchema {
	result := make(map[string]definitionmodel.ObjectSchema, len(objects))
	for _, object := range objects {
		result[object.Key] = object
	}
	return result
}

type schedulerDurationAccumulator struct{ count, total, max, le100, le1s, gt1s int }

func newSchedulerDurationAccumulator() *schedulerDurationAccumulator {
	return &schedulerDurationAccumulator{}
}

func (d *schedulerDurationAccumulator) add(value int) {
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

func (d *schedulerDurationAccumulator) metrics() map[string]any {
	avg := 0
	if d.count > 0 {
		avg = d.total / d.count
	}
	return map[string]any{"count": d.count, "total": d.total, "avg": avg, "max": d.max, "le_100": d.le100, "le_1000": d.le1s, "gt_1000": d.gt1s}
}

func parseSchedulerMetricTime(value string) (time.Time, bool) {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, false
	}
	return parsed.UTC(), true
}

func schedulerExecutionsFailed(executions []workflowmodel.WorkflowExecution) bool {
	for _, execution := range executions {
		if execution.Status == "failed" || execution.Status == "dead_letter" || execution.Status == "blocked" {
			return true
		}
	}
	return false
}

func schedulerFirstExecutionID(executions []workflowmodel.WorkflowExecution) string {
	for _, execution := range executions {
		if strings.TrimSpace(execution.ID) != "" {
			return execution.ID
		}
	}
	return ""
}

func schedulerRunResultJSON(executions []workflowmodel.WorkflowExecution, evidence []schedulerBusinessEvidence, status string) string {
	return schedulerRunResultJSONCount(len(executions), len(evidence), status)
}

func schedulerRunResultJSONCount(executionCount, evidenceCount int, status string) string {
	return fmt.Sprintf(`{"status":%q,"workflow_execution_count":%d,"business_evidence_count":%d}`, status, executionCount, evidenceCount)
}

func schedulerNextRetryAt(run recordmodel.Record, attempt int, now time.Time) time.Time {
	mode := strings.ToLower(strings.TrimSpace(fmt.Sprint(run.Data["retry_backoff"])))
	if mode == "" || mode == "<nil>" {
		mode = strings.ToLower(strings.TrimSpace(fmt.Sprint(run.Data["retry_backoff_mode"])))
	}
	if mode == "" || mode == "<nil>" {
		mode = "fixed"
	}
	baseSeconds := schedulerpolicy.SchedulerInt(run.Data["retry_delay_seconds"], 60)
	if baseSeconds <= 0 {
		baseSeconds = 60
	}
	maxSeconds := schedulerpolicy.SchedulerInt(run.Data["retry_max_delay_seconds"], 3600)
	if maxSeconds <= 0 {
		maxSeconds = 3600
	}
	delay := baseSeconds
	switch mode {
	case "exponential", "capped_exponential":
		exp := attempt - 1
		if exp < 0 {
			exp = 0
		}
		delay = baseSeconds
		for i := 0; i < exp; i++ {
			if delay >= maxSeconds {
				break
			}
			delay *= 2
		}
		if mode == "capped_exponential" && delay > maxSeconds {
			delay = maxSeconds
		}
	case "fixed":
	default:
		delay = baseSeconds
	}
	if delay > maxSeconds {
		delay = maxSeconds
	}
	return now.Add(time.Duration(delay) * time.Second)
}

func NextRetryAt(run recordmodel.Record, attempt int, now time.Time) time.Time {
	return schedulerNextRetryAt(run, attempt, now)
}
