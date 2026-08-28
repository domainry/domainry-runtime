package scheduler

import (
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/mutation"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	schedulerpolicy "github.com/domainry/domainry-runtime/runtime/domain/scheduler/policy"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

func (s *SchedulerApplicationService) checkpointRun(ctx context.Context, workspaceID string, run recordmodel.Record, page workflowmodel.WorkflowScheduledPage, now time.Time) error {
	runObject, err := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "job_run")
	if err != nil {
		return err
	}
	expectedStatus := existingStringBefore(run, "status")
	expectedOwner := existingStringBefore(run, "lease_owner")
	expectedToken := schedulerpolicy.SchedulerInt(run.Data["fencing_token"], 0)
	processed := schedulerpolicy.SchedulerInt(run.Data["checkpoint_processed"], 0) + page.Processed
	run.Data["status"] = "queued"
	run.Data["lease_owner"], run.Data["lease_expires_at"] = "", ""
	run.Data["checkpoint_cursor"] = page.Checkpoint
	run.Data["checkpoint_processed"] = processed
	run.Data["result_json"] = fmt.Sprintf(`{"status":"checkpointed","workflow_execution_count":%d}`, processed)
	run.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	eventObject, err := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "job_run_event")
	if err != nil {
		return err
	}
	event := schedulerRunEventRecord(run.ID, "checkpoint_saved", "Scheduler saved a bounded workflow traversal checkpoint.", now, map[string]any{
		"checkpoint_processed": processed, "page_processed": page.Processed, "page_scanned": page.Scanned,
	})
	err = s.repository.CommitRecordMutationBatch(ctx, workspaceID, []transactionmodel.RecordMutationCommit{
		{Operation: "update", Object: runObject, Record: run, Conditions: map[string]any{"status": expectedStatus, "lease_owner": expectedOwner, "fencing_token": expectedToken}},
		{Operation: "create", Object: eventObject, Record: event},
	})
	if err != nil {
		if mutation.IsMutationConflict(err, mutation.MutationConflictOptimistic) {
			return mutation.MutationConflict("job_run", run.ID, mutation.MutationConflictLeaseLost, err)
		}
		return internalError("commit scheduler traversal checkpoint", err)
	}
	workerplatform.ObserveOutcome("scheduler", "checkpointed")
	return nil
}

func (s *SchedulerApplicationService) finishRun(ctx context.Context, workspaceID string, run recordmodel.Record, executions []workflowmodel.WorkflowExecution, evidence []schedulerBusinessEvidence, processErr error, startedAt time.Time) error {
	now := s.worker.Clock.Now()
	runObject, err := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "job_run")
	if err != nil {
		return err
	}
	expectedLeaseOwner := strings.TrimSpace(fmt.Sprint(run.Data["lease_owner"]))
	expectedFencingToken := schedulerpolicy.SchedulerInt(run.Data["fencing_token"], 0)
	expectedStatus := strings.TrimSpace(fmt.Sprint(run.Data["status"]))
	run.Data["started_at"] = startedAt.Format(time.RFC3339)
	run.Data["finished_at"] = now.Format(time.RFC3339)
	run.Data["lease_owner"] = ""
	run.Data["lease_expires_at"] = ""
	status := "succeeded"
	message := "Scheduler job run completed."
	timeoutSeconds := schedulerpolicy.SchedulerInt(run.Data["timeout_seconds"], 0)
	timedOut := timeoutSeconds > 0 && now.Sub(startedAt) > time.Duration(timeoutSeconds)*time.Second
	if processErr != nil {
		status = "failed"
		message = processErr.Error()
		run.Data["error_message"] = message
		run.Data["error_category"] = schedulerErrorCategory(processErr)
		run.Data["recoverability"] = "retryable"
	} else if timedOut {
		status = "failed"
		message = "backend.scheduler.timeout_exceeded"
		run.Data["error_message"] = message
		run.Data["error_category"] = "timeout"
		run.Data["recoverability"] = "retryable"
	} else if schedulerExecutionsFailed(executions) {
		status = "failed"
		message = "One or more workflow executions failed."
		run.Data["error_message"] = message
		run.Data["error_category"] = "workflow_failure"
		run.Data["recoverability"] = "retryable"
	}
	if status == "failed" {
		attempt := schedulerpolicy.SchedulerInt(run.Data["attempt"], 1)
		maxAttempts := schedulerpolicy.SchedulerInt(run.Data["max_attempts"], 3)
		if maxAttempts <= 0 || attempt < maxAttempts {
			status = "retrying"
			nextRetryAt := schedulerNextRetryAt(run, attempt, now)
			run.Data["next_retry_at"] = nextRetryAt.Format(time.RFC3339)
			run.Data["retry_backoff_seconds"] = int(nextRetryAt.Sub(now).Seconds())
		} else {
			status = "dead_letter"
			run.Data["next_retry_at"] = ""
			run.Data["retry_backoff_seconds"] = 0
			run.Data["recoverability"] = "manual_review"
		}
	} else {
		run.Data["next_retry_at"] = ""
		run.Data["retry_backoff_seconds"] = 0
		run.Data["error_category"] = ""
		run.Data["recoverability"] = ""
	}
	run.Data["status"] = status
	run.Data["workflow_execution_id"] = schedulerFirstExecutionID(executions)
	totalExecutions := schedulerpolicy.SchedulerInt(run.Data["checkpoint_processed"], 0) + len(executions)
	run.Data["checkpoint_cursor"] = ""
	run.Data["checkpoint_processed"] = totalExecutions
	run.Data["result_json"] = schedulerRunResultJSONCount(totalExecutions, len(evidence), status)
	run.UpdatedAt = now.Format(time.RFC3339)
	err = s.commitFinishedRun(ctx, workspaceID, runObject, run, executions, evidence, status, message, now, map[string]any{"status": expectedStatus, "lease_owner": expectedLeaseOwner, "fencing_token": expectedFencingToken})
	if err != nil {
		if mutation.IsMutationConflict(err, mutation.MutationConflictLeaseLost) {
			workerplatform.ObserveOutcome("scheduler", "lease_lost")
		}
		return err
	}
	outcome := status
	if status == "succeeded" {
		outcome = "completed"
	} else if status == "retrying" {
		outcome = "retry"
	}
	workerplatform.ObserveOutcome("scheduler", outcome)
	return nil
}

func (s *SchedulerApplicationService) FinishRun(ctx context.Context, run recordmodel.Record, executions []workflowmodel.WorkflowExecution, processErr error, startedAt time.Time, scope principalmodel.SystemScope) error {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return schedulerError(apperror.KindForbidden, "backend.system_scope_required", err)
	}
	return s.finishRun(ctx, principalmodel.InstallationWorkspaceID, run, executions, nil, processErr, startedAt)
}

func (s *SchedulerApplicationService) appendStateEvent(ctx context.Context, run recordmodel.Record, executions []workflowmodel.WorkflowExecution, status string, message string, now time.Time) error {
	eventType := "state_changed"
	if status == "retrying" {
		eventType = "retry_scheduled"
	} else if status == "dead_letter" {
		eventType = "dead_lettered"
	}
	return s.appendRunEvent(ctx, principalmodel.InstallationWorkspaceID, run.ID, eventType, message, now, map[string]any{
		"status":                status,
		"workflow_execution_id": schedulerFirstExecutionID(executions),
		"next_retry_at":         existingStringBefore(run, "next_retry_at"),
		"retry_backoff_seconds": schedulerpolicy.SchedulerInt(run.Data["retry_backoff_seconds"], 0),
		"error_category":        existingStringBefore(run, "error_category"),
		"recoverability":        existingStringBefore(run, "recoverability"),
	})
}

func (s *SchedulerApplicationService) appendWorkflowEvents(ctx context.Context, run recordmodel.Record, executions []workflowmodel.WorkflowExecution, now time.Time) error {
	for _, execution := range executions {
		if strings.TrimSpace(execution.ID) == "" || execution.Status == "duplicate" {
			continue
		}
		if err := s.appendRunEvent(ctx, principalmodel.InstallationWorkspaceID, run.ID, "workflow_triggered", "Scheduler triggered workflow execution.", now, map[string]any{
			"workflow_execution_id": execution.ID,
			"workflow_key":          execution.WorkflowKey,
			"workflow_status":       execution.Status,
			"object_key":            execution.ObjectKey,
			"record_id":             execution.RecordID,
		}); err != nil {
			return err
		}
		if strings.TrimSpace(execution.ActionType) == "" {
			continue
		}
		if err := s.appendRunEvent(ctx, principalmodel.InstallationWorkspaceID, run.ID, "action_triggered", "Scheduler workflow triggered action.", now, map[string]any{
			"workflow_execution_id": execution.ID,
			"workflow_key":          execution.WorkflowKey,
			"action_type":           execution.ActionType,
			"object_key":            execution.ObjectKey,
			"record_id":             execution.RecordID,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *SchedulerApplicationService) appendBusinessEvidenceEvents(ctx context.Context, run recordmodel.Record, evidence []schedulerBusinessEvidence, now time.Time) error {
	for _, item := range evidence {
		eventType := strings.TrimSpace(item.Kind)
		if eventType == "" {
			eventType = "business_evidence_created"
		}
		if err := s.appendRunEvent(ctx, principalmodel.InstallationWorkspaceID, run.ID, eventType, "Scheduler created domain evidence.", now, map[string]any{
			"evidence_kind": eventType,
			"object_key":    item.ObjectKey,
			"record_id":     item.RecordID,
		}); err != nil {
			return err
		}
	}
	return nil
}

func schedulerErrorCategory(err error) string {
	if err == nil {
		return ""
	}
	var appErr *apperror.AppError
	if errors.As(err, &appErr) && strings.TrimSpace(appErr.Code) != "" {
		code := appErr.Code
		if strings.Contains(code, "timeout") {
			return "timeout"
		}
		if strings.Contains(code, "permission") || strings.Contains(code, "forbidden") {
			return "permission"
		}
		if strings.Contains(code, "validation") || strings.Contains(code, "bad_request") {
			return "validation"
		}
		return code
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "timeout") || strings.Contains(message, "deadline exceeded") {
		return "timeout"
	}
	return "runtime_error"
}

func (s *SchedulerApplicationService) commitFinishedRun(ctx context.Context, workspaceID string, runObject definitionmodel.ObjectSchema, run recordmodel.Record, executions []workflowmodel.WorkflowExecution, evidence []schedulerBusinessEvidence, status, message string, now time.Time, conditions map[string]any) error {
	commits := []transactionmodel.RecordMutationCommit{{Operation: "update", Object: runObject, Record: run, Conditions: conditions}}
	if status == "dead_letter" {
		deadLetterObject, err := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "job_dead_letter")
		if err != nil {
			return err
		}
		deadLetterID := "jobdl_" + schedulerpolicy.SchedulerSlug(run.ID)
		if _, found, err := s.repository.GetRecord(ctx, workspaceID, deadLetterObject, deadLetterID); err != nil {
			return internalError("get scheduler dead letter", err)
		} else if !found {
			commits = append(commits, transactionmodel.RecordMutationCommit{Operation: "create", Object: deadLetterObject, Record: schedulerDeadLetterRecord(run, deadLetterID, message, now)})
		}
	}
	definitionCommits, err := s.prepareDefinitionCursorCommits(ctx, workspaceID, run, status, now)
	if err != nil {
		return err
	}
	commits = append(commits, definitionCommits...)
	if event, ok, err := s.compileSchedulerMissedDeadlineNotification(ctx, workspaceID, run, status, now); err != nil {
		return err
	} else if ok {
		commits[0].NotificationEvents = append(commits[0].NotificationEvents, event)
	}
	if event, ok, err := s.compileSchedulerRunNotification(ctx, workspaceID, run, status, now); err != nil {
		return err
	} else if ok {
		commits[0].NotificationEvents = append(commits[0].NotificationEvents, event)
	}
	eventObject, err := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "job_run_event")
	if err != nil {
		return err
	}
	for index, event := range schedulerFinishEvents(run, executions, evidence, status, message, now) {
		event.ID = fmt.Sprintf("jobevt_%s_%d_%d", schedulerpolicy.SchedulerSlug(run.ID+"_"+fmt.Sprint(event.Data["event_type"])), now.UnixNano(), index)
		commits = append(commits, transactionmodel.RecordMutationCommit{Operation: "create", Object: eventObject, Record: event})
	}
	if err := s.repository.CommitRecordMutationBatch(ctx, workspaceID, commits); err != nil {
		if mutation.IsMutationConflict(err, mutation.MutationConflictOptimistic) {
			return mutation.MutationConflict("job_run", run.ID, mutation.MutationConflictLeaseLost, err)
		}
		return internalError("commit scheduler run state", err)
	}
	return nil
}
func (s *SchedulerApplicationService) prepareDefinitionCursorCommits(ctx context.Context, workspaceID string, run recordmodel.Record, status string, now time.Time) ([]transactionmodel.RecordMutationCommit, error) {
	if schedulerRunDoesNotAdvanceCursor(run, status) {
		return nil, nil
	}
	definitionID := strings.TrimSpace(fmt.Sprint(run.Data["scheduler_definition_key"]))
	if definitionID == "" {
		return nil, nil
	}
	object, err := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "scheduler_cursor")
	if err != nil {
		return nil, err
	}
	if s.definitions == nil {
		return nil, schedulerError(apperror.KindUnavailable, "backend.scheduler.definition_source_unavailable", nil)
	}
	definition, found, err := s.definitions.GetSchedulerDefinition(ctx, definitionID)
	if err != nil {
		return nil, internalError("get scheduler job definition", err)
	}
	if !found {
		return nil, nil
	}
	cursor, cursorFound, err := s.repository.GetRecord(ctx, workspaceID, object, definitionID)
	if err != nil {
		return nil, internalError("get scheduler cursor", err)
	}
	previousUpdatedAt := cursor.UpdatedAt
	previousNextRunAt := strings.TrimSpace(fmt.Sprint(cursor.Data["next_run_at"]))
	if !cursorFound {
		previousNextRunAt = strings.TrimSpace(fmt.Sprint(definition.Data["next_run_at"]))
		cursor = recordmodel.Record{ID: definitionID, CreatedAt: now.Format(time.RFC3339), Data: map[string]any{"scheduler_definition_key": definitionID}}
	}
	definition.Data["next_run_at"] = previousNextRunAt
	cursorAnchor := schedulerDefinitionCursorAnchor(definition, run, now)
	nextRunAt := schedulerNextRunAt(definition, cursorAnchor)
	cursor.Data["last_run_at"], cursor.Data["last_run_status"] = now.Format(time.RFC3339), status
	cursor.Data["last_run_missed_deadline"] = schedulerRunMissedDeadline(definition, run)
	cursor.Data["next_run_at"], cursor.UpdatedAt = nextRunAt.Format(time.RFC3339), now.Format(time.RFC3339)
	event := schedulerRunEventRecord(run.ID, "definition_cursor_advanced", "Scheduler definition cursor advanced.", now, map[string]any{
		"scheduler_definition_key": definition.ID, "status": status, "previous_next_run_at": previousNextRunAt, "next_run_at": nextRunAt.Format(time.RFC3339),
	})
	eventObject, err := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "job_run_event")
	if err != nil {
		return nil, err
	}
	event.ID = fmt.Sprintf("jobevt_%s_%d_cursor", schedulerpolicy.SchedulerSlug(run.ID), now.UnixNano())
	operation := "create"
	optimistic := transactionmodel.OptimisticPrecondition{}
	if cursorFound {
		operation = "update"
		optimistic.ExpectedUpdatedAt = previousUpdatedAt
	}
	return []transactionmodel.RecordMutationCommit{
		{Operation: operation, Object: object, Record: cursor, Optimistic: optimistic},
		{Operation: "create", Object: eventObject, Record: event},
	}, nil
}

func schedulerDeadLetterRecord(run recordmodel.Record, id, reason string, now time.Time) recordmodel.Record {
	return recordmodel.Record{ID: id, CreatedAt: now.Format(time.RFC3339), UpdatedAt: now.Format(time.RFC3339), Data: map[string]any{
		"job_run_id": run.ID, "scheduler_definition_key": strings.TrimSpace(fmt.Sprint(run.Data["scheduler_definition_key"])), "status": "open",
		"reason": valueOrDefault(strings.TrimSpace(reason), "backend.scheduler.dead_letter"), "last_error": strings.TrimSpace(fmt.Sprint(run.Data["error_message"])),
		"failed_at": now.Format(time.RFC3339), "resolved_at": "", "resolved_by": "", "resolution_note": "",
	}}
}

func schedulerFinishEvents(run recordmodel.Record, executions []workflowmodel.WorkflowExecution, evidence []schedulerBusinessEvidence, status, message string, now time.Time) []recordmodel.Record {
	eventType := "state_changed"
	if status == "retrying" {
		eventType = "retry_scheduled"
	} else if status == "dead_letter" {
		eventType = "dead_lettered"
	}
	events := []recordmodel.Record{schedulerRunEventRecord(run.ID, eventType, message, now, map[string]any{
		"status": status, "workflow_execution_id": schedulerFirstExecutionID(executions), "next_retry_at": existingStringBefore(run, "next_retry_at"),
		"retry_backoff_seconds": schedulerpolicy.SchedulerInt(run.Data["retry_backoff_seconds"], 0), "error_category": existingStringBefore(run, "error_category"), "recoverability": existingStringBefore(run, "recoverability"),
	})}
	for _, execution := range executions {
		if strings.TrimSpace(execution.ID) == "" || execution.Status == "duplicate" {
			continue
		}
		events = append(events, schedulerRunEventRecord(run.ID, "workflow_triggered", "Scheduler triggered workflow execution.", now, map[string]any{"workflow_execution_id": execution.ID, "workflow_key": execution.WorkflowKey, "workflow_status": execution.Status, "object_key": execution.ObjectKey, "record_id": execution.RecordID}))
		if strings.TrimSpace(execution.ActionType) != "" {
			events = append(events, schedulerRunEventRecord(run.ID, "action_triggered", "Scheduler workflow triggered action.", now, map[string]any{"workflow_execution_id": execution.ID, "workflow_key": execution.WorkflowKey, "action_type": execution.ActionType, "object_key": execution.ObjectKey, "record_id": execution.RecordID}))
		}
	}
	for _, item := range evidence {
		kind := valueOrDefault(strings.TrimSpace(item.Kind), "business_evidence_created")
		events = append(events, schedulerRunEventRecord(run.ID, kind, "Scheduler created domain evidence.", now, map[string]any{"evidence_kind": kind, "object_key": item.ObjectKey, "record_id": item.RecordID}))
	}
	return events
}
