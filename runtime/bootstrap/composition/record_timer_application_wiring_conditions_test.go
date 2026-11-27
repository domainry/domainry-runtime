package composition

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	recordtimerapplication "github.com/domainry/domainry-runtime/runtime/application/recordtimer"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type recordTimerWorkflowProbe struct {
	execution workflowapplication.ManagedWorkloadExecution
	principal principalmodel.Principal
	calls     int
}

func (*recordTimerWorkflowProbe) ResumeTimerNode(context.Context, string, string, string, principalmodel.Principal) (workflowmodel.WorkflowProcessInstance, error) {
	return workflowmodel.WorkflowProcessInstance{}, nil
}

func (*recordTimerWorkflowProbe) ProcessApprovalDeadlineTimer(context.Context, string, string, string, principalmodel.Principal) error {
	return nil
}

func (p *recordTimerWorkflowProbe) ResolveManagedWorkloadPrincipal(_ context.Context, execution workflowapplication.ManagedWorkloadExecution) (principalmodel.Principal, error) {
	p.calls++
	p.execution = execution
	return p.principal, nil
}

type recordTimerActionProbe struct {
	source     actionmodel.ActionSource
	invocation actionmodel.ActionInvocation
	calls      int
}

func (p *recordTimerActionProbe) Invoke(_ context.Context, source actionmodel.ActionSource, invocation actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
	p.calls++
	p.source, p.invocation = source, invocation
	return actionmodel.ActionInvocationResult{Status: "succeeded"}, nil
}

func TestRecordTimerTargetRuntimeAdapterConditions(t *testing.T) {
	adapter := recordTimerTargetRuntimeAdapter{}
	if err := adapter.ExecuteRecordTimer(t.Context(), recordtimerapplication.RecordTimerExecution{}, principalmodel.Principal{}); err == nil {
		t.Fatal("missing timer runtime accepted")
	}
	called := false
	adapter.execute = func(context.Context, recordtimerapplication.RecordTimerExecution, principalmodel.Principal) error {
		called = true
		return nil
	}
	if err := adapter.ExecuteRecordTimer(t.Context(), recordtimerapplication.RecordTimerExecution{}, principalmodel.Principal{}); err != nil || !called {
		t.Fatalf("timer called=%v err=%v", called, err)
	}
}

func TestRecordTimerBusinessJobReResolvesPersistedWorkload(t *testing.T) {
	bundle := identitysdk.AccessBundle{
		FunctionGrants: []identitysdk.FunctionGrant{{Resource: "order", Action: "expire", Effect: identitysdk.EffectAllow}},
		DataPolicies:   []identitysdk.DataPolicy{{Key: "order.expire", Resource: "order", Action: "expire", Effect: identitysdk.EffectAllow}},
	}
	resolved := principalmodel.Principal{Principal: identitysdk.Principal{
		Known: true, WorkspaceID: "workspace-1", UserID: "workflow:order-maintenance", RoleKey: "order_maintenance", AccessBundle: &bundle,
		Workload: &identitysdk.WorkflowWorkloadPrincipalContext{WorkflowKey: "order-maintenance", DefinitionVersionID: "definition-1", DefinitionVersion: 2, ReleaseID: "release-1", ReleaseDigest: "digest-1"},
	}}
	workflows := &recordTimerWorkflowProbe{principal: resolved}
	actions := &recordTimerActionProbe{}
	worker := principalmodel.NewSystemPrincipal("record-timer", principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test"), "record_timer.read")
	execution := recordtimerapplication.RecordTimerExecution{
		TimerID: "timer-1", WorkspaceID: "workspace-1", ObjectKey: "order", RecordID: "order-1", TargetType: "action", TargetKey: "order.expire",
		WorkloadKey: "order-maintenance", WorkloadDefinitionVersionID: "definition-1", WorkloadDefinitionVersion: 2, WorkloadRoleKey: "order_maintenance",
		WorkloadInitiatorSubjectID: "operator-1", Payload: map[string]any{"reason": "expired"}, IdempotencyKey: "timer-1",
	}
	if err := executeRecordTimer(t.Context(), execution, worker, workflows, actions); err != nil {
		t.Fatal(err)
	}
	if workflows.calls != 1 || workflows.execution.WorkspaceID != "workspace-1" || workflows.execution.WorkloadKey != "order-maintenance" || workflows.execution.ExecutionID != "timer-1" || workflows.execution.InitiatorSubjectID != "operator-1" {
		t.Fatalf("managed workload resolution=%+v calls=%d", workflows.execution, workflows.calls)
	}
	if actions.calls != 1 || actions.source != actionmodel.ActionSourceRecordTimer || actions.invocation.Principal.UserID != resolved.UserID || actions.invocation.Actor.UserID != resolved.UserID || actions.invocation.IdempotencyKey != "timer-1" {
		t.Fatalf("source=%q invocation=%+v calls=%d", actions.source, actions.invocation, actions.calls)
	}
}
