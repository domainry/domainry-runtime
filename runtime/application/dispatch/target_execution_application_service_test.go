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

type agentTargetRuntime struct {
	request AgentTargetRequest
	calls   int
}

type notificationTargetRuntime struct {
	request NotificationTargetRequest
	calls   int
}

func (n *notificationTargetRuntime) ExecuteNotificationTarget(_ context.Context, request NotificationTargetRequest) (NotificationTargetReceipt, error) {
	n.calls++
	n.request = request
	return NotificationTargetReceipt{ID: "notification-event-1", Status: "queued"}, nil
}

func (a *agentTargetRuntime) ExecuteAgentTarget(_ context.Context, request AgentTargetRequest) (AgentTargetReceipt, error) {
	a.calls++
	a.request = request
	return AgentTargetReceipt{ID: "agent-task-1", Status: "accepted"}, nil
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

func TestExecuteRoutesAgentTargetWithoutWorkflowDependency(t *testing.T) {
	service := NewTargetExecutionApplicationService(nil)
	agent := &agentTargetRuntime{}
	service.UseAgentTargetRuntime(agent)
	dueAt := time.Date(2026, time.September, 12, 9, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	payload := []byte(`{"contract_version":"domainry-scheduler-plan-dispatch-v1"}`)
	receipt, err := service.Execute(t.Context(), ExecutionRequest{ExecutionID: "scheduler-run", IdempotencyKey: "window", DueAt: dueAt, Target: Target{Owner: "agent", Operation: "conversation_task_start", Payload: payload}})
	if err != nil || receipt != (ExecutionReceipt{ID: "agent-task-1", Owner: "agent", Status: "accepted"}) || agent.calls != 1 || agent.request.Operation != "conversation_task_start" || !agent.request.ScheduledFor.Equal(dueAt.UTC()) || string(agent.request.Payload) != string(payload) {
		t.Fatalf("receipt=%+v calls=%d request=%+v err=%v", receipt, agent.calls, agent.request, err)
	}
	payload[0] = 'x'
	if string(agent.request.Payload) == string(payload) {
		t.Fatal("Agent target retained caller payload backing storage")
	}
	workflowService := NewTargetExecutionApplicationService(nil)
	if _, err = workflowService.Execute(t.Context(), ExecutionRequest{Target: Target{Owner: "workflow"}}); apperror.CodeOf(err) != "backend.dispatch.workflow_target_unavailable" {
		t.Fatalf("missing workflow runtime err=%v", err)
	}
}

func TestExecuteRoutesNotificationTargetWithoutOwningNotificationState(t *testing.T) {
	service := NewTargetExecutionApplicationService(nil)
	notifications := &notificationTargetRuntime{}
	service.UseNotificationTargetRuntime(notifications)
	dueAt := time.Date(2026, time.September, 13, 9, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60))
	payload := []byte(`{"contract_version":"domainry-scheduler-plan-dispatch-v1"}`)
	receipt, err := service.Execute(t.Context(), ExecutionRequest{ExecutionID: "scheduler-run", IdempotencyKey: "window", DueAt: dueAt, Target: Target{Owner: "notification", Operation: "publish_reminder", Payload: payload}})
	if err != nil || receipt != (ExecutionReceipt{ID: "notification-event-1", Owner: "notification", Status: "queued"}) || notifications.calls != 1 || notifications.request.Operation != "publish_reminder" || !notifications.request.ScheduledFor.Equal(dueAt.UTC()) || string(notifications.request.Payload) != string(payload) {
		t.Fatalf("receipt=%+v calls=%d request=%+v err=%v", receipt, notifications.calls, notifications.request, err)
	}
	payload[0] = 'x'
	if string(notifications.request.Payload) == string(payload) {
		t.Fatal("Notification target retained caller payload backing storage")
	}
	unconfigured := NewTargetExecutionApplicationService(nil)
	if _, err = unconfigured.Execute(t.Context(), ExecutionRequest{Target: Target{Owner: "notification"}}); apperror.CodeOf(err) != "backend.dispatch.notification_target_unavailable" {
		t.Fatalf("missing Notification runtime err=%v", err)
	}
}
