package scheduler

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/mutation"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	schedulerpolicy "github.com/domainry/domainry-runtime/runtime/domain/scheduler/policy"
	schedulervalidation "github.com/domainry/domainry-runtime/runtime/domain/scheduler/validation"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type SchedulerOperationResult struct {
	Status  string             `json:"status"`
	Message string             `json:"message,omitempty"`
	Run     recordmodel.Record `json:"run,omitempty"`
	Result  any                `json:"result,omitempty"`
}

type SchedulerOperationRuntime interface {
	ProcessDueWorkflowExecutions(context.Context, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error)
}

type TargetedSchedulerOperationRuntime interface {
	ProcessDueWorkflowExecutionsForTarget(context.Context, string, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error)
}

type RecordTimerExecution struct {
	TimerID        string
	WorkspaceID    string
	ObjectKey      string
	RecordID       string
	TargetType     string
	TargetKey      string
	Payload        map[string]any
	IdempotencyKey string
}

type RecordTimerTargetRuntime interface {
	ExecuteRecordTimer(context.Context, RecordTimerExecution, principalmodel.Principal) error
}

// SimulateTenantAdminDefinition returns dry-run evidence for the governed
// definition workspace. It deliberately does not create job_run records:
// durable execution evidence and recovery belong to the Runtime Ops surface.
func (s *SchedulerApplicationService) SimulateTenantAdminDefinition(ctx context.Context, definitionID string, principal principalmodel.Principal) (SchedulerOperationResult, error) {
	if err := schedulerDefinitionWriteAllowed(principal); err != nil {
		return SchedulerOperationResult{}, err
	}
	if s.definitions == nil {
		return SchedulerOperationResult{}, schedulerError(apperror.KindUnavailable, "backend.scheduler.definition_source_unavailable", nil)
	}
	definition, found, err := s.definitions.GetSchedulerDefinition(ctx, strings.TrimSpace(definitionID))
	if err != nil {
		return SchedulerOperationResult{}, internalError("get scheduler job definition", err)
	}
	if !found {
		return SchedulerOperationResult{}, notFound("backend.scheduler.definition_not_found")
	}
	if err := schedulervalidation.SchedulerValidateDefinitionContract(ctx, definition.Data); err != nil {
		return SchedulerOperationResult{}, err
	}
	now := s.worker.Clock.Now()
	next := schedulerpolicy.SchedulerScheduleNextRunAt(definition, now)
	return SchedulerOperationResult{
		Status:  "simulated",
		Message: "backend.scheduler.simulated",
		Run: recordmodel.Record{
			ID: "preview_" + schedulerpolicy.SchedulerSlug(definition.ID),
			Data: map[string]any{
				"scheduler_definition_key": definition.ID,
				"status":                   "preview",
				"scheduled_for":            next.UTC().Format(time.RFC3339),
				"target_type":              schedulerDefinitionTargetType(definition),
				"target_key":               strings.TrimSpace(fmt.Sprint(definition.Data["target_key"])),
			},
		},
	}, nil
}

func (s *SchedulerApplicationService) SimulateJob(ctx context.Context, definitionID string, principal principalmodel.Principal) (SchedulerOperationResult, error) {
	definition, err := s.schedulerDefinitionForOperation(ctx, definitionID, principal)
	if err != nil {
		return SchedulerOperationResult{}, err
	}
	now := s.worker.Clock.Now()
	run := recordmodel.Record{
		ID:        "jobrun_" + schedulerpolicy.SchedulerSlug(strings.TrimSpace(fmt.Sprint(definition.Data["key"]))) + "_simulate_" + fmt.Sprint(now.UnixNano()),
		CreatedAt: now.Format(time.RFC3339),
		UpdatedAt: now.Format(time.RFC3339),
		Data: map[string]any{
			"scheduler_definition_key": definition.ID,
			"status":                   "cancelled",
			"triggered_by":             "manual",
			"scheduled_for":            now.Format(time.RFC3339),
			"attempt":                  0,
			"max_attempts":             schedulerpolicy.SchedulerInt(definition.Data["max_attempts"], 3),
			"idempotency_key":          definition.ID + ":simulate:" + now.Format("20060102150405"),
			"workflow_key":             strings.TrimSpace(fmt.Sprint(definition.Data["target_key"])),
			"target_object":            strings.TrimSpace(fmt.Sprint(definition.Data["target_object"])),
			"result_json":              `{"status":"simulated"}`,
		},
	}
	runObject, err := s.objectForPrincipal(ctx, principal, "job_run")
	if err != nil {
		return SchedulerOperationResult{}, err
	}
	eventObject, err := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "job_run_event")
	if err != nil {
		return SchedulerOperationResult{}, err
	}
	event := schedulerRunEventRecord(run.ID, "simulated", "Scheduler job simulation recorded.", now, map[string]any{
		"scheduler_definition_key": definition.ID,
		"target_type":              schedulerDefinitionTargetType(definition),
		"target_key":               strings.TrimSpace(fmt.Sprint(definition.Data["target_key"])),
	})
	if err := s.repository.CommitRecordMutationBatch(ctx, principal.WorkspaceID, []transactionmodel.RecordMutationCommit{
		{Operation: "create", Object: runObject, Record: run},
		{Operation: "create", Object: eventObject, Record: event},
	}); err != nil {
		return SchedulerOperationResult{}, err
	}
	return SchedulerOperationResult{Status: "simulated", Message: "backend.scheduler.simulated", Run: run}, nil
}

func (s *SchedulerApplicationService) RunJob(ctx context.Context, definitionID, idempotencyKey string, principal principalmodel.Principal) (SchedulerOperationResult, error) {
	return s.runJob(ctx, definitionID, idempotencyKey, principal)
}

// RescheduleDefinition moves only next_run_at through the governed Scheduler
// command surface; scheduler_cursor remains unavailable to generic Record CRUD.
func (s *SchedulerApplicationService) RescheduleDefinition(ctx context.Context, definitionID string, nextRunAt time.Time, principal principalmodel.Principal) (SchedulerOperationResult, error) {
	if err := schedulerOpsCommandAllowed(principal); err != nil {
		return SchedulerOperationResult{}, err
	}
	if nextRunAt.IsZero() {
		return SchedulerOperationResult{}, badRequest("backend.scheduler.next_run_at_required")
	}
	definition, err := s.schedulerDefinitionForOperation(ctx, definitionID, principal)
	if err != nil {
		return SchedulerOperationResult{}, err
	}
	cursor, before, err := s.rescheduleDefinitionCursor(ctx, principal.WorkspaceID, definition, nextRunAt)
	if err != nil {
		return SchedulerOperationResult{}, err
	}
	s.insertOperationAudit(ctx, "scheduler_definition_rescheduled", "scheduler_cursor", cursor.ID, principal, "Scheduler definition rescheduled "+definition.ID, before, cursor.Data, map[string]any{"scheduler_definition_key": definition.ID, "next_run_at": cursor.Data["next_run_at"]})
	return SchedulerOperationResult{Status: "rescheduled", Message: "backend.scheduler.definition_rescheduled", Run: cursor}, nil
}

func (s *SchedulerApplicationService) RetryRun(ctx context.Context, runID, callerKey string, principal principalmodel.Principal) (SchedulerOperationResult, error) {
	if strings.TrimSpace(callerKey) == "" {
		return SchedulerOperationResult{}, badRequest(idempotency.ErrorCodeMissingKey)
	}
	if err := schedulerOpsCommandAllowed(principal); err != nil {
		return SchedulerOperationResult{}, err
	}
	run, definition, err := s.schedulerRunAndDefinition(ctx, runID, principal)
	if err != nil {
		return SchedulerOperationResult{}, err
	}
	commandKey := schedulerCommandIdempotencyKey("retry", run.ID, callerKey)
	if strings.TrimSpace(fmt.Sprint(run.Data["last_command_scope"])) == "scheduler.retry" && strings.TrimSpace(fmt.Sprint(run.Data["last_command_key"])) == commandKey {
		return SchedulerOperationResult{Status: "replayed", Message: "backend.scheduler.retry_replayed", Run: run}, nil
	}
	now := s.worker.Clock.Now()
	leaseTTL := s.leaseTTL()
	before := recordvalidation.RecordCloneData(run.Data)
	previousStatus := strings.TrimSpace(fmt.Sprint(run.Data["status"]))
	previousFencingToken := schedulerpolicy.SchedulerInt(run.Data["fencing_token"], 0)
	previousUpdatedAt := run.UpdatedAt
	run.Data["status"] = "retrying"
	run.Data["error_message"] = ""
	run.Data["error_category"] = ""
	run.Data["recoverability"] = ""
	run.Data["lease_owner"] = s.worker.WorkerID.String()
	run.Data["lease_expires_at"] = now.Add(leaseTTL).Format(time.RFC3339)
	run.Data["next_retry_at"] = ""
	run.Data["attempt"] = schedulerpolicy.SchedulerInt(run.Data["attempt"], 0) + 1
	run.Data["fencing_token"] = previousFencingToken + 1
	run.Data["idempotency_scope"] = "scheduler.retry"
	run.Data["last_command_scope"], run.Data["last_command_key"] = "scheduler.retry", commandKey
	run.UpdatedAt = now.Format(time.RFC3339)
	runObject, err := s.objectForPrincipal(ctx, principal, "job_run")
	if err != nil {
		return SchedulerOperationResult{}, err
	}
	conditions := map[string]any{"status": previousStatus}
	if previousFencingToken > 0 {
		conditions["fencing_token"] = previousFencingToken
	}
	eventObject, err := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "job_run_event")
	if err != nil {
		return SchedulerOperationResult{}, err
	}
	if err := s.repository.CommitRecordMutationBatch(ctx, principal.WorkspaceID, []transactionmodel.RecordMutationCommit{
		{Operation: "update", Object: runObject, Record: run, Optimistic: transactionmodel.OptimisticPrecondition{ExpectedUpdatedAt: previousUpdatedAt}, Conditions: conditions},
		{Operation: "create", Object: eventObject, Record: schedulerRunEventRecord(run.ID, "retry_scheduled", "Scheduler run retry requested.", now, map[string]any{"attempt": run.Data["attempt"]})},
	}); err != nil {
		if mutation.IsMutationConflict(err, mutation.MutationConflictOptimistic) {
			return SchedulerOperationResult{}, conflict("backend.scheduler.retry_conflict")
		}
		return SchedulerOperationResult{}, err
	}
	result, err := s.processClaimedRun(ctx, principal.WorkspaceID, definition, run, 25, workflowWorkerPrincipal(), now)
	if err != nil {
		return SchedulerOperationResult{}, err
	}
	finished, _, _ := s.repository.GetRecord(ctx, principal.WorkspaceID, runObject, run.ID)
	s.insertOperationAudit(ctx, "scheduler_run_retry_requested", "job_run", finished.ID, principal, "Retried scheduler run "+run.ID, before, finished.Data, map[string]any{
		"scheduler_definition_key": fmt.Sprint(finished.Data["scheduler_definition_key"]),
		"attempt":                  finished.Data["attempt"],
	})
	return SchedulerOperationResult{Status: "retried", Message: "backend.scheduler.retry_completed", Run: finished, Result: result}, nil
}

func (s *SchedulerApplicationService) CancelRun(ctx context.Context, runID, callerKey string, principal principalmodel.Principal) (SchedulerOperationResult, error) {
	if strings.TrimSpace(callerKey) == "" {
		return SchedulerOperationResult{}, badRequest(idempotency.ErrorCodeMissingKey)
	}
	if err := schedulerOpsCommandAllowed(principal); err != nil {
		return SchedulerOperationResult{}, err
	}
	run, _, err := s.schedulerRunAndDefinition(ctx, runID, principal)
	if err != nil {
		return SchedulerOperationResult{}, err
	}
	commandKey := schedulerCommandIdempotencyKey("cancel", run.ID, callerKey)
	if strings.TrimSpace(fmt.Sprint(run.Data["last_command_scope"])) == "scheduler.cancel" && strings.TrimSpace(fmt.Sprint(run.Data["last_command_key"])) == commandKey {
		return SchedulerOperationResult{Status: "replayed", Message: "backend.scheduler.cancel_replayed", Run: run}, nil
	}
	now := s.worker.Clock.Now()
	before := recordvalidation.RecordCloneData(run.Data)
	previousStatus, previousUpdatedAt := strings.TrimSpace(fmt.Sprint(run.Data["status"])), run.UpdatedAt
	previousFencingToken := schedulerpolicy.SchedulerInt(run.Data["fencing_token"], 0)
	run.Data["status"] = "cancelled"
	run.Data["lease_owner"] = ""
	run.Data["lease_expires_at"] = ""
	run.Data["fencing_token"] = previousFencingToken + 1
	run.Data["finished_at"] = now.Format(time.RFC3339)
	run.Data["last_command_scope"], run.Data["last_command_key"] = "scheduler.cancel", commandKey
	run.UpdatedAt = now.Format(time.RFC3339)
	runObject, err := s.objectForPrincipal(ctx, principal, "job_run")
	if err != nil {
		return SchedulerOperationResult{}, err
	}
	eventObject, err := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "job_run_event")
	if err != nil {
		return SchedulerOperationResult{}, err
	}
	conditions := map[string]any{"status": previousStatus}
	if previousFencingToken > 0 {
		conditions["fencing_token"] = previousFencingToken
	}
	if err := s.repository.CommitRecordMutationBatch(ctx, principal.WorkspaceID, []transactionmodel.RecordMutationCommit{
		{Operation: "update", Object: runObject, Record: run, Optimistic: transactionmodel.OptimisticPrecondition{ExpectedUpdatedAt: previousUpdatedAt}, Conditions: conditions},
		{Operation: "create", Object: eventObject, Record: schedulerRunEventRecord(run.ID, "cancelled", "Scheduler run cancelled.", now, map[string]any{"cancelled_by": principal.UserID})},
	}); err != nil {
		return SchedulerOperationResult{}, err
	}
	s.insertOperationAudit(ctx, "scheduler_run_cancel_requested", "job_run", run.ID, principal, "Cancelled scheduler run "+run.ID, before, run.Data, map[string]any{
		"scheduler_definition_key": fmt.Sprint(run.Data["scheduler_definition_key"]),
	})
	return SchedulerOperationResult{Status: "cancelled", Message: "backend.scheduler.cancelled", Run: run}, nil
}

func (s *SchedulerApplicationService) ResolveDeadLetter(ctx context.Context, deadLetterID, note, callerKey string, principal principalmodel.Principal) (SchedulerOperationResult, error) {
	if strings.TrimSpace(callerKey) == "" {
		return SchedulerOperationResult{}, badRequest(idempotency.ErrorCodeMissingKey)
	}
	if err := schedulerOpsCommandAllowed(principal); err != nil {
		return SchedulerOperationResult{}, err
	}
	object, err := s.objectForPrincipal(ctx, principal, "job_dead_letter")
	if err != nil {
		return SchedulerOperationResult{}, err
	}
	deadLetter, ok, err := s.repository.GetRecord(ctx, principal.WorkspaceID, object, strings.TrimSpace(deadLetterID))
	if err != nil {
		return SchedulerOperationResult{}, internalError("get scheduler dead letter", err)
	}
	if !ok {
		return SchedulerOperationResult{}, notFound("backend.scheduler.dead_letter_not_found")
	}
	commandKey := schedulerCommandIdempotencyKey("dead_letter.resolve", deadLetter.ID, callerKey)
	if strings.TrimSpace(fmt.Sprint(deadLetter.Data["resolution_idempotency_key"])) == commandKey {
		return SchedulerOperationResult{Status: "replayed", Message: "backend.scheduler.dead_letter_resolve_replayed", Run: deadLetter}, nil
	}
	now := s.worker.Clock.Now()
	before := recordvalidation.RecordCloneData(deadLetter.Data)
	previousStatus, previousUpdatedAt := strings.TrimSpace(fmt.Sprint(deadLetter.Data["status"])), deadLetter.UpdatedAt
	deadLetter.Data["status"] = "resolved"
	deadLetter.Data["resolved_at"] = now.Format(time.RFC3339)
	deadLetter.Data["resolved_by"] = principal.UserID
	deadLetter.Data["resolution_note"] = strings.TrimSpace(note)
	deadLetter.Data["resolution_idempotency_key"] = commandKey
	deadLetter.UpdatedAt = now.Format(time.RFC3339)
	commits := []transactionmodel.RecordMutationCommit{{Operation: "update", Object: object, Record: deadLetter, Optimistic: transactionmodel.OptimisticPrecondition{ExpectedUpdatedAt: previousUpdatedAt}, Conditions: map[string]any{"status": previousStatus}}}
	if runID := existingStringBefore(deadLetter, "job_run_id"); runID != "" {
		eventObject, eventErr := s.objectForPrincipal(ctx, workflowWorkerPrincipal(), "job_run_event")
		if eventErr != nil {
			return SchedulerOperationResult{}, eventErr
		}
		commits = append(commits, transactionmodel.RecordMutationCommit{Operation: "create", Object: eventObject, Record: schedulerRunEventRecord(runID, "dead_letter_resolved", "Scheduler dead letter resolved.", now, map[string]any{"dead_letter_id": deadLetter.ID, "resolved_by": principal.UserID})})
	}
	if err := s.repository.CommitRecordMutationBatch(ctx, principal.WorkspaceID, commits); err != nil {
		return SchedulerOperationResult{}, err
	}
	s.insertOperationAudit(ctx, "scheduler_dead_letter_resolved", "job_dead_letter", deadLetter.ID, principal, "Resolved scheduler dead letter "+deadLetter.ID, before, deadLetter.Data, map[string]any{
		"job_run_id":               existingStringBefore(deadLetter, "job_run_id"),
		"scheduler_definition_key": existingStringBefore(deadLetter, "scheduler_definition_key"),
		"reason":                   strings.TrimSpace(note),
	})
	return SchedulerOperationResult{Status: "resolved", Message: "backend.scheduler.dead_letter_resolved", Run: deadLetter}, nil
}

// RequeueDeadLetter retries the owning run and then resolves the dead-letter
// marker. Both steps derive stable child keys from the caller key. If the
// resolve step fails after a successful retry, replaying this command reuses
// the retry receipt and safely resumes resolution without running the job twice.
func (s *SchedulerApplicationService) RequeueDeadLetter(ctx context.Context, deadLetterID, note, callerKey string, principal principalmodel.Principal) (SchedulerOperationResult, error) {
	if strings.TrimSpace(callerKey) == "" {
		return SchedulerOperationResult{}, badRequest(idempotency.ErrorCodeMissingKey)
	}
	if err := schedulerOpsCommandAllowed(principal); err != nil {
		return SchedulerOperationResult{}, err
	}
	deadLetter, err := s.InspectDeadLetter(ctx, deadLetterID, principal)
	if err != nil {
		return SchedulerOperationResult{}, err
	}
	runID := strings.TrimSpace(fmt.Sprint(deadLetter.Data["job_run_id"]))
	if runID == "" || runID == "<nil>" {
		return SchedulerOperationResult{}, notFound("backend.scheduler.run_not_found")
	}
	retried, err := s.RetryRun(ctx, runID, strings.TrimSpace(callerKey)+":run", principal)
	if err != nil {
		return SchedulerOperationResult{}, err
	}
	if _, err := s.ResolveDeadLetter(ctx, deadLetter.ID, note, strings.TrimSpace(callerKey)+":resolve", principal); err != nil {
		return SchedulerOperationResult{}, err
	}
	retried.Status = "requeued"
	return retried, nil
}

func (s *SchedulerApplicationService) InspectDeadLetter(ctx context.Context, deadLetterID string, principal principalmodel.Principal) (recordmodel.Record, error) {
	if err := schedulerOpsReadAllowed(principal); err != nil {
		return recordmodel.Record{}, err
	}
	object, err := s.objectForPrincipal(ctx, principal, "job_dead_letter")
	if err != nil {
		return recordmodel.Record{}, err
	}
	deadLetter, found, err := s.repository.GetRecord(ctx, principal.WorkspaceID, object, strings.TrimSpace(deadLetterID))
	if err != nil {
		return recordmodel.Record{}, internalError("get scheduler dead letter", err)
	}
	if !found {
		return recordmodel.Record{}, notFound("backend.scheduler.dead_letter_not_found")
	}
	return deadLetter, nil
}

func (s *SchedulerApplicationService) schedulerDefinitionForOperation(ctx context.Context, definitionID string, principal principalmodel.Principal) (recordmodel.Record, error) {
	if err := schedulerOperationAllowed(principal); err != nil {
		return recordmodel.Record{}, err
	}
	if s.definitions == nil {
		return recordmodel.Record{}, schedulerError(apperror.KindUnavailable, "backend.scheduler.definition_source_unavailable", nil)
	}
	definition, ok, err := s.definitions.GetSchedulerDefinition(ctx, strings.TrimSpace(definitionID))
	if err != nil {
		return recordmodel.Record{}, internalError("get scheduler job definition", err)
	}
	if !ok {
		return recordmodel.Record{}, notFound("backend.scheduler.definition_not_found")
	}
	return definition, nil
}

func (s *SchedulerApplicationService) schedulerRunAndDefinition(ctx context.Context, runID string, principal principalmodel.Principal) (recordmodel.Record, recordmodel.Record, error) {
	runObject, err := s.objectForPrincipal(ctx, principal, "job_run")
	if err != nil {
		return recordmodel.Record{}, recordmodel.Record{}, err
	}
	run, ok, err := s.repository.GetRecord(ctx, principal.WorkspaceID, runObject, strings.TrimSpace(runID))
	if err != nil {
		return recordmodel.Record{}, recordmodel.Record{}, internalError("get scheduler run", err)
	}
	if !ok {
		return recordmodel.Record{}, recordmodel.Record{}, notFound("backend.scheduler.run_not_found")
	}
	definitionID := strings.TrimSpace(fmt.Sprint(run.Data["scheduler_definition_key"]))
	if s.definitions == nil {
		return recordmodel.Record{}, recordmodel.Record{}, schedulerError(apperror.KindUnavailable, "backend.scheduler.definition_source_unavailable", nil)
	}
	definition, ok, err := s.definitions.GetSchedulerDefinition(ctx, definitionID)
	if err != nil {
		return recordmodel.Record{}, recordmodel.Record{}, internalError("get scheduler run definition", err)
	}
	if !ok {
		return recordmodel.Record{}, recordmodel.Record{}, notFound("backend.scheduler.definition_not_found")
	}
	return run, definition, nil
}
