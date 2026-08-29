package scheduler

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	schedulerpolicy "github.com/domainry/domainry-runtime/runtime/domain/scheduler/policy"
	schedulervalidation "github.com/domainry/domainry-runtime/runtime/domain/scheduler/validation"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

// SchedulerOperationResult is retained only for definition preview. Durable
// run and dead-letter state is returned by domainry-scheduler-sdk.
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

type WindowedTargetedSchedulerOperationRuntime interface {
	ProcessDueWorkflowExecutionsForScheduledWindow(context.Context, string, time.Time, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error)
}

type RecordTimerExecution struct {
	TimerID, WorkspaceID, ObjectKey, RecordID string
	TargetType, TargetKey                     string
	Payload                                   map[string]any
	IdempotencyKey                            string
}

type RecordTimerTargetRuntime interface {
	ExecuteRecordTimer(context.Context, RecordTimerExecution, principalmodel.Principal) error
}

func (s *SchedulerApplicationService) SimulateTenantAdminDefinition(ctx context.Context, definitionID string, principal principalmodel.Principal) (SchedulerOperationResult, error) {
	if err := schedulerDefinitionWriteAllowed(principal); err != nil {
		return SchedulerOperationResult{}, err
	}
	definition, err := s.schedulerDefinitionForOperation(ctx, definitionID, principal)
	if err != nil {
		return SchedulerOperationResult{}, err
	}
	if err := schedulervalidation.SchedulerValidateDefinitionContract(ctx, definition.Data); err != nil {
		return SchedulerOperationResult{}, err
	}
	next := schedulerpolicy.SchedulerScheduleNextRunAt(definition, s.worker.Clock.Now())
	return SchedulerOperationResult{Status: "simulated", Message: "backend.scheduler.simulated", Run: recordmodel.Record{
		ID: "preview_" + schedulerpolicy.SchedulerSlug(definition.ID), Data: map[string]any{
			"scheduler_definition_key": definition.ID, "status": "preview", "scheduled_for": next.UTC().Format(time.RFC3339),
			"target_type": schedulerDefinitionTargetType(definition), "target_key": strings.TrimSpace(fmt.Sprint(definition.Data["target_key"])),
		},
	}}, nil
}

func (s *SchedulerApplicationService) SimulateJob(ctx context.Context, definitionID string, principal principalmodel.Principal) (SchedulerOperationResult, error) {
	return s.SimulateTenantAdminDefinition(ctx, definitionID, principal)
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

func schedulerDefinitionTargetType(definition recordmodel.Record) string {
	return strings.TrimSpace(fmt.Sprint(definition.Data["target_type"]))
}
