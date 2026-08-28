package scheduler

import (
	"context"
	"fmt"
	"strings"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"github.com/domainry/domainry-runtime/runtime/platform/logging"
	"go.uber.org/zap"
)

// RuntimeAvailable reports whether the metadata schema exposes the complete
// Scheduler persistence surface required by the application use case.
func (s *SchedulerApplicationService) RuntimeAvailable(ctx context.Context, principal principalmodel.Principal) bool {
	if s == nil || s.schema == nil || s.definitions == nil {
		return false
	}
	if schedulerAuthorizeQuery(principal) != nil {
		return false
	}
	available := map[string]bool{}
	// Runtime provisioning is an owner-internal infrastructure fact. The caller
	// is authorized above, but their presentation-level object visibility must
	// not make a healthy Scheduler look unprovisioned.
	for _, object := range s.schema.SchemaForPrincipal(ctx, workflowWorkerPrincipal()).Objects {
		available[object.Key] = true
	}
	for _, key := range []string{"scheduler_cursor", "job_run", "job_run_event", "job_dead_letter"} {
		if !available[key] {
			return false
		}
	}
	return true
}

type WindowedTargetedSchedulerOperationRuntime interface {
	ProcessDueWorkflowExecutionsForScheduledWindow(context.Context, string, time.Time, int, principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error)
}

type CheckpointedWindowedSchedulerOperationRuntime interface {
	ProcessScheduledWorkflowWindowPage(context.Context, string, time.Time, int, string, principalmodel.Principal) (workflowmodel.WorkflowScheduledPage, error)
}

func (s *SchedulerApplicationService) ownerObject(ctx context.Context, objectKey string) (definitionmodel.ObjectSchema, error) {
	return s.objectForPrincipal(ctx, workflowWorkerPrincipal(), objectKey)
}

func schedulerRunDoesNotAdvanceCursor(run recordmodel.Record, status string) bool {
	return status == "retrying" || strings.TrimSpace(fmt.Sprint(run.Data["triggered_by"])) == "manual_run"
}

func (s *SchedulerApplicationService) provisionWorkerDefinitions(ctx context.Context) bool {
	if !s.RuntimeAvailable(ctx, workflowWorkerPrincipal()) {
		return true
	}
	provisioned, err := s.provisionPublishedDefinitions(ctx, principalmodel.InstallationWorkspaceID, s.worker.Clock.Now())
	if err != nil {
		logging.FromContext(ctx).Error("scheduler definition provisioning failed", logging.StableErrorFields(err)...)
		return false
	}
	if provisioned > 0 {
		logging.FromContext(ctx).Info("scheduler definitions provisioned", zap.Int("provisioned", provisioned))
	}
	return true
}

func (s *SchedulerApplicationService) processWorkflowTarget(ctx context.Context, definition, run recordmodel.Record, limit int, principal principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	target := strings.TrimSpace(fmt.Sprint(definition.Data["target_key"]))
	scheduledFor, parseErr := time.Parse(time.RFC3339, existingStringBefore(run, "scheduled_for"))
	if windowed, ok := s.runtime.(WindowedTargetedSchedulerOperationRuntime); ok && parseErr == nil {
		return windowed.ProcessDueWorkflowExecutionsForScheduledWindow(ctx, target, scheduledFor.UTC(), limit, principal)
	}
	if targeted, ok := s.runtime.(TargetedSchedulerOperationRuntime); ok {
		return targeted.ProcessDueWorkflowExecutionsForTarget(ctx, target, limit, principal)
	}
	return s.runtime.ProcessDueWorkflowExecutions(ctx, limit, principal)
}

func (s *SchedulerApplicationService) processWorkflowTargetPage(ctx context.Context, definition, run recordmodel.Record, limit int, principal principalmodel.Principal) (workflowmodel.WorkflowScheduledPage, bool, error) {
	if strings.TrimSpace(fmt.Sprint(run.Data["triggered_by"])) == "manual_run" {
		return workflowmodel.WorkflowScheduledPage{}, false, nil
	}
	windowed, ok := s.runtime.(CheckpointedWindowedSchedulerOperationRuntime)
	if !ok {
		return workflowmodel.WorkflowScheduledPage{}, false, nil
	}
	scheduledFor, err := time.Parse(time.RFC3339, existingStringBefore(run, "scheduled_for"))
	if err != nil {
		return workflowmodel.WorkflowScheduledPage{}, false, nil
	}
	page, err := windowed.ProcessScheduledWorkflowWindowPage(ctx, strings.TrimSpace(fmt.Sprint(definition.Data["target_key"])), scheduledFor.UTC(), limit, existingStringBefore(run, "checkpoint_cursor"), principal)
	return page, true, err
}

func (s *SchedulerApplicationService) ProcessClaimedRun(ctx context.Context, definition recordmodel.Record, run recordmodel.Record, limit int, principal principalmodel.Principal, now time.Time) (workflowmodel.WorkflowProcessResult, error) {
	if err := schedulerAuthorizeCommand(principal); err != nil {
		return workflowmodel.WorkflowProcessResult{}, err
	}
	workspaceID := schedulerWorkspaceID(principal)
	principal.WorkspaceID = workspaceID
	return s.processClaimedRun(ctx, workspaceID, definition, run, limit, principal, now)
}
