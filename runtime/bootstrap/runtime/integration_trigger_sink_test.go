package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationprojection "github.com/domainry/domainry-runtime/runtime/domain/automation/projection"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type integrationAgentProbe struct {
	request    agentsdk.BusinessEventConversationTaskRequest
	authorized bool
	calls      int
}

func (p *integrationAgentProbe) AcceptBusinessEventConversationTask(ctx context.Context, request agentsdk.BusinessEventConversationTaskRequest) (agentsdk.BusinessEventConversationTaskReceipt, error) {
	p.request = request
	p.calls++
	p.authorized = agentsdk.HasAuthorizedServiceAction(ctx, agentsdk.ActionAgentBusinessEventConversationTaskAccept, agentsdk.AgentRuntimeServiceAudience)
	return agentsdk.BusinessEventConversationTaskReceipt{Task: agentsdk.ConversationTask{ID: "task-event-1"}}, nil
}

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

type integrationAutomationProbe struct {
	request   automationmodel.AutomationWebhookTriggerRequest
	principal principalmodel.Principal
	calls     int
}

func (p *integrationAutomationProbe) RunWebhookAutomation(_ context.Context, request automationmodel.AutomationWebhookTriggerRequest, principal principalmodel.Principal) (automationprojection.AutomationRuleTrace, error) {
	p.request, p.principal = request, principal
	p.calls++
	return automationprojection.AutomationRuleTrace{ExecutionID: "automation-1", RuleKey: request.RuleKey, Status: "succeeded"}, nil
}

func (p *integrationWorkflowProbe) RunIntegrationWorkflow(_ context.Context, key string, payload map[string]any, principal principalmodel.Principal) (workflowmodel.WorkflowRunResult, error) {
	p.key, p.payload, p.principal = key, payload, principal
	return workflowmodel.WorkflowRunResult{Status: "succeeded", Execution: workflowmodel.WorkflowExecution{ID: "workflow-1", Status: "succeeded"}}, nil
}

func TestRuntimeIntegrationTriggerSinkExecutesOnlyRuntimeOwnedTargets(t *testing.T) {
	actions, workflows := &integrationActionProbe{}, &integrationWorkflowProbe{}
	sink := runtimeIntegrationTriggerSink{actions: actions, workflows: workflows, principals: runtimeIdentityPrincipalResolverStub{}}
	actionRequest := integrationsdk.TriggerRequest{
		EventID: "event-1", WorkspaceID: "workspace-a", MappingKey: "mapping-a", IdempotencyKey: "event-1:mapping-a",
		Principal: integrationsdk.TriggerPrincipal{OwnerUserID: "connection-owner"},
		Target:    integrationsdk.TriggerTarget{Type: "action", ObjectKey: "contact", RecordID: "contact-1", ActionKey: "sync", Input: map[string]any{"name": "Ada"}},
	}
	receipt, err := sink.Trigger(t.Context(), actionRequest)
	if err != nil || receipt.ExecutionID != "action-1" || actions.source != actionmodel.ActionSourceIntegration {
		t.Fatalf("receipt=%#v source=%q err=%v", receipt, actions.source, err)
	}
	if actions.invocation.IdempotencyKey != actionRequest.IdempotencyKey || actions.invocation.Principal.WorkspaceID != "workspace-a" || actions.invocation.Principal.UserID != "connection-owner" || actions.invocation.Principal.OrgID != "sales-org" || actions.invocation.Principal.AccessBundle != nil || !actions.invocation.Principal.SystemScope.Valid() ||
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

func TestRuntimeIntegrationTriggerSinkMapsVerifiedEventToCurrentAgentIdentity(t *testing.T) {
	agent := &integrationAgentProbe{}
	sink := runtimeIntegrationTriggerSink{agents: agent, principals: runtimeIdentityPrincipalResolverStub{}}
	received := time.Date(2026, 9, 16, 9, 30, 0, 0, time.UTC)
	request := integrationsdk.TriggerRequest{
		EventID: "event-42", WorkspaceID: "workspace-a", MappingKey: "ticket-escalated", MappingRevision: strings.Repeat("a", 64), IdempotencyKey: "event-42:ticket-escalated",
		Source:    integrationsdk.TriggerSource{Provider: "support", EventType: "ticket.escalated", ExternalID: "ticket-42", ReceivedAt: received.Format(time.RFC3339Nano)},
		Principal: integrationsdk.TriggerPrincipal{ActorID: "user-7", RoleKey: "support"},
		Target:    integrationsdk.TriggerTarget{Type: "agent_task", AgentID: "support-agent", ConversationID: "conversation-7", AgentTaskMode: "wake", RelatedTaskID: "task-previous", Input: map[string]any{"goal": "Handle escalation", "ticket_id": "42", "allowed_tools": []any{}}},
	}
	receipt, err := sink.Trigger(t.Context(), request)
	if err != nil || receipt.ExecutionID != "task-event-1" || receipt.Status != "accepted" || !agent.authorized {
		t.Fatalf("receipt=%+v authorized=%t err=%v", receipt, agent.authorized, err)
	}
	actual := agent.request
	if actual.ContractVersion != agentsdk.BusinessEventConversationTaskContractVersion || actual.Authority.UserID != "user-7" || actual.Authority.RoleKey != "support" || actual.Authority.WorkspaceID != "workspace-a" ||
		actual.AgentID != "support-agent" || actual.ConversationID != "conversation-7" || actual.Mode != "wake" || actual.RelatedTaskID != "task-previous" || actual.Source.EventID != "event-42" || actual.Rule.Revision != request.MappingRevision || actual.Input.Goal != "Handle escalation" || !strings.Contains(actual.Input.Input, `"ticket_id":"42"`) {
		t.Fatalf("Agent request=%+v", actual)
	}
	request.Principal = integrationsdk.TriggerPrincipal{}
	if _, err = sink.Trigger(t.Context(), request); err == nil || agent.calls != 1 {
		t.Fatalf("unmapped Agent identity reached task service: err=%v request=%+v", err, agent.request)
	}
}

func TestRuntimeIntegrationTriggerSinkRunsAutomationAsCurrentConnectionOwner(t *testing.T) {
	automation := &integrationAutomationProbe{}
	sink := runtimeIntegrationTriggerSink{automations: automation, principals: runtimeIdentityPrincipalResolverStub{}}
	received := time.Date(2026, 9, 17, 8, 0, 0, 0, time.UTC)
	request := integrationsdk.TriggerRequest{
		EventID: "event-automation-1", WorkspaceID: "workspace-a", MappingKey: "contact-change", MappingRevision: strings.Repeat("b", 64), IdempotencyKey: "event-automation-1:contact-change",
		Source:    integrationsdk.TriggerSource{Provider: "crm", EventType: "contact.changed", ExternalID: "provider-event-1", ReceivedAt: received.Format(time.RFC3339Nano)},
		Principal: integrationsdk.TriggerPrincipal{OwnerUserID: "connection-owner"},
		Target:    integrationsdk.TriggerTarget{Type: "automation", AutomationRuleKey: "contact.webhook", Input: map[string]any{"name": "Ada"}},
	}
	receipt, err := sink.Trigger(t.Context(), request)
	if err != nil || receipt.ExecutionID != "automation-1" || receipt.Status != "succeeded" || automation.calls != 1 {
		t.Fatalf("receipt=%+v calls=%d err=%v", receipt, automation.calls, err)
	}
	if automation.request.RuleKey != "contact.webhook" || automation.request.MappingRevision != request.MappingRevision || automation.request.Input["name"] != "Ada" || automation.request.ExternalID != "provider-event-1" {
		t.Fatalf("Automation request=%#v", automation.request)
	}
	if !automation.principal.Known || automation.principal.UserID != "connection-owner" || automation.principal.WorkspaceID != "workspace-a" || automation.principal.SystemScope.Valid() {
		t.Fatalf("Automation principal=%#v", automation.principal)
	}
	request.MappingRevision = strings.Repeat("B", 64)
	if _, err = sink.Trigger(t.Context(), request); err == nil || automation.calls != 1 {
		t.Fatalf("invalid mapping revision reached Automation: calls=%d err=%v", automation.calls, err)
	}
}

func TestIntegrationMappingProjectionPreservesAutomationContract(t *testing.T) {
	projected := integrationMappingProjections([]integrationsdk.EventMappingRequirement{{
		Key: "contact-change", Provider: "crm", EventType: "contact.changed", TargetType: "automation", AutomationRuleKey: "contact.webhook",
		AutomationInput: map[string]string{"name": "contact.name"}, Enabled: true,
	}})
	if len(projected) != 1 || projected[0].AutomationRuleKey != "contact.webhook" || projected[0].AutomationInput["name"] != "contact.name" {
		t.Fatalf("projection=%#v", projected)
	}
}
