// Package schedulermodulehost contains Runtime adapters used by the extracted
// Scheduler owner. It executes only authenticated downstream owner callbacks;
// Scheduler retains clocks, claims, retries, runs, events, and dead letters.
package schedulermodulehost

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type PublishedDefinition struct {
	Key  string
	Data map[string]any
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

type ReportSnapshotRuntime interface {
	RefreshSnapshot(context.Context, string, string, principalmodel.Principal) (reportmodel.ReportSnapshot, error)
}

type DownstreamDispatcher struct {
	workflows       ScheduledWorkflowRuntime
	reportSnapshots ReportSnapshotRuntime
}

func NewDownstreamDispatcher(workflows ScheduledWorkflowRuntime) *DownstreamDispatcher {
	return &DownstreamDispatcher{workflows: workflows}
}

func (d *DownstreamDispatcher) UseReportSnapshotRuntime(runtime ReportSnapshotRuntime) {
	if d != nil {
		d.reportSnapshots = runtime
	}
}

func (d *DownstreamDispatcher) Dispatch(ctx context.Context, definition PublishedDefinition, runID string, scheduledFor time.Time, limit int, principal principalmodel.Principal) (string, error) {
	if d == nil || d.workflows == nil {
		return "", schedulerHostError(apperror.KindUnavailable, "backend.scheduler.runtime_operation_executor_unavailable")
	}
	if limit <= 0 {
		limit = 25
	}
	workspaceID := schedulerWorkspaceID(principal)
	principal.WorkspaceID = workspaceID
	runID = strings.TrimSpace(runID)
	switch definitionString(definition, "target_type") {
	case "workflow":
		result, err := d.processWorkflowTarget(ctx, definition, scheduledFor, limit, principal)
		if err != nil {
			return "", err
		}
		if len(result.Executions) > 0 && strings.TrimSpace(result.Executions[0].ID) != "" {
			return result.Executions[0].ID, nil
		}
		return runID, nil
	case "report_snapshot_refresh":
		receiptID, err := d.refreshReportSnapshot(ctx, workspaceID, definition, runID, principal)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(receiptID) != "" {
			return receiptID, nil
		}
		return runID, nil
	default:
		return "", schedulerHostError(apperror.KindBadRequest, "backend.scheduler.unsupported_target_type", "target_type", definitionString(definition, "target_type"))
	}
}

func (d *DownstreamDispatcher) processWorkflowTarget(ctx context.Context, definition PublishedDefinition, scheduledFor time.Time, limit int, principal principalmodel.Principal) (workflowmodel.WorkflowProcessResult, error) {
	target := definitionString(definition, "target_key")
	if windowed, ok := d.workflows.(WindowedScheduledWorkflowRuntime); ok && !scheduledFor.IsZero() {
		return windowed.ProcessDueWorkflowExecutionsForScheduledWindow(ctx, target, scheduledFor.UTC(), limit, principal)
	}
	if targeted, ok := d.workflows.(TargetedScheduledWorkflowRuntime); ok {
		return targeted.ProcessDueWorkflowExecutionsForTarget(ctx, target, limit, principal)
	}
	return d.workflows.ProcessDueWorkflowExecutions(ctx, limit, principal)
}

func (d *DownstreamDispatcher) refreshReportSnapshot(ctx context.Context, workspaceID string, definition PublishedDefinition, idempotencyKey string, principal principalmodel.Principal) (string, error) {
	if d.reportSnapshots == nil {
		return "", schedulerHostError(apperror.KindBadRequest, "backend.scheduler.report_snapshot_runtime_unavailable", "target_type", "report_snapshot_refresh")
	}
	principal.WorkspaceID = workspaceID
	snapshot, err := d.reportSnapshots.RefreshSnapshot(ctx, definitionString(definition, "target_key"), strings.TrimSpace(idempotencyKey), principal)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(snapshot.ID), nil
}

func definitionString(definition PublishedDefinition, key string) string {
	value := strings.TrimSpace(fmt.Sprint(definition.Data[key]))
	if value == "<nil>" {
		return ""
	}
	return value
}

func schedulerWorkspaceID(principal principalmodel.Principal) string {
	if workspaceID, err := principalmodel.NewWorkspaceID(principal.WorkspaceID); err == nil {
		return workspaceID.String()
	}
	if principal.SystemScope.Valid() {
		return principalmodel.InstallationWorkspaceID
	}
	return ""
}

func schedulerHostError(kind apperror.ErrorKind, code string, params ...string) error {
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		if key := strings.TrimSpace(params[index]); key != "" {
			values[key] = params[index+1]
		}
	}
	if len(values) == 0 {
		values = nil
	}
	return &apperror.AppError{Kind: kind, Code: code, Params: values}
}
