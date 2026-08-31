package scheduler

import (
	"context"
	"strings"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// ReportSnapshotRuntime is the target-owner boundary for Scheduler callbacks.
// Report owns refresh semantics and durable snapshot evidence.
type ReportSnapshotRuntime interface {
	RefreshSnapshot(context.Context, string, string, principalmodel.Principal) (reportmodel.ReportSnapshot, error)
}

func (s *SchedulerApplicationService) refreshScheduledReportSnapshot(ctx context.Context, workspaceID string, definition PublishedDefinition, idempotencyKey string, principal principalmodel.Principal) (string, error) {
	if s.reportSnapshots == nil {
		return "", badRequest("backend.scheduler.report_snapshot_runtime_unavailable", "target_type", "report_snapshot_refresh")
	}
	principal.WorkspaceID = workspaceID
	snapshot, err := s.reportSnapshots.RefreshSnapshot(ctx, strings.TrimSpace(definitionString(definition, "target_key")), strings.TrimSpace(idempotencyKey), principal)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(snapshot.ID), nil
}
