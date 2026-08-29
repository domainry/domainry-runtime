package scheduler

import (
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	schedulerpolicy "github.com/domainry/domainry-runtime/runtime/domain/scheduler/policy"

	"context"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/mutation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func (s *SchedulerApplicationService) claimRun(ctx context.Context, workspaceID string, definition recordmodel.Record, triggerSource string, now time.Time) (recordmodel.Record, bool, error) {
	return s.claimRunWithKey(ctx, workspaceID, definition, triggerSource, "", now)
}

func (s *SchedulerApplicationService) readFinishedRun(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, runID string) (recordmodel.Record, error) {
	run, found, err := s.repository.GetRecord(ctx, workspaceID, object, runID)
	if err != nil {
		return recordmodel.Record{}, internalError("read finished scheduler run", err)
	}
	if !found {
		return recordmodel.Record{}, internalError("read finished scheduler run", fmt.Errorf("job_run %s missing after commit", runID))
	}
	return run, nil
}

func (s *SchedulerApplicationService) claimRunWithKey(ctx context.Context, workspaceID string, definition recordmodel.Record, triggerSource, callerKey string, now time.Time) (recordmodel.Record, bool, error) {
	leaseNow, err := s.authoritativeNow(ctx, now)
	if err != nil {
		return recordmodel.Record{}, false, err
	}
	runObject, err := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "job_run")
	if err != nil {
		return recordmodel.Record{}, false, err
	}
	definitionKey := strings.TrimSpace(fmt.Sprint(definition.Data["key"]))
	if definitionKey == "" {
		definitionKey = definition.ID
	}
	scheduledFor := schedulerScheduledRunTime(definition, triggerSource, now, s.maxCatchupWindows())
	if strings.TrimSpace(triggerSource) == "scheduler" && scheduledFor.After(leaseNow) {
		return recordmodel.Record{}, false, nil
	}
	runID := schedulerRunIDForDefinition(definition, triggerSource, scheduledFor)
	idempotencyScope := "scheduler.window"
	idempotencyKey := definitionKey + ":" + schedulerDefinitionWindowSuffix(definition, scheduledFor)
	if strings.TrimSpace(triggerSource) == "manual_run" {
		idempotencyScope = "scheduler.manual"
		idempotencyKey = schedulerManualIdempotencyKey(definition, callerKey)
		runID = schedulerManualRunID(definition, idempotencyKey)
	}
	leaseTTL := s.leaseTTL()
	if existing, ok, err := s.repository.GetRecord(ctx, workspaceID, runObject, runID); err != nil {
		return recordmodel.Record{}, false, internalError("get scheduler job run", err)
	} else if ok {
		status := strings.TrimSpace(fmt.Sprint(existing.Data["status"]))
		if status == "leased" && !schedulerpolicy.SchedulerLeaseExpired(existing, leaseNow) {
			return existing, false, nil
		}
		if status == "retrying" && !schedulerpolicy.SchedulerRetryDue(existing, leaseNow) {
			return existing, false, nil
		}
		if status != "queued" && status != "leased" && status != "failed" && status != "retrying" {
			return existing, false, nil
		}
		attempt := schedulerpolicy.SchedulerNextAttempt(existing, status)
		maxAttempts := schedulerpolicy.SchedulerInt(existing.Data["max_attempts"], schedulerpolicy.SchedulerInt(definition.Data["max_attempts"], 3))
		if maxAttempts > 0 && attempt > maxAttempts {
			if err := s.deadLetterRun(ctx, workspaceID, existing, definition, "backend.scheduler.max_attempts_reached", leaseNow); err != nil {
				return recordmodel.Record{}, false, err
			}
			existing.Data["status"] = "dead_letter"
			existing.Data["lease_owner"] = ""
			existing.Data["lease_expires_at"] = ""
			existing.Data["error_message"] = "backend.scheduler.max_attempts_reached"
			existing.UpdatedAt = leaseNow.Format(time.RFC3339)
			if err := s.updateRecord(ctx, workspaceID, runObject, existing, "dead-letter scheduler job run"); err != nil {
				return recordmodel.Record{}, false, err
			}
			return existing, false, nil
		}
		previousLeaseExpiresAt := existingStringBefore(existing, "lease_expires_at")
		previousFencingToken := schedulerpolicy.SchedulerInt(existing.Data["fencing_token"], 1)
		existing.Data["status"] = "leased"
		existing.Data["lease_owner"] = s.worker.WorkerID.String()
		existing.Data["lease_expires_at"] = leaseNow.Add(leaseTTL).Format(time.RFC3339)
		existing.Data["next_retry_at"] = ""
		existing.Data["attempt"] = attempt
		existing.Data["fencing_token"] = previousFencingToken + 1
		existing.UpdatedAt = leaseNow.Format(time.RFC3339)
		claimConditions := map[string]any{"status": status, "fencing_token": previousFencingToken}
		if previousLeaseExpiresAt != "" || status == "leased" {
			claimConditions["lease_expires_at"] = previousLeaseExpiresAt
		}
		claimed, err := s.updateRunIfCurrent(ctx, workspaceID, runObject, existing, claimConditions)
		if err != nil {
			return recordmodel.Record{}, false, err
		}
		if !claimed {
			return existing, false, nil
		}
		if err := s.appendRunEvent(ctx, workspaceID, existing.ID, "lease_acquired", "Scheduler retry lease acquired.", leaseNow, map[string]any{"attempt": attempt}); err != nil {
			return recordmodel.Record{}, false, err
		}
		return existing, true, nil
	}
	run := recordmodel.Record{
		ID:        runID,
		CreatedAt: leaseNow.Format(time.RFC3339),
		UpdatedAt: leaseNow.Format(time.RFC3339),
		Data: map[string]any{
			"scheduler_definition_key": definition.ID,
			"status":                   "leased",
			"triggered_by":             valueOrDefault(strings.TrimSpace(triggerSource), "scheduler"),
			"scheduled_for":            scheduledFor.Format(time.RFC3339),
			"lease_owner":              s.worker.WorkerID.String(),
			"lease_expires_at":         leaseNow.Add(leaseTTL).Format(time.RFC3339),
			"fencing_token":            1,
			"attempt":                  1,
			"max_attempts":             schedulerpolicy.SchedulerInt(definition.Data["max_attempts"], 3),
			"timeout_seconds":          schedulerpolicy.SchedulerInt(definition.Data["timeout_seconds"], 300),
			"retry_backoff":            valueOrDefault(strings.TrimSpace(fmt.Sprint(definition.Data["retry_backoff"])), "fixed"),
			"retry_delay_seconds":      schedulerpolicy.SchedulerInt(firstNonNil(definition.Data["retry_delay_seconds"], definition.Data["retry_interval_seconds"]), 60),
			"retry_max_delay_seconds":  schedulerpolicy.SchedulerInt(definition.Data["retry_max_delay_seconds"], 3600),
			"next_retry_at":            "",
			"idempotency_scope":        idempotencyScope,
			"idempotency_key":          idempotencyKey,
			"workflow_key":             strings.TrimSpace(fmt.Sprint(definition.Data["target_key"])),
			"target_object":            strings.TrimSpace(fmt.Sprint(definition.Data["target_object"])),
			"payload_json":             valueOrDefault(strings.TrimSpace(fmt.Sprint(definition.Data["payload_json"])), "{}"),
			"result_json":              "{}",
			"checkpoint_cursor":        "",
			"checkpoint_processed":     0,
			"error_message":            "",
		},
	}
	eventObject, err := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "job_run_event")
	if err != nil {
		return recordmodel.Record{}, false, err
	}
	created := schedulerRunEventRecord(run.ID, "created", "Scheduler job run created.", leaseNow, map[string]any{
		"scheduler_definition_key": definition.ID,
		"scheduled_for":            scheduledFor.Format(time.RFC3339),
		"missed_window_policy":     schedulerMissedWindowPolicy(definition),
	})
	leaseAcquired := schedulerRunEventRecord(run.ID, "lease_acquired", "Scheduler job lease acquired.", leaseNow, map[string]any{"attempt": 1})
	if err := s.repository.CommitRecordMutationBatch(ctx, workspaceID, []transactionmodel.RecordMutationCommit{
		{Operation: "create", Object: runObject, Record: run},
		{Operation: "create", Object: eventObject, Record: created},
		{Operation: "create", Object: eventObject, Record: leaseAcquired},
	}); err != nil {
		if existing, ok, getErr := s.repository.GetRecord(ctx, workspaceID, runObject, run.ID); getErr == nil && ok {
			return existing, false, nil
		}
		return recordmodel.Record{}, false, internalError("commit scheduler job run claim", err)
	}
	return run, true, nil
}

func (s *SchedulerApplicationService) ClaimRun(ctx context.Context, definition recordmodel.Record, triggerSource string, now time.Time, scope principalmodel.SystemScope) (recordmodel.Record, bool, error) {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return recordmodel.Record{}, false, schedulerError(apperror.KindForbidden, "backend.system_scope_required", err)
	}
	return s.claimRun(ctx, principalmodel.InstallationWorkspaceID, definition, triggerSource, now)
}

func (s *SchedulerApplicationService) heartbeatRun(ctx context.Context, workspaceID string, run recordmodel.Record, now time.Time) error {
	leaseNow, err := s.authoritativeNow(ctx, now)
	if err != nil {
		return err
	}
	owner := existingStringBefore(run, "lease_owner")
	fencingToken := schedulerpolicy.SchedulerInt(run.Data["fencing_token"], 0)
	if owner == "" || fencingToken <= 0 {
		return mutation.MutationConflict("job_run", run.ID, mutation.MutationConflictLeaseLost, nil)
	}
	object, err := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "job_run")
	if err != nil {
		return err
	}
	current, ok, err := s.repository.GetRecord(ctx, workspaceID, object, run.ID)
	if err != nil {
		return err
	}
	if !ok {
		return mutation.MutationConflict("job_run", run.ID, mutation.MutationConflictLeaseLost, nil)
	}
	current.Data["lease_expires_at"] = leaseNow.Add(s.leaseTTL()).Format(time.RFC3339)
	current.UpdatedAt = leaseNow.Format(time.RFC3339)
	status := strings.TrimSpace(fmt.Sprint(run.Data["status"]))
	updated, err := s.updateRunIfCurrent(ctx, workspaceID, object, current, map[string]any{"status": status, "lease_owner": owner, "fencing_token": fencingToken})
	if err != nil {
		return err
	}
	if !updated {
		return mutation.MutationConflict("job_run", run.ID, mutation.MutationConflictLeaseLost, nil)
	}
	return nil
}

func (s *SchedulerApplicationService) HeartbeatRun(ctx context.Context, run recordmodel.Record, now time.Time, scope principalmodel.SystemScope) error {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return schedulerError(apperror.KindForbidden, "backend.system_scope_required", err)
	}
	return s.heartbeatRun(ctx, principalmodel.InstallationWorkspaceID, run, now)
}

func existingStringBefore(record recordmodel.Record, key string) string {
	value := strings.TrimSpace(fmt.Sprint(record.Data[key]))
	if value == "<nil>" {
		return ""
	}
	return value
}

func ExistingStringBefore(record recordmodel.Record, key string) string {
	return existingStringBefore(record, key)
}

func (s *SchedulerApplicationService) updateRunIfCurrent(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, record recordmodel.Record, conditions map[string]any) (bool, error) {
	if err := validateSchedulerMutationObject(object); err != nil {
		return false, err
	}
	updated, err := s.repository.UpdateRecordWhere(ctx, workspaceID, object, record, conditions)
	if err != nil {
		return false, internalError("lease scheduler job run", err)
	}
	if updated {
		s.insertOperationAudit(ctx, "internal_record_mutation", object.Key, record.ID, workflowWorkerPrincipal(), "Internal record mutation", nil, record.Data, map[string]any{"policy": "scheduler_runtime", "operation": "update", "reason": "conditional scheduler lease claim"})
	}
	return updated, nil
}

func validateSchedulerMutationObject(object definitionmodel.ObjectSchema) error {
	allowed := map[string]bool{
		"scheduler_cursor": true, "job_run": true, "job_run_event": true, "job_dead_letter": true,
		"report_query_run": true, "report_export_audit": true, "download_task": true, "report_definition": true,
	}
	if allowed[strings.TrimSpace(object.Key)] {
		return nil
	}
	return forbidden("backend.record.internal_mutation_policy_denied", "policy", "scheduler_runtime", "operation", "update", "object", object.Key)
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func (s *SchedulerApplicationService) advanceDefinitionCursor(ctx context.Context, workspaceID string, run recordmodel.Record, status string, now time.Time) error {
	definitionID := strings.TrimSpace(fmt.Sprint(run.Data["scheduler_definition_key"]))
	if definitionID == "" {
		return nil
	}
	cursorObject, err := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "scheduler_cursor")
	if err != nil {
		return err
	}
	if s.definitions == nil {
		return schedulerError(apperror.KindUnavailable, "backend.scheduler.definition_source_unavailable", nil)
	}
	definition, ok, err := s.definitions.GetSchedulerDefinition(ctx, definitionID)
	if err != nil {
		return internalError("get scheduler job definition", err)
	}
	if !ok {
		return nil
	}
	cursor, cursorFound, err := s.repository.GetRecord(ctx, workspaceID, cursorObject, definitionID)
	if err != nil {
		return internalError("get scheduler cursor", err)
	}
	previousNextRunAt := strings.TrimSpace(fmt.Sprint(cursor.Data["next_run_at"]))
	if !cursorFound {
		previousNextRunAt = strings.TrimSpace(fmt.Sprint(definition.Data["next_run_at"]))
		cursor = recordmodel.Record{ID: definitionID, CreatedAt: now.Format(time.RFC3339), Data: map[string]any{"scheduler_definition_key": definitionID}}
	}
	definition.Data["next_run_at"] = previousNextRunAt
	cursorAnchor := schedulerDefinitionCursorAnchor(definition, run, now)
	nextRunAt := schedulerNextRunAt(definition, cursorAnchor)
	cursor.Data["last_run_at"] = now.Format(time.RFC3339)
	cursor.Data["last_run_status"] = status
	cursor.Data["next_run_at"] = nextRunAt.Format(time.RFC3339)
	cursor.UpdatedAt = now.Format(time.RFC3339)
	if cursorFound {
		advanced, err := s.updateRunIfCurrent(ctx, workspaceID, cursorObject, cursor, map[string]any{"next_run_at": previousNextRunAt})
		if err != nil {
			return err
		}
		if !advanced {
			return mutation.MutationConflict("scheduler_cursor", cursor.ID, mutation.MutationConflictOptimistic, nil)
		}
	} else if err := s.insertRecord(ctx, workspaceID, cursorObject, cursor, "create scheduler definition cursor"); err != nil {
		return err
	}
	return s.appendRunEvent(ctx, workspaceID, run.ID, "definition_cursor_advanced", "Scheduler definition cursor advanced.", now, map[string]any{
		"scheduler_definition_key": definition.ID,
		"status":                   status,
		"schedule_type":            schedulerpolicy.SchedulerScheduleType(definition),
		"schedule_expression":      strings.TrimSpace(fmt.Sprint(definition.Data["schedule_expression"])),
		"timezone":                 strings.TrimSpace(fmt.Sprint(definition.Data["timezone"])),
		"previous_next_run_at":     previousNextRunAt,
		"next_run_at":              nextRunAt.Format(time.RFC3339),
		"window_suffix":            schedulerDefinitionWindowSuffix(definition, cursorAnchor),
	})
}

func (s *SchedulerApplicationService) appendRunEvent(ctx context.Context, workspaceID, runID string, eventType string, message string, now time.Time, metadata map[string]any) error {
	eventObject, err := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "job_run_event")
	if err != nil {
		return err
	}
	event := schedulerRunEventRecord(runID, eventType, message, now, metadata)
	if err := s.insertRecord(ctx, workspaceID, eventObject, event, "scheduler run event"); err != nil {
		return err
	}
	return nil
}

func schedulerRunEventRecord(runID, eventType, message string, now time.Time, metadata map[string]any) recordmodel.Record {
	return recordmodel.Record{ID: fmt.Sprintf("jobevt_%s_%d", schedulerpolicy.SchedulerSlug(runID+"_"+eventType), now.UnixNano()), CreatedAt: now.Format(time.RFC3339), UpdatedAt: now.Format(time.RFC3339), Data: map[string]any{
		"job_run_id": runID, "event_type": eventType, "message": message, "metadata_json": schedulerpolicy.SchedulerMetadataJSON(metadata),
	}}
}

func (s *SchedulerApplicationService) AppendRunEvent(ctx context.Context, runID, eventType, message string, now time.Time, metadata map[string]any, scope principalmodel.SystemScope) error {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return schedulerError(apperror.KindForbidden, "backend.system_scope_required", err)
	}
	return s.appendRunEvent(ctx, principalmodel.InstallationWorkspaceID, runID, eventType, message, now, metadata)
}

func (s *SchedulerApplicationService) deadLetterRun(ctx context.Context, workspaceID string, run recordmodel.Record, definition recordmodel.Record, reason string, now time.Time) error {
	deadLetterObject, err := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "job_dead_letter")
	if err != nil {
		return err
	}
	deadLetterID := "jobdl_" + schedulerpolicy.SchedulerSlug(run.ID)
	if _, ok, err := s.repository.GetRecord(ctx, workspaceID, deadLetterObject, deadLetterID); err != nil {
		return internalError("get scheduler dead letter", err)
	} else if ok {
		return nil
	}
	definitionID := strings.TrimSpace(definition.ID)
	if definitionID == "" {
		definitionID = strings.TrimSpace(fmt.Sprint(run.Data["scheduler_definition_key"]))
	}
	deadLetter := recordmodel.Record{
		ID:        deadLetterID,
		CreatedAt: now.Format(time.RFC3339),
		UpdatedAt: now.Format(time.RFC3339),
		Data: map[string]any{
			"job_run_id":               run.ID,
			"scheduler_definition_key": definitionID,
			"status":                   "open",
			"reason":                   valueOrDefault(strings.TrimSpace(reason), "backend.scheduler.dead_letter"),
			"last_error":               strings.TrimSpace(fmt.Sprint(run.Data["error_message"])),
			"failed_at":                now.Format(time.RFC3339),
			"resolved_at":              "",
			"resolved_by":              "",
			"resolution_note":          "",
		},
	}
	if err := s.insertRecord(ctx, workspaceID, deadLetterObject, deadLetter, "scheduler dead letter"); err != nil {
		return err
	}
	return s.appendRunEvent(ctx, workspaceID, run.ID, "dead_lettered", reason, now, map[string]any{"dead_letter_id": deadLetter.ID})
}
