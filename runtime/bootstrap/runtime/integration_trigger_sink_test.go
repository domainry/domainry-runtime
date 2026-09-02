package runtime

import (
	"context"
	"testing"

	integrationsdk "github.com/domainry/domainry-integration-sdk"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type integrationActionProbe struct {
	source     actionmodel.ActionSource
	invocation actionmodel.ActionInvocation
}

func (p *integrationActionProbe) Invoke(_ context.Context, source actionmodel.ActionSource, invocation actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
	p.source, p.invocation = source, invocation
	return actionmodel.ActionInvocationResult{InvocationID: "action-1", Status: "succeeded", Source: source}, nil
}

type integrationWorkflowProbe struct {
	key       string
	payload   map[string]any
	principal principalmodel.Principal
}

func (p *integrationWorkflowProbe) RunIntegrationWorkflow(_ context.Context, key string, payload map[string]any, principal principalmodel.Principal) (workflowmodel.WorkflowRunResult, error) {
	p.key, p.payload, p.principal = key, payload, principal
	return workflowmodel.WorkflowRunResult{Status: "succeeded", Execution: workflowmodel.WorkflowExecution{ID: "workflow-1", Status: "succeeded"}}, nil
}

func TestRuntimeIntegrationTriggerSinkExecutesOnlyRuntimeOwnedTargets(t *testing.T) {
	actions, workflows := &integrationActionProbe{}, &integrationWorkflowProbe{}
	sink := runtimeIntegrationTriggerSink{actions: actions, workflows: workflows}
	actionRequest := integrationsdk.TriggerRequest{
		EventID: "event-1", WorkspaceID: "workspace-a", MappingKey: "mapping-a", IdempotencyKey: "event-1:mapping-a",
		Target: integrationsdk.TriggerTarget{Type: "action", ObjectKey: "contact", RecordID: "contact-1", ActionKey: "sync", Input: map[string]any{"name": "Ada"}},
	}
	receipt, err := sink.Trigger(t.Context(), actionRequest)
	if err != nil || receipt.ExecutionID != "action-1" || actions.source != actionmodel.ActionSourceIntegration {
		t.Fatalf("receipt=%#v source=%q err=%v", receipt, actions.source, err)
	}
	if actions.invocation.IdempotencyKey != actionRequest.IdempotencyKey || actions.invocation.Principal.WorkspaceID != "workspace-a" || !actions.invocation.Principal.SystemScope.Valid() ||
		!actions.invocation.Principal.HasExactPermission("sync") || actions.invocation.Principal.HasPermission("anything.execute") || len(actions.invocation.Actor.SystemCapabilities) != 0 {
		t.Fatalf("action invocation=%#v", actions.invocation)
	}

	workflowRequest := actionRequest
	workflowRequest.EventID, workflowRequest.MappingKey, workflowRequest.IdempotencyKey = "event-2", "mapping-b", "event-2:mapping-b"
	workflowRequest.Target = integrationsdk.TriggerTarget{Type: "workflow", WorkflowKey: "contact-sync", Input: map[string]any{"name": "Ada"}}
	receipt, err = sink.Trigger(t.Context(), workflowRequest)
	if err != nil || receipt.ExecutionID != "workflow-1" || workflows.key != "contact-sync" {
		t.Fatalf("receipt=%#v workflow=%#v err=%v", receipt, workflows, err)
	}
	if workflows.payload["integration_event_id"] != "event-2" || workflows.payload["integration_mapping_key"] != "mapping-b" || workflows.payload["integration_idempotency_key"] != "event-2:mapping-b" {
		t.Fatalf("workflow payload=%#v", workflows.payload)
	}
	if !workflows.principal.HasExactPermission("workflow.contact-sync.run") || workflows.principal.HasPermission("anything.execute") {
		t.Fatalf("workflow principal=%#v", workflows.principal)
	}
}
