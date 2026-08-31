package scheduler

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type schedulerReportSnapshotRuntime struct {
	reportKey, idempotencyKey string
	principal                 principalmodel.Principal
	calls                     int
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
	evidence, err := service.schedulerProcessReportSnapshotDefinition(t.Context(), "workspace-a", recordmodel.Record{Data: map[string]any{"target_key": "operations"}}, recordmodel.Record{ID: "run-1", Data: map[string]any{"idempotency_key": "window-1"}}, principalmodel.Principal{Principal: identitysdk.Principal{UserID: "scheduler-user"}})
	if err != nil || runtime.calls != 1 || runtime.reportKey != "operations" || runtime.idempotencyKey != "window-1" || runtime.principal.WorkspaceID != "workspace-a" || len(evidence) != 1 || evidence[0].Kind != "report_snapshot_refreshed" || evidence[0].RecordID != "snapshot-1" {
		t.Fatalf("runtime=%#v evidence=%#v err=%v", runtime, evidence, err)
	}
	service.UseReportSnapshotRuntime(nil)
	if _, err := service.schedulerProcessReportSnapshotDefinition(t.Context(), "workspace-a", recordmodel.Record{}, recordmodel.Record{}, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.scheduler.report_snapshot_runtime_unavailable" {
		t.Fatalf("missing runtime err=%v", err)
	}
}
