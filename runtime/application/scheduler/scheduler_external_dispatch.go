package scheduler

import (
	"context"
	"fmt"
	"strings"
	"time"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

// DispatchOwnedTrigger executes downstream Runtime semantics for a run whose
// lifecycle and durable evidence are owned by the extracted Scheduler. It must
// not create, lease or finish Scheduler-owned run records.
func (s *SchedulerApplicationService) DispatchOwnedTrigger(ctx context.Context, definition recordmodel.Record, runID string, scheduledFor time.Time, limit int, principal principalmodel.Principal) (string, error) {
	if s == nil || s.runtime == nil {
		return "", schedulerErrorUnavailable("backend.scheduler.runtime_operation_executor_unavailable")
	}
	if limit <= 0 {
		limit = 25
	}
	workspaceID := schedulerWorkspaceID(principal)
	principal.WorkspaceID = workspaceID
	run := recordmodel.Record{ID: strings.TrimSpace(runID), CreatedAt: scheduledFor.UTC().Format(time.RFC3339Nano), Data: map[string]any{
		"scheduled_for": scheduledFor.UTC().Format(time.RFC3339Nano), "checkpoint_cursor": "", "checkpoint_processed": 0,
	}}
	switch schedulerDefinitionTargetType(definition) {
	case "workflow":
		result, err := s.processWorkflowTarget(ctx, definition, run, limit, principal)
		if err != nil {
			return "", err
		}
		if len(result.Executions) > 0 && strings.TrimSpace(result.Executions[0].ID) != "" {
			return result.Executions[0].ID, nil
		}
		return run.ID, nil
	case "report_export":
		evidence, err := s.schedulerProcessReportExportDefinition(ctx, workspaceID, definition, run, scheduledFor)
		if err != nil {
			return "", err
		}
		if len(evidence) > 0 {
			return valueOrDefault(strings.TrimSpace(evidence[len(evidence)-1].RecordID), run.ID), nil
		}
		return run.ID, nil
	case "report_snapshot_refresh":
		evidence, err := s.schedulerProcessReportSnapshotDefinition(ctx, workspaceID, definition, run, principal)
		if err != nil {
			return "", err
		}
		if len(evidence) > 0 {
			return valueOrDefault(strings.TrimSpace(evidence[len(evidence)-1].RecordID), run.ID), nil
		}
		return run.ID, nil
	default:
		return "", badRequest("backend.scheduler.unsupported_target_type", "target_type", schedulerDefinitionTargetType(definition))
	}
}

func (s *SchedulerApplicationService) processWorkflowTarget(ctx context.Context, definition, run recordmodel.Record, limit int, principal principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	target := strings.TrimSpace(fmt.Sprint(definition.Data["target_key"]))
	scheduledFor, parseErr := time.Parse(time.RFC3339Nano, existingStringBefore(run, "scheduled_for"))
	if windowed, ok := s.runtime.(WindowedTargetedSchedulerOperationRuntime); ok && parseErr == nil {
		return windowed.ProcessDueWorkflowExecutionsForScheduledWindow(ctx, target, scheduledFor.UTC(), limit, principal)
	}
	if targeted, ok := s.runtime.(TargetedSchedulerOperationRuntime); ok {
		return targeted.ProcessDueWorkflowExecutionsForTarget(ctx, target, limit, principal)
	}
	return s.runtime.ProcessDueWorkflowExecutions(ctx, limit, principal)
}
