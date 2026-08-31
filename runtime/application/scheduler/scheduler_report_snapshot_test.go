package scheduler

import (
	"context"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type schedulerReportSnapshotRuntime struct {
	reportKey, idempotencyKey string
	principal                 principalmodel.Principal
	calls                     int
}

func TestSchedulerRejectsReportExportInsteadOfSynthesizingReportEvidence(t *testing.T) {
	service := NewSchedulerApplicationService(schedulerAuthoringRuntime{})
	_, err := service.DispatchOwnedTrigger(
		t.Context(),
		PublishedDefinition{Key: "nightly-export", Data: map[string]any{"target_type": "report_export", "target_key": "orders"}},
		"run-1",
		time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC),
		25,
		principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "scheduler-user"}},
	)
	if apperror.CodeOf(err) != "backend.scheduler.unsupported_target_type" {
		t.Fatalf("report export dispatch err=%v", err)
	}
}

func (r *schedulerReportSnapshotRuntime) RefreshSnapshot(_ context.Context, reportKey, idempotencyKey string, principal principalmodel.Principal) (reportmodel.ReportSnapshot, error) {
	r.calls++
	r.reportKey, r.idempotencyKey, r.principal = reportKey, idempotencyKey, principal
	return reportmodel.ReportSnapshot{ID: "snapshot-1", Status: "succeeded"}, nil
}

func TestSchedulerReportSnapshotTargetDelegatesWithDurableRunIdentity(t *testing.T) {
	runtime := &schedulerReportSnapshotRuntime{}
	service := &SchedulerApplicationService{}
	service.UseReportSnapshotRuntime(runtime)
	receiptID, err := service.refreshScheduledReportSnapshot(t.Context(), "workspace-a", PublishedDefinition{Data: map[string]any{"target_key": "operations"}}, "window-1", principalmodel.Principal{Principal: identitysdk.Principal{UserID: "scheduler-user"}})
	if err != nil || runtime.calls != 1 || runtime.reportKey != "operations" || runtime.idempotencyKey != "window-1" || runtime.principal.WorkspaceID != "workspace-a" || receiptID != "snapshot-1" {
		t.Fatalf("runtime=%#v receipt=%q err=%v", runtime, receiptID, err)
	}
	service.UseReportSnapshotRuntime(nil)
	if _, err := service.refreshScheduledReportSnapshot(t.Context(), "workspace-a", PublishedDefinition{}, "", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.scheduler.report_snapshot_runtime_unavailable" {
		t.Fatalf("missing runtime err=%v", err)
	}
}
