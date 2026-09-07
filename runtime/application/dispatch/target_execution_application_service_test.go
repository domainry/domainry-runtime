package dispatch

import (
	"context"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type windowedWorkflowRuntime struct {
	target         string
	idempotencyKey string
	dueAt          time.Time
	limit          int
}

func (w *windowedWorkflowRuntime) ExecuteWorkflowTarget(_ context.Context, request WorkflowTargetRequest) (workflowmodel.WorkflowProcessResult, error) {
	w.target, w.idempotencyKey, w.dueAt, w.limit = request.Operation, request.IdempotencyKey, request.EffectiveAt, request.Limit
	return workflowmodel.WorkflowProcessResult{Executions: []workflowmodel.WorkflowExecution{{ID: "workflow-execution-1"}}}, nil
}

type reportSnapshotRuntime struct{ calls int }

func (r *reportSnapshotRuntime) RefreshSnapshot(context.Context, string, string, principalmodel.Principal) (reportmodel.ReportSnapshot, error) {
	r.calls++
	return reportmodel.ReportSnapshot{ID: "snapshot-1", Status: "succeeded"}, nil
}

func TestExecuteRoutesResolvedWorkflowTargetWithoutScheduleDefinitionLookup(t *testing.T) {
	workflows := &windowedWorkflowRuntime{}
	service := NewTargetExecutionApplicationService(workflows)
	dueAt := time.Date(2026, time.September, 1, 2, 3, 4, 0, time.FixedZone("test", 8*60*60))
	receipt, err := service.Execute(t.Context(), ExecutionRequest{ExecutionID: "execution-1", IdempotencyKey: "key-1", DueAt: dueAt, Target: Target{Owner: "workflow", Operation: "scheduled:orders.sync"}, Principal: principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}})
	if err != nil || receipt.ID != "workflow-execution-1" || workflows.target != "scheduled:orders.sync" || workflows.idempotencyKey != "key-1" || !workflows.dueAt.Equal(dueAt.UTC()) || workflows.limit != 25 {
		t.Fatalf("receipt=%#v workflows=%#v err=%v", receipt, workflows, err)
	}
}

func TestExecuteRoutesReportTargetAndRejectsUnknownOwner(t *testing.T) {
	service := NewTargetExecutionApplicationService(&windowedWorkflowRuntime{})
	reports := &reportSnapshotRuntime{}
	service.UseReportSnapshotRuntime(reports)
	receipt, err := service.Execute(t.Context(), ExecutionRequest{ExecutionID: "execution-1", IdempotencyKey: "key-1", Target: Target{Owner: "report_snapshot_refresh", Operation: "operations"}, Principal: principalmodel.NewSystemPrincipal("caller", principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "execute target"))})
	if err != nil || receipt.ID != "snapshot-1" || reports.calls != 1 {
		t.Fatalf("receipt=%#v calls=%d err=%v", receipt, reports.calls, err)
	}
	_, err = service.Execute(t.Context(), ExecutionRequest{Target: Target{Owner: "report_export"}})
	if apperror.CodeOf(err) != "backend.dispatch.unsupported_target_owner" {
		t.Fatalf("unsupported owner err=%v", err)
	}
}
