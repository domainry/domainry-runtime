package scheduler

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	schedulermodulehost "github.com/domainry/domainry-runtime/runtime/modulehost/scheduler"
	"github.com/domainry/domainry-scheduler-sdk/schedule"
)

type SchedulerDefinitionSimulation struct {
	Status     string `json:"status"`
	Message    string `json:"message,omitempty"`
	NextRunAt  string `json:"next_run_at,omitempty"`
	TargetType string `json:"target_type,omitempty"`
	TargetKey  string `json:"target_key,omitempty"`
}

type ScheduledWorkflowRuntime = schedulermodulehost.ScheduledWorkflowRuntime
type TargetedScheduledWorkflowRuntime = schedulermodulehost.TargetedScheduledWorkflowRuntime
type WindowedScheduledWorkflowRuntime = schedulermodulehost.WindowedScheduledWorkflowRuntime

func (s *SchedulerApplicationService) SimulateManagementDefinition(ctx context.Context, definitionID string, principal principalmodel.Principal) (SchedulerDefinitionSimulation, error) {
	if err := schedulerExactQueryAllowed(principal, ActionSimulateSchedulerJob); err != nil {
		return SchedulerDefinitionSimulation{}, err
	}
	definition, err := s.schedulerDefinition(ctx, definitionID)
	if err != nil {
		return SchedulerDefinitionSimulation{}, err
	}
	nextRuns, err := schedule.PreviewDefinitionData(ctx, definition.Data, s.clock.Now(), 1)
	if err != nil {
		return SchedulerDefinitionSimulation{}, apperror.FromError(apperror.KindBadRequest, err)
	}
	next := nextRuns[0]
	return SchedulerDefinitionSimulation{
		Status: "simulated", Message: "backend.scheduler.simulated", NextRunAt: next.UTC().Format(time.RFC3339),
		TargetType: schedulerDefinitionTargetType(definition), TargetKey: strings.TrimSpace(fmt.Sprint(definition.Data["target_key"])),
	}, nil
}

func schedulerDefinitionTargetType(definition PublishedDefinition) string {
	return strings.TrimSpace(fmt.Sprint(definition.Data["target_type"]))
}
