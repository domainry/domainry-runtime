package scheduler

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"github.com/domainry/domainry-scheduler-sdk/schedule"
)

type SchedulerDefinitionSimulation struct {
	Status     string `json:"status"`
	Message    string `json:"message,omitempty"`
	NextRunAt  string `json:"next_run_at,omitempty"`
	TargetType string `json:"target_type,omitempty"`
	TargetKey  string `json:"target_key,omitempty"`
}

type ScheduledWorkflowRuntime interface {
	ProcessDueWorkflowExecutions(context.Context, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error)
}

type TargetedScheduledWorkflowRuntime interface {
	ProcessDueWorkflowExecutionsForTarget(context.Context, string, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error)
}

type WindowedScheduledWorkflowRuntime interface {
	ProcessDueWorkflowExecutionsForScheduledWindow(context.Context, string, time.Time, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error)
}

func (s *SchedulerApplicationService) SimulateTenantAdminDefinition(ctx context.Context, definitionID string, principal principalmodel.Principal) (SchedulerDefinitionSimulation, error) {
	if err := schedulerDefinitionWriteAllowed(principal); err != nil {
		return SchedulerDefinitionSimulation{}, err
	}
	definition, err := s.schedulerDefinitionForOperation(ctx, definitionID, principal)
	if err != nil {
		return SchedulerDefinitionSimulation{}, err
	}
	if err := validateSchedulerDefinitionContract(ctx, definition.Data); err != nil {
		return SchedulerDefinitionSimulation{}, err
	}
	next := schedule.NextSchedule(schedulerScheduleFromData(definition.Data), s.clock.Now())
	return SchedulerDefinitionSimulation{
		Status: "simulated", Message: "backend.scheduler.simulated", NextRunAt: next.UTC().Format(time.RFC3339),
		TargetType: schedulerDefinitionTargetType(definition), TargetKey: strings.TrimSpace(fmt.Sprint(definition.Data["target_key"])),
	}, nil
}

func (s *SchedulerApplicationService) schedulerDefinitionForOperation(ctx context.Context, definitionID string, principal principalmodel.Principal) (PublishedDefinition, error) {
	if err := schedulerOperationAllowed(principal); err != nil {
		return PublishedDefinition{}, err
	}
	if s.definitions == nil {
		return PublishedDefinition{}, schedulerError(apperror.KindUnavailable, "backend.scheduler.definition_source_unavailable", nil)
	}
	definition, ok, err := s.definitions.GetSchedulerDefinition(ctx, strings.TrimSpace(definitionID))
	if err != nil {
		return PublishedDefinition{}, internalError("get scheduler definition", err)
	}
	if !ok {
		return PublishedDefinition{}, notFound("backend.scheduler.definition_not_found")
	}
	return definition, nil
}

func schedulerDefinitionTargetType(definition PublishedDefinition) string {
	return strings.TrimSpace(fmt.Sprint(definition.Data["target_type"]))
}
