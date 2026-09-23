package composition

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	dispatchapplication "github.com/domainry/domainry-runtime/runtime/application/dispatch"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

type schedulerManagedPrincipalProbe struct {
	execution workflowapplication.ManagedWorkloadExecution
	principal principalmodel.Principal
	err       error
	calls     int
}

func (p *schedulerManagedPrincipalProbe) ResolveManagedWorkloadPrincipal(_ context.Context, execution workflowapplication.ManagedWorkloadExecution) (principalmodel.Principal, error) {
	p.calls++
	p.execution = execution
	return p.principal, p.err
}

type schedulerActionProbe struct {
	source     actionmodel.ActionSource
	invocation actionmodel.ActionInvocation
	receipts   map[string]actionmodel.ActionInvocationResult
	effects    int
	calls      int
}

func (p *schedulerActionProbe) Invoke(_ context.Context, source actionmodel.ActionSource, invocation actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
	p.calls++
	p.source, p.invocation = source, invocation
	if result, replay := p.receipts[invocation.IdempotencyKey]; replay {
		return result, nil
	}
	p.effects++
	result := actionmodel.ActionInvocationResult{InvocationID: invocation.IdempotencyKey, Status: "succeeded", Source: source}
	p.receipts[invocation.IdempotencyKey] = result
	return result, nil
}

func TestSchedulerBusinessActionUsesManagedRoleAndStableWindowIdempotency(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "workflow:scheduler:expire-orders", RoleKey: "order_automation"}}
	principals := &schedulerManagedPrincipalProbe{principal: principal}
	actions := &schedulerActionProbe{receipts: map[string]actionmodel.ActionInvocationResult{}}
	runtime := schedulerBusinessActionRuntime{principals: principals, actions: actions}
	request := dispatchapplication.BusinessActionTargetRequest{
		ExecutionID: "run-1", DefinitionKey: "expire-orders", IdempotencyKey: "expire-orders:window:2026-09-20T02:00:00Z",
		Operation: "order.expire", ObjectKey: "order", RunAsRole: "order_automation", Payload: []byte(`{"status":"expired","sequence":9007199254740993}`),
	}
	first, err := runtime.ExecuteBusinessActionTarget(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtime.ExecuteBusinessActionTarget(t.Context(), request)
	if err != nil || first != second || actions.calls != 2 || actions.effects != 1 {
		t.Fatalf("first=%+v second=%+v calls=%d effects=%d err=%v", first, second, actions.calls, actions.effects, err)
	}
	wantVersion := schedulerBusinessActionAuthorizationVersion("expire-orders", "order.expire", "order", "order_automation")
	if principals.execution.WorkloadKey != "scheduler:expire-orders" || principals.execution.DefinitionVersionID != wantVersion || principals.execution.RoleKey != "order_automation" || principals.execution.IdempotencyKey != request.IdempotencyKey {
		t.Fatalf("managed workload resolution=%+v", principals.execution)
	}
	if actions.source != actionmodel.ActionSourceScheduler || actions.invocation.Principal.UserID != principal.UserID || actions.invocation.ActionKey != "order.expire" || actions.invocation.ObjectKey != "order" || actions.invocation.Input["status"] != "expired" || actions.invocation.Input["sequence"] != json.Number("9007199254740993") || actions.invocation.IdempotencyKey != request.IdempotencyKey || !actions.invocation.PreventExecutionReclaim {
		t.Fatalf("source=%q invocation=%+v", actions.source, actions.invocation)
	}

	pausedVersion := schedulerBusinessActionAuthorizationVersion("expire-orders", "order.expire", "order", "order_automation")
	if pausedVersion != wantVersion {
		t.Fatal("pause/resume changed Scheduler authorization identity")
	}
	if schedulerBusinessActionAuthorizationVersion("expire-orders", "order.cancel", "order", "order_automation") == wantVersion || schedulerBusinessActionAuthorizationVersion("expire-orders", "order.expire", "order", "other_role") == wantVersion {
		t.Fatal("Action or role change did not rotate Scheduler authorization identity")
	}
}

func TestSchedulerBusinessActionFailsClosedBeforeActionInvocation(t *testing.T) {
	denied := errors.New("managed role denied")
	principals := &schedulerManagedPrincipalProbe{err: denied}
	actions := &schedulerActionProbe{receipts: map[string]actionmodel.ActionInvocationResult{}}
	runtime := schedulerBusinessActionRuntime{principals: principals, actions: actions}
	request := dispatchapplication.BusinessActionTargetRequest{ExecutionID: "run", DefinitionKey: "job", IdempotencyKey: "window", Operation: "order.expire", ObjectKey: "order", RunAsRole: "service", Payload: []byte(`{}`)}
	if _, err := runtime.ExecuteBusinessActionTarget(t.Context(), request); !errors.Is(err, denied) || actions.calls != 0 {
		t.Fatalf("denied error=%v action calls=%d", err, actions.calls)
	}
	request.Payload = []byte(`[]`)
	principals.err = nil
	if _, err := runtime.ExecuteBusinessActionTarget(t.Context(), request); err == nil || principals.calls != 1 || actions.calls != 0 {
		t.Fatalf("invalid payload error=%v principal calls=%d action calls=%d", err, principals.calls, actions.calls)
	}
	request.Payload = []byte(`{} {}`)
	if _, err := runtime.ExecuteBusinessActionTarget(t.Context(), request); err == nil || principals.calls != 1 || actions.calls != 0 {
		t.Fatalf("trailing payload error=%v principal calls=%d action calls=%d", err, principals.calls, actions.calls)
	}
	request.Payload = []byte(`{"value":"` + strings.Repeat("x", schedulerBusinessActionMaximumPayloadBytes) + `"}`)
	if _, err := runtime.ExecuteBusinessActionTarget(t.Context(), request); err == nil || principals.calls != 1 || actions.calls != 0 {
		t.Fatalf("oversized payload error=%v principal calls=%d action calls=%d", err, principals.calls, actions.calls)
	}
	request.Payload = []byte(`{}`)
	request.ExecutionID = ""
	if _, err := runtime.ExecuteBusinessActionTarget(t.Context(), request); err == nil || principals.calls != 1 || actions.calls != 0 {
		t.Fatalf("missing execution error=%v principal calls=%d action calls=%d", err, principals.calls, actions.calls)
	}
}

func TestSchedulerBusinessActionWorkloadProjectionIsDeterministic(t *testing.T) {
	definitions := []schedulersdk.Definition{
		{Key: "snapshot", Target: schedulersdk.TargetRef{Owner: "report", Operation: "daily"}},
		{Key: "expire-orders", Target: schedulersdk.TargetRef{Owner: "business_action", Operation: "order.expire", ObjectKey: "order", RunAsRole: "order_automation"}},
	}
	bindings, err := SchedulerBusinessActionWorkloadBindings(definitions)
	if err != nil || len(bindings) != 1 || bindings[0].WorkloadKey != "scheduler:expire-orders" || bindings[0].RoleKey != "order_automation" || len(bindings[0].ActionKeys) != 1 || bindings[0].ActionKeys[0] != "order.expire" {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
}
