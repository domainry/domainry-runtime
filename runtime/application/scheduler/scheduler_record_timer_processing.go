package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/mutation"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	schedulercontract "github.com/domainry/domainry-runtime/runtime/domain/scheduler/contract"
	schedulerpolicy "github.com/domainry/domainry-runtime/runtime/domain/scheduler/policy"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

func (s *SchedulerApplicationService) FinishRecordTimer(ctx context.Context, workspaceID string, lease RecordTimerLease, now time.Time, scope principalmodel.SystemScope) error {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return schedulerError(apperror.KindForbidden, "backend.system_scope_required", err)
	}
	if now.IsZero() {
		now = s.worker.Clock.Now()
	}
	object, err := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "record_timer")
	if err != nil {
		return err
	}
	eventObject, err := s.recordTimerEventObject(ctx)
	if err != nil {
		return err
	}
	record := cloneRecordTimer(lease.Record)
	record.Data["status"], record.Data["fired_at"] = "fired", now.UTC().Format(time.RFC3339Nano)
	record.Data["lease_owner"], record.Data["lease_expires_at"] = "", ""
	record.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	commits := []transactionmodel.RecordMutationCommit{
		{Operation: "update", Object: object, Record: record, Conditions: map[string]any{"status": "leased", "lease_owner": lease.Owner, "fencing_token": lease.Token}},
		buildRecordTimerEventCommit(eventObject, record, "succeeded", lease.Owner, "", "", "", now),
	}
	if err := s.repository.CommitRecordMutationBatch(ctx, workspaceID, commits); err != nil {
		return recordTimerLeaseCommitError(record.ID, err)
	}
	return nil
}

func (s *SchedulerApplicationService) FailRecordTimer(ctx context.Context, workspaceID string, lease RecordTimerLease, executionErr error, now time.Time, scope principalmodel.SystemScope) error {
	if executionErr == nil {
		return schedulerError(apperror.KindBadRequest, "backend.scheduler.record_timer_failure_required", nil)
	}
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return schedulerError(apperror.KindForbidden, "backend.system_scope_required", err)
	}
	if now.IsZero() {
		now = s.worker.Clock.Now()
	}
	object, err := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "record_timer")
	if err != nil {
		return err
	}
	eventObject, err := s.recordTimerEventObject(ctx)
	if err != nil {
		return err
	}
	record := cloneRecordTimer(lease.Record)
	attempt, maximum := schedulerpolicy.SchedulerInt(record.Data["attempt"], 0), schedulerpolicy.SchedulerInt(record.Data["max_attempts"], 10)
	record.Data["last_error"], record.Data["lease_owner"], record.Data["lease_expires_at"] = apperror.CodeOf(executionErr), "", ""
	if attempt >= maximum {
		record.Data["status"], record.Data["failed_at"] = "failed", now.UTC().Format(time.RFC3339Nano)
	} else {
		record.Data["status"], record.Data["due_at"] = "scheduled", now.Add(recordTimerRetryDelay(record)).UTC().Format(time.RFC3339Nano)
	}
	record.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	eventType := "retry_scheduled"
	if record.Data["status"] == "failed" {
		eventType = "failed"
	}
	commits := []transactionmodel.RecordMutationCommit{
		{Operation: "update", Object: object, Record: record, Conditions: map[string]any{"status": "leased", "lease_owner": lease.Owner, "fencing_token": lease.Token}},
		buildRecordTimerEventCommit(eventObject, record, eventType, lease.Owner, apperror.CodeOf(executionErr), executionErr.Error(), strings.TrimSpace(fmt.Sprint(record.Data["due_at"])), now),
	}
	if err := s.repository.CommitRecordMutationBatch(ctx, workspaceID, commits); err != nil {
		return recordTimerLeaseCommitError(record.ID, err)
	}
	return nil
}

func recordTimerLeaseCommitError(timerID string, err error) error {
	if mutation.IsMutationConflict(err, mutation.MutationConflictOptimistic) {
		return mutation.MutationConflict("record_timer", timerID, mutation.MutationConflictLeaseLost, err)
	}
	return err
}

func (s *SchedulerApplicationService) FinishRecordTimers(ctx context.Context, workspaceID string, leases []RecordTimerLease, now time.Time, scope principalmodel.SystemScope) error {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return schedulerError(apperror.KindForbidden, "backend.system_scope_required", err)
	}
	if len(leases) == 0 {
		return nil
	}
	if now.IsZero() {
		now = s.worker.Clock.Now()
	}
	object, err := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "record_timer")
	if err != nil {
		return err
	}
	eventObject, err := s.recordTimerEventObject(ctx)
	if err != nil {
		return err
	}
	commits := make([]transactionmodel.RecordMutationCommit, 0, len(leases)*2)
	for _, lease := range leases {
		record := cloneRecordTimer(lease.Record)
		record.Data["status"], record.Data["fired_at"] = "fired", now.UTC().Format(time.RFC3339Nano)
		record.Data["lease_owner"], record.Data["lease_expires_at"], record.UpdatedAt = "", "", now.UTC().Format(time.RFC3339Nano)
		commits = append(commits, transactionmodel.RecordMutationCommit{Operation: "update", Object: object, Record: record, Conditions: map[string]any{"status": "leased", "lease_owner": lease.Owner, "fencing_token": lease.Token}})
		commits = append(commits, buildRecordTimerEventCommit(eventObject, record, "succeeded", lease.Owner, "", "", "", now))
	}
	return s.repository.CommitRecordMutationBatch(ctx, workspaceID, commits)
}

func (s *SchedulerApplicationService) ProcessDueRecordTimers(ctx context.Context, workspaceID string, now time.Time, limit int, principal principalmodel.Principal, scope principalmodel.SystemScope) (int, error) {
	processed, _, err := s.processDueRecordTimers(ctx, workspaceID, now, limit, principal, scope)
	return processed, err
}

func (s *SchedulerApplicationService) processDueRecordTimers(ctx context.Context, workspaceID string, now time.Time, limit int, principal principalmodel.Principal, scope principalmodel.SystemScope) (int, int, error) {
	runtime, ok := s.runtime.(RecordTimerTargetRuntime)
	if !ok {
		return 0, 0, schedulerError(apperror.KindInternal, "backend.scheduler.record_timer_runtime_unavailable", nil)
	}
	leases, err := s.ClaimDueRecordTimers(ctx, workspaceID, now, limit, scope)
	if err != nil {
		return 0, 0, err
	}
	processed, attempted := 0, len(leases)
	var firstErr error
	for _, lease := range leases {
		payload := map[string]any{}
		if raw := strings.TrimSpace(fmt.Sprint(lease.Record.Data["payload_json"])); raw != "" {
			if err := json.Unmarshal([]byte(raw), &payload); err != nil {
				executionErr := schedulerError(apperror.KindBadRequest, "backend.scheduler.record_timer_payload_invalid", err)
				if failErr := s.FailRecordTimer(ctx, workspaceID, lease, executionErr, now, scope); failErr != nil {
					return processed, attempted, failErr
				}
				if firstErr == nil {
					firstErr = executionErr
				}
				continue
			}
		}
		execution := RecordTimerExecution{TimerID: lease.Record.ID, WorkspaceID: workspaceID, ObjectKey: existingStringBefore(lease.Record, "object_key"), RecordID: existingStringBefore(lease.Record, "record_id"), TargetType: existingStringBefore(lease.Record, "target_type"), TargetKey: existingStringBefore(lease.Record, "target_key"), Payload: payload, IdempotencyKey: lease.Record.ID}
		if err := runtime.ExecuteRecordTimer(ctx, execution, principal); err != nil {
			if failErr := s.FailRecordTimer(ctx, workspaceID, lease, err, now, scope); failErr != nil {
				return processed, attempted, failErr
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if err := s.FinishRecordTimer(ctx, workspaceID, lease, now, scope); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		processed++
	}
	return processed, attempted, firstErr
}

func (s *SchedulerApplicationService) ProcessDueRecordTimersForAllWorkspaces(ctx context.Context, now time.Time, limit int, principal principalmodel.Principal, scope principalmodel.SystemScope) (int, error) {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return 0, schedulerError(apperror.KindForbidden, "backend.system_scope_required", err)
	}
	if now.IsZero() {
		now = s.worker.Clock.Now()
	}
	repository, ok := s.repository.(schedulercontract.RecordTimerWorkspaceRepository)
	if !ok {
		return 0, schedulerError(apperror.KindInternal, "backend.scheduler.record_timer_workspace_repository_unavailable", nil)
	}
	object, err := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "record_timer")
	if err != nil {
		return 0, err
	}
	workspaces, err := repository.ListDueRecordTimerWorkspaces(ctx, object, now)
	if err != nil {
		return 0, internalError("list record timer workspaces", err)
	}
	limit = schedulerpolicy.SchedulerLimit(limit)
	if len(workspaces) == 0 {
		return 0, nil
	}
	s.timerCursorMu.Lock()
	start := s.timerCursor % len(workspaces)
	s.timerCursor = (s.timerCursor + 1) % len(workspaces)
	s.timerCursorMu.Unlock()
	ordered := append(append([]string(nil), workspaces[start:]...), workspaces[:start]...)
	base, extra, processed := limit/len(ordered), limit%len(ordered), 0
	var firstErr error
	for index, workspaceID := range ordered {
		quota := base
		if index < extra {
			quota++
		}
		if quota == 0 {
			continue
		}
		workspacePrincipal := principal
		workspacePrincipal.WorkspaceID = workspaceID
		count, _, processErr := s.processDueRecordTimers(ctx, workspaceID, now, quota, workspacePrincipal, scope)
		if processErr != nil && firstErr == nil {
			firstErr = processErr
		}
		processed += count
	}
	return processed, firstErr
}
