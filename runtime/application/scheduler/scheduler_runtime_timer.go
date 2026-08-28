package scheduler

import (
	"encoding/json"
	"fmt"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	schedulercontract "github.com/domainry/domainry-runtime/runtime/domain/scheduler/contract"
	schedulerpolicy "github.com/domainry/domainry-runtime/runtime/domain/scheduler/policy"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"context"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"

	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/logging"
	"github.com/domainry/domainry-foundation/mutation"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
	"go.uber.org/zap"
)

func (s *SchedulerApplicationService) StartWorker(ctx context.Context, cfg WorkerConfig, hasWorkflowDefinitions bool) <-chan struct{} {
	cfg = NormalizeWorkerConfig(cfg)
	if !cfg.Enabled || (!hasWorkflowDefinitions && !s.RuntimeAvailable(ctx, workflowWorkerPrincipal())) {
		return workerplatform.Stopped()
	}
	return workerplatform.StartAdaptiveLoop(ctx, "scheduler", cfg.PollInterval, 5*cfg.PollInterval, func() bool {
		worked := false
		s.worker.Control.RunIfAccepting(func() { worked = s.processWorkerTick(ctx, cfg.BatchSize) })
		return worked
	})
}

func (s *SchedulerApplicationService) processWorkerTick(ctx context.Context, limit int) bool {
	if err := ctx.Err(); err != nil {
		return false
	}
	if !s.provisionWorkerDefinitions(ctx) {
		return false
	}
	worked := false
	if _, ok := s.runtime.(RecordTimerTargetRuntime); ok {
		now := s.worker.Clock.Now()
		principal := workflowWorkerPrincipal()
		if processed, timerErr := s.ProcessDueRecordTimersForAllWorkspaces(ctx, now, limit, principal, principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "process due record timers")); timerErr != nil {
			workerplatform.ObserveOutcome("record_timer", "failed")
			logging.FromContext(ctx).Error("record timer worker failed", logging.StableErrorFields(timerErr)...)
		} else if processed > 0 {
			worked = true
			workerplatform.ObserveOutcome("record_timer", "completed")
			logging.FromContext(ctx).Info("record timer worker completed", zap.Int("processed", processed))
		}
	}
	if err := ctx.Err(); err != nil {
		return worked
	}
	result, err := s.ProcessDueJobs(ctx, limit, workflowWorkerPrincipal(), "scheduler")
	if err != nil {
		logging.FromContext(ctx).Error("workflow worker failed", logging.StableErrorFields(err)...)
		return worked
	}
	if result.Processed > 0 {
		worked = true
		logging.FromContext(ctx).Info("workflow worker completed", zap.Int("processed", result.Processed))
	}
	return worked
}

func (s *SchedulerApplicationService) ProcessDueJobs(ctx context.Context, limit int, principal principalmodel.Principal, triggerSource string) (workflowmodel.WorkflowProcessResult, error) {
	if err := schedulerAuthorizeCommand(principal); err != nil {
		return workflowmodel.WorkflowProcessResult{}, err
	}
	workspaceID := schedulerWorkspaceID(principal)
	principal.WorkspaceID = workspaceID
	if !s.RuntimeAvailable(ctx, principal) {
		return s.runtime.ProcessDueWorkflowExecutions(ctx, limit, principal)
	}
	now := s.worker.Clock.Now()
	definitions, err := s.dueDefinitions(ctx, workspaceID, definitionmodel.ObjectSchema{}, now)
	if err != nil {
		return workflowmodel.WorkflowProcessResult{}, err
	}
	workerplatform.SetQueueMetrics("scheduler", len(definitions), schedulerDefinitionQueueLag(definitions, now))
	processed := []workflowmodel.WorkflowExecution{}
	for _, definition := range definitions {
		if err := ctx.Err(); err != nil {
			return workflowmodel.WorkflowProcessResult{Processed: len(processed), Executions: processed}, err
		}
		if len(processed) >= schedulerpolicy.SchedulerLimit(limit) {
			break
		}
		run, claimed, err := s.claimRun(ctx, workspaceID, definition, triggerSource, now)
		if err != nil {
			return workflowmodel.WorkflowProcessResult{}, err
		}
		if !claimed {
			continue
		}
		workerplatform.ObserveOutcome("scheduler", "claimed")
		result, err := s.processClaimedRun(ctx, workspaceID, definition, run, schedulerpolicy.SchedulerLimit(limit)-len(processed), principal, now)
		if err != nil {
			return workflowmodel.WorkflowProcessResult{}, err
		}
		processed = append(processed, result.Executions...)
	}
	return workflowmodel.WorkflowProcessResult{Processed: len(processed), Executions: processed}, nil
}

func schedulerDefinitionQueueLag(definitions []recordmodel.Record, now time.Time) time.Duration {
	var oldest time.Time
	for _, definition := range definitions {
		candidate, err := time.Parse(time.RFC3339, fmt.Sprint(definition.Data["next_run_at"]))
		if err == nil && (oldest.IsZero() || candidate.Before(oldest)) {
			oldest = candidate
		}
	}
	if oldest.IsZero() || oldest.After(now) {
		return 0
	}
	return now.Sub(oldest)
}

func (s *SchedulerApplicationService) processClaimedRun(ctx context.Context, workspaceID string, definition recordmodel.Record, run recordmodel.Record, limit int, principal principalmodel.Principal, now time.Time) (workflowmodel.WorkflowProcessResult, error) {
	leaseTTL := s.leaseTTL()
	workCtx, stopHeartbeat := workerplatform.WithHeartbeat(ctx, leaseTTL/3, func(heartbeatCtx context.Context) error {
		return s.heartbeatRun(heartbeatCtx, workspaceID, run, s.worker.Clock.Now())
	})
	switch schedulerDefinitionTargetType(definition) {
	case "workflow":
		page, checkpointed, err := s.processWorkflowTargetPage(workCtx, definition, run, limit, principal)
		result := page.WorkflowProcessResult
		if !checkpointed {
			result, err = s.processWorkflowTarget(workCtx, definition, run, limit, principal)
		}
		if heartbeatErr := stopHeartbeat(); err == nil && heartbeatErr != nil {
			err = heartbeatErr
		}
		if err != nil {
			if updateErr := s.finishRun(ctx, workspaceID, run, nil, nil, err, now); updateErr != nil {
				return workflowmodel.WorkflowProcessResult{}, updateErr
			}
			return workflowmodel.WorkflowProcessResult{}, err
		}
		if checkpointed && !page.Complete {
			if updateErr := s.checkpointRun(ctx, workspaceID, run, page, now); updateErr != nil {
				return workflowmodel.WorkflowProcessResult{}, updateErr
			}
			return result, nil
		}
		if updateErr := s.finishRun(ctx, workspaceID, run, result.Executions, nil, nil, now); updateErr != nil {
			return workflowmodel.WorkflowProcessResult{}, updateErr
		}
		return result, nil
	case "report_export":
		evidence, err := s.schedulerProcessReportExportDefinition(workCtx, workspaceID, definition, run, now)
		if heartbeatErr := stopHeartbeat(); err == nil && heartbeatErr != nil {
			err = heartbeatErr
		}
		if err != nil {
			if updateErr := s.finishRun(ctx, workspaceID, run, nil, evidence, err, now); updateErr != nil {
				return workflowmodel.WorkflowProcessResult{}, updateErr
			}
			return workflowmodel.WorkflowProcessResult{}, err
		}
		if updateErr := s.finishRun(ctx, workspaceID, run, nil, evidence, nil, now); updateErr != nil {
			return workflowmodel.WorkflowProcessResult{}, updateErr
		}
		return workflowmodel.WorkflowProcessResult{}, nil
	case "report_snapshot_refresh":
		evidence, err := s.schedulerProcessReportSnapshotDefinition(workCtx, workspaceID, definition, run, principal)
		if heartbeatErr := stopHeartbeat(); err == nil && heartbeatErr != nil {
			err = heartbeatErr
		}
		if err != nil {
			if updateErr := s.finishRun(ctx, workspaceID, run, nil, evidence, err, now); updateErr != nil {
				return workflowmodel.WorkflowProcessResult{}, updateErr
			}
			return workflowmodel.WorkflowProcessResult{}, err
		}
		if updateErr := s.finishRun(ctx, workspaceID, run, nil, evidence, nil, now); updateErr != nil {
			return workflowmodel.WorkflowProcessResult{}, updateErr
		}
		return workflowmodel.WorkflowProcessResult{}, nil
	default:
		_ = stopHeartbeat()
		err := badRequest("backend.scheduler.unsupported_target_type", "target_type", schedulerDefinitionTargetType(definition))
		if updateErr := s.finishRun(ctx, workspaceID, run, nil, nil, err, now); updateErr != nil {
			return workflowmodel.WorkflowProcessResult{}, updateErr
		}
		return workflowmodel.WorkflowProcessResult{}, err
	}
}

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
	record := cloneRecordTimer(lease.Record)
	record.Data["status"], record.Data["fired_at"] = "fired", now.UTC().Format(time.RFC3339Nano)
	record.Data["lease_owner"], record.Data["lease_expires_at"] = "", ""
	record.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	updated, err := s.repository.UpdateRecordWhere(ctx, workspaceID, object, record, map[string]any{"status": "leased", "lease_owner": lease.Owner, "fencing_token": lease.Token})
	if err != nil {
		return internalError("finish record timer", err)
	}
	if !updated {
		return mutation.MutationConflict("record_timer", record.ID, mutation.MutationConflictLeaseLost, nil)
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
	record := cloneRecordTimer(lease.Record)
	attempt := schedulerpolicy.SchedulerInt(record.Data["attempt"], 0)
	maxAttempts := schedulerpolicy.SchedulerInt(record.Data["max_attempts"], 10)
	record.Data["last_error"] = apperror.CodeOf(executionErr)
	record.Data["lease_owner"], record.Data["lease_expires_at"] = "", ""
	if attempt >= maxAttempts {
		record.Data["status"] = "failed"
		record.Data["failed_at"] = now.UTC().Format(time.RFC3339Nano)
	} else {
		record.Data["status"] = "scheduled"
		record.Data["due_at"] = now.Add(recordTimerRetryDelay(record)).UTC().Format(time.RFC3339Nano)
	}
	record.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
	updated, err := s.repository.UpdateRecordWhere(ctx, workspaceID, object, record, map[string]any{"status": "leased", "lease_owner": lease.Owner, "fencing_token": lease.Token})
	if err != nil {
		return internalError("fail record timer", err)
	}
	if !updated {
		return mutation.MutationConflict("record_timer", record.ID, mutation.MutationConflictLeaseLost, nil)
	}
	return nil
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
	commits := make([]transactionmodel.RecordMutationCommit, 0, len(leases))
	for _, lease := range leases {
		record := cloneRecordTimer(lease.Record)
		record.Data["status"], record.Data["fired_at"] = "fired", now.UTC().Format(time.RFC3339Nano)
		record.Data["lease_owner"], record.Data["lease_expires_at"] = "", ""
		record.UpdatedAt = now.UTC().Format(time.RFC3339Nano)
		commits = append(commits, transactionmodel.RecordMutationCommit{Operation: "update", Object: object, Record: record, Conditions: map[string]any{"status": "leased", "lease_owner": lease.Owner, "fencing_token": lease.Token}})
	}
	if err := s.repository.CommitRecordMutationBatch(ctx, workspaceID, commits); err != nil {
		return err
	}
	return nil
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
	attempted := len(leases)
	processed := 0
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
		execution := RecordTimerExecution{
			TimerID: lease.Record.ID, WorkspaceID: workspaceID,
			ObjectKey: strings.TrimSpace(fmt.Sprint(lease.Record.Data["object_key"])), RecordID: strings.TrimSpace(fmt.Sprint(lease.Record.Data["record_id"])),
			TargetType: strings.TrimSpace(fmt.Sprint(lease.Record.Data["target_type"])), TargetKey: strings.TrimSpace(fmt.Sprint(lease.Record.Data["target_key"])),
			Payload: payload, IdempotencyKey: lease.Record.ID,
		}
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
	workspaceRepository, ok := s.repository.(schedulercontract.RecordTimerWorkspaceRepository)
	if !ok {
		return 0, schedulerError(apperror.KindInternal, "backend.scheduler.record_timer_workspace_repository_unavailable", nil)
	}
	object, err := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "record_timer")
	if err != nil {
		return 0, err
	}
	workspaces, err := workspaceRepository.ListDueRecordTimerWorkspaces(ctx, object, now)
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
	baseQuota, extraQuota := limit/len(ordered), limit%len(ordered)
	processed := 0
	var firstErr error
	for index, workspaceID := range ordered {
		quota := baseQuota
		if index < extraQuota {
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
