package composition

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestWorkflowBusinessActionInvocationOwnsSynchronousEffectsDurably(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "owner-a", RoleKey: "sales"}}
	invocation := workflowBusinessActionInvocation(workflowapplication.WorkflowBusinessActionInvocation{
		ActionKey: "email.send", ObjectKey: "email", RecordID: "email-1", Input: map[string]any{"confirmed": true},
		Principal: principal, Actor: principal, ProcessID: "process-1", NodeID: "send", IdempotencyKey: "process-1:send:1",
	})
	if !invocation.PreventExecutionReclaim || invocation.ActionKey != "email.send" || invocation.ObjectKey != "email" || invocation.RecordID != "email-1" || invocation.ProcessID != "process-1" || invocation.NodeID != "send" || invocation.IdempotencyKey != "process-1:send:1" {
		t.Fatalf("workflow invocation lost its durable execution identity: %+v", invocation)
	}
	if invocation.Principal.UserID != principal.UserID || invocation.Principal.RoleKey != principal.RoleKey {
		t.Fatalf("workflow invocation principal=%+v", invocation.Principal)
	}
}
