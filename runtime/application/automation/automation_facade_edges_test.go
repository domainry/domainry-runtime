package automation

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	automationcontract "github.com/domainry/domainry-runtime/runtime/domain/automation/contract"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationbusiness "github.com/domainry/domainry-runtime/runtime/domain/automation/service"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type automationNotificationCommitterStub struct{}

func (automationNotificationCommitterStub) CommitAutomationExecution(context.Context, automationmodel.AutomationRuleExecution) error {
	return nil
}

func (automationNotificationCommitterStub) CommitAutomationExecutionNotification(context.Context, automationmodel.AutomationRuleExecution, notificationmodel.NotificationEvent) error {
	return nil
}

var errAutomationFacadeProbe = errors.New("automation facade probe failed")

type automationFacadeRegistry struct {
	rules map[string]automationmodel.AutomationRuleSchema
}

func (r *automationFacadeRegistry) List() []automationmodel.AutomationRuleSchema {
	result := make([]automationmodel.AutomationRuleSchema, 0, len(r.rules))
	for _, rule := range r.rules {
		result = append(result, rule)
	}
	return result
}

func (r *automationFacadeRegistry) Get(key string) (automationmodel.AutomationRuleSchema, bool) {
	rule, found := r.rules[key]
	return rule, found
}

type automationFacadeConnectorCatalog struct {
	schema integrationmodel.IntegrationSchema
}

func (c automationFacadeConnectorCatalog) Schema() integrationmodel.IntegrationSchema {
	return c.schema
}

type automationFacadeMetadataProbe struct {
}

func (p *automationFacadeMetadataProbe) ListMetadataDefinitionVersions(_ context.Context, _ string, resourceKey string, _ principalmodel.Principal) ([]metadatamodel.MetadataDefinitionVersion, error) {
	return []metadatamodel.MetadataDefinitionVersion{{ResourceType: "automation_rule", ResourceKey: resourceKey, SchemaVersion: "1"}}, nil
}

type automationFacadeWorkflowProbe struct {
	run workflowmodel.WorkflowRunResult
	err error
}

func (p automationFacadeWorkflowProbe) RunAutomationWorkflow(context.Context, string, map[string]any, principalmodel.Principal) (workflowmodel.WorkflowRunResult, error) {
	return p.run, p.err
}

func automationFacadePrincipal() principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "operator"}}, accessfixture.Bundle{Key: "admin", Permissions: []string{"workspace.admin"}})
}

func newAutomationFacade(registry *automationFacadeRegistry, metadata *automationFacadeMetadataProbe) *AutomationApplicationService {
	return NewAutomationApplicationService(AutomationApplicationDependencies{
		Rules:      registry,
		Connectors: automationFacadeConnectorCatalog{schema: integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "erp"}}}},
		Metadata:   metadata,
		Schema: func(context.Context, principalmodel.Principal) metadatamodel.MetadataSchemaSnapshot {
			return metadatamodel.MetadataSchemaSnapshot{Integrations: integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "erp"}}}}
		},
		Principal: func(_ context.Context, userID, roleKey, _ string) principalmodel.Principal {
			return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: userID}}, accessfixture.Bundle{Key: roleKey, Permissions: []string{"workspace.admin"}})
		},
		ValidateRule: func(context.Context, automationmodel.AutomationRuleSchema) error { return nil },
	})
}

func TestAutomationFacadeConstructorQueriesAndValidationDelegation(t *testing.T) {
	registry := &automationFacadeRegistry{rules: map[string]automationmodel.AutomationRuleSchema{
		"z": {Key: "z"}, "a": {Key: "a"},
	}}
	service := newAutomationFacade(registry, &automationFacadeMetadataProbe{})
	principal := automationFacadePrincipal()
	rules, err := service.AutomationRules(t.Context(), principal)
	if err != nil || len(rules) != 2 || rules[0].Key != "a" {
		t.Fatalf("rules=%#v err=%v", rules, err)
	}
	rule, err := service.AutomationRule(t.Context(), " a ", principal)
	if err != nil || rule.Key != "a" {
		t.Fatalf("rule=%#v err=%v", rule, err)
	}
	catalog, err := service.AutomationCapabilities(t.Context(), principal)
	if err != nil || len(catalog.Connectors) != 1 || len(catalog.Connections) != 0 {
		t.Fatalf("catalog=%#v err=%v", catalog, err)
	}
	history, err := service.AutomationExecutions(t.Context(), automationmodel.AutomationExecutionFilter{}, principal)
	if err != nil || history.Count != 0 {
		t.Fatalf("history=%#v err=%v", history, err)
	}
	if err := service.ValidateIntegrationOutput(automationmodel.AutomationInstructionSchema{Config: map[string]any{"connector_key": "missing"}}, nil); err == nil {
		t.Fatal("missing connector output accepted")
	}
	if result, err := service.ValidateAutomationRule(t.Context(), automationmodel.AutomationRuleSchema{Key: "valid"}, principal); err != nil || !result.Valid {
		t.Fatalf("validation=%#v err=%v", result, err)
	}
}

func TestAutomationFacadeBeforeOutboxSimulationAndWorkflowProjection(t *testing.T) {
	beforeRule := automationmodel.AutomationRuleSchema{Key: "before", ObjectKey: "order", Enabled: true, Trigger: automationmodel.AutomationTriggerSchema{Phase: "before", Operation: "create"}}
	afterRule := automationmodel.AutomationRuleSchema{Key: "after", ObjectKey: "order", Enabled: true, Trigger: automationmodel.AutomationTriggerSchema{Phase: "after", Operation: "update"}}
	registry := &automationFacadeRegistry{rules: map[string]automationmodel.AutomationRuleSchema{"before": beforeRule, "after": afterRule}}
	service := newAutomationFacade(registry, &automationFacadeMetadataProbe{})
	principal := automationFacadePrincipal()
	traces, err := service.RunBefore(t.Context(), "order", "create", "record-1", map[string]any{"name": "A"}, nil, map[string]any{"name": "A"}, principal)
	if err != nil || len(traces) != 1 || traces[0].RuleKey != "before" {
		t.Fatalf("traces=%#v err=%v", traces, err)
	}
	if trace, err := service.ExecuteBeforeRule(t.Context(), beforeRule, nil, nil, map[string]any{"name": "A"}, principal); err != nil || trace.Status != "succeeded" {
		t.Fatalf("trace=%#v err=%v", trace, err)
	}
	if _, err := service.ExecuteBeforeRule(t.Context(), beforeRule, nil, nil, nil, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("before auth err=%v", err)
	}
	if _, _, err := service.FindBeforeCreateReplay(t.Context(), definitionmodel.ObjectSchema{Key: "order"}, nil, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("replay auth err=%v", err)
	}
	if _, found, err := service.FindBeforeCreateReplay(t.Context(), definitionmodel.ObjectSchema{Key: "other"}, nil, principal); err != nil || found {
		t.Fatalf("authorized replay found=%v err=%v", found, err)
	}
	if messages := service.AfterOutbox("order", "update", nil, recordmodel.Record{ID: "record-1"}, principalmodel.Principal{}); messages != nil {
		t.Fatalf("unauthorized outbox=%#v", messages)
	}
	if messages := service.AfterOutbox("order", "update", nil, recordmodel.Record{ID: "record-1", Data: map[string]any{"status": "ready"}}, principal); len(messages) != 1 || messages[0].DedupKey == "" {
		t.Fatalf("outbox=%#v", messages)
	}

	requestRule := beforeRule
	result, err := service.SimulateAutomationRule(t.Context(), "", automationcontract.AutomationSimulationRequest{Rule: &requestRule, Input: map[string]any{"name": "A"}}, principal)
	if err != nil || result.TestedNodeID != "save" || !result.NodePassed {
		t.Fatalf("simulation=%#v err=%v", result, err)
	}
	result, err = service.SimulateAutomationRule(t.Context(), "after", automationcontract.AutomationSimulationRequest{}, principal)
	if err != nil || result.TestedNodeID != "outbox" || !result.NodePassed {
		t.Fatalf("after simulation=%#v err=%v", result, err)
	}
	if _, err := service.SimulateAutomationRule(t.Context(), "missing", automationcontract.AutomationSimulationRequest{}, principal); apperror.CodeOf(err) != "backend.automation.not_found" {
		t.Fatalf("missing simulation err=%v", err)
	}

	run := workflowmodel.WorkflowRunResult{Execution: workflowmodel.WorkflowExecution{ID: "execution-1", Status: "completed"}}
	projected, err := AutomationWorkflowInstructionResult("workflow-a", run, nil)
	if err != nil || projected["execution_id"] != "execution-1" || projected["status"] != "completed" {
		t.Fatalf("projection=%#v err=%v", projected, err)
	}
	if _, err := AutomationWorkflowInstructionResult("workflow-a", run, errAutomationFacadeProbe); !errors.Is(err, errAutomationFacadeProbe) {
		t.Fatalf("workflow err=%v", err)
	}
}

func TestAutomationFacadeRejectsEffectfulBeforeInstruction(t *testing.T) {
	rule := automationmodel.AutomationRuleSchema{
		Key: "blocked", ObjectKey: "order", Enabled: true,
		Trigger:      automationmodel.AutomationTriggerSchema{Phase: "before", Operation: "create"},
		Instructions: []automationmodel.AutomationInstructionSchema{{Key: "invoke", Type: "invoke_business_action", Config: map[string]any{}}},
	}
	registry := &automationFacadeRegistry{rules: map[string]automationmodel.AutomationRuleSchema{rule.Key: rule}}
	service := newAutomationFacade(registry, &automationFacadeMetadataProbe{})
	traces, err := service.RunBefore(t.Context(), "order", "create", "record-1", nil, nil, map[string]any{}, automationFacadePrincipal())
	if err == nil || len(traces) != 1 || traces[0].ErrorCode == "" {
		t.Fatalf("traces=%+v err=%v", traces, err)
	}
}

func TestAutomationFacadeExecuteOutboxValidatesScopeRuleAndExecutes(t *testing.T) {
	afterRule := automationmodel.AutomationRuleSchema{Key: "after", ObjectKey: "order", Enabled: true, Trigger: automationmodel.AutomationTriggerSchema{Phase: "after", Operation: "update"}, Execution: automationmodel.AutomationExecutionPolicy{ResultNotification: "all"}}
	registry := &automationFacadeRegistry{rules: map[string]automationmodel.AutomationRuleSchema{"after": afterRule, "disabled": {Key: "disabled", Enabled: false, Trigger: automationmodel.AutomationTriggerSchema{Phase: "after"}}, "before": {Key: "before", Enabled: true, Trigger: automationmodel.AutomationTriggerSchema{Phase: "before"}}}}
	service := newAutomationFacade(registry, &automationFacadeMetadataProbe{})
	var notification notificationmodel.NotificationIntent
	service.compileNotification = func(intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		notification = intent
		return notificationmodel.NotificationEvent{EventType: intent.EventType}, nil
	}
	service.commitNotification = automationNotificationCommitterStub{}
	message := integrationmodel.IntegrationOutboxMessage{WorkspaceID: "workspace-1", Payload: automationbusiness.LifecycleEventPayload(automationmodel.AutomationLifecycleEvent{RuleKey: "after", RecordVersion: "v2", Record: recordmodel.Record{ID: "record-1", Data: map[string]any{"status": "ready"}}, ActorUserID: "user", ActorRoleKey: "role", RequestID: "request"})}
	if err := service.ExecuteOutboxMessage(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	if notification.EventType != "automation.execution.completed" || notification.RecipientUserIDs[0] != "user" || notification.SubjectID != "after" {
		t.Fatalf("terminal notification=%+v", notification)
	}
	message.WorkspaceID = ""
	if err := service.ExecuteOutboxMessage(t.Context(), message); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("scope err=%v", err)
	}
	message.WorkspaceID = "workspace-1"
	for _, key := range []string{"missing", "disabled", "before"} {
		message.Payload = automationbusiness.LifecycleEventPayload(automationmodel.AutomationLifecycleEvent{RuleKey: key})
		if err := service.ExecuteOutboxMessage(t.Context(), message); apperror.CodeOf(err) != "backend.automation.after_rule_not_found" {
			t.Fatalf("key=%s err=%v", key, err)
		}
	}
}

func TestAutomationAfterInstructionCanRenderStableEventID(t *testing.T) {
	rule := automationmodel.AutomationRuleSchema{
		Key: "after", ObjectKey: "order", Enabled: true,
		Trigger: automationmodel.AutomationTriggerSchema{Phase: "after", Operation: "update"},
		Instructions: []automationmodel.AutomationInstructionSchema{{
			Key: "invoke", Type: "invoke_business_action", Config: map[string]any{
				"action_key": "order.complete",
				"input":      map[string]any{"idempotency_key": "$event.id"},
			},
		}},
	}
	registry := &automationFacadeRegistry{rules: map[string]automationmodel.AutomationRuleSchema{rule.Key: rule}}
	var got string
	service := NewAutomationApplicationService(AutomationApplicationDependencies{
		Rules: registry,
		WorkerStore: instructionRepositoryStub{
			claim: func(_ context.Context, execution automationmodel.AutomationInstructionExecution, _, _ string) (automationmodel.AutomationInstructionExecution, bool, error) {
				execution.ID, execution.LeaseOwner, execution.FencingToken = "instruction", "worker", 1
				return execution, true, nil
			},
			complete: func(context.Context, string, string, string, int64, string, map[string]any, string) (automationmodel.AutomationInstructionExecution, error) {
				return automationmodel.AutomationInstructionExecution{}, nil
			},
		},
		ExecutionRepository: &executionRepositoryStub{},
		Principal: func(_ context.Context, userID, roleKey, _ string) principalmodel.Principal {
			return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: userID}}, accessfixture.Bundle{Key: roleKey, Permissions: []string{"order.complete"}})
		},
		InvokeAction: func(_ context.Context, invocation actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
			got, _ = invocation.Input["idempotency_key"].(string)
			return actionmodel.ActionInvocationResult{InvocationID: "completed"}, nil
		},
	})
	eventID := "automation:after:order:order-1:update:v1"
	message := integrationmodel.IntegrationOutboxMessage{WorkspaceID: "workspace-1", Payload: automationbusiness.LifecycleEventPayload(automationmodel.AutomationLifecycleEvent{
		ID: eventID, RuleKey: rule.Key, RecordVersion: "v1", Record: recordmodel.Record{ID: "order-1", Data: map[string]any{"status": "ready"}, UpdatedAt: "v1"},
		ActorUserID: "user", ActorRoleKey: "role", RequestID: "request", IdentityPolicy: "revalidate_initiator",
	})}
	if err := service.ExecuteOutboxMessage(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	if got != eventID {
		t.Fatalf("rendered event id=%q want=%q", got, eventID)
	}
}

func TestAutomationAfterOutboxRevalidatesIdentityAndRejectsLoopsAndDepth(t *testing.T) {
	rule := automationmodel.AutomationRuleSchema{Key: "after", ObjectKey: "order", Enabled: true, Trigger: automationmodel.AutomationTriggerSchema{Phase: "after", Operation: "update"}, Execution: automationmodel.AutomationExecutionPolicy{MaxDepth: 2}}
	registry := &automationFacadeRegistry{rules: map[string]automationmodel.AutomationRuleSchema{"after": rule}}
	known := true
	service := NewAutomationApplicationService(AutomationApplicationDependencies{
		Rules: registry,
		Principal: func(_ context.Context, userID, roleKey, _ string) principalmodel.Principal {
			return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: known, UserID: userID}}, accessfixture.Bundle{Key: roleKey})
		},
	})
	message := func(event automationmodel.AutomationLifecycleEvent) integrationmodel.IntegrationOutboxMessage {
		event.RuleKey, event.RecordVersion = "after", "v1"
		event.Record = recordmodel.Record{ID: "record", Data: map[string]any{}}
		event.ActorUserID, event.ActorRoleKey = "user", "operator"
		if event.IdentityPolicy == "" {
			event.IdentityPolicy = "revalidate_initiator"
		}
		return integrationmodel.IntegrationOutboxMessage{WorkspaceID: "workspace", Payload: automationbusiness.LifecycleEventPayload(event)}
	}
	known = false
	if err := service.ExecuteOutboxMessage(t.Context(), message(automationmodel.AutomationLifecycleEvent{})); apperror.CodeOf(err) != "backend.automation.identity_revoked" {
		t.Fatalf("revoked identity err=%v", err)
	}
	known = true
	if err := service.ExecuteOutboxMessage(t.Context(), message(automationmodel.AutomationLifecycleEvent{VisitedRuleKeys: []string{"after"}})); apperror.CodeOf(err) != "backend.automation.recursion_detected" {
		t.Fatalf("loop err=%v", err)
	}
	if err := service.ExecuteOutboxMessage(t.Context(), message(automationmodel.AutomationLifecycleEvent{AutomationDepth: 2})); apperror.CodeOf(err) != "backend.automation.max_depth_exceeded" {
		t.Fatalf("depth err=%v", err)
	}
	if err := service.ExecuteOutboxMessage(t.Context(), message(automationmodel.AutomationLifecycleEvent{IdentityPolicy: "frozen_admin"})); apperror.CodeOf(err) != "backend.automation.identity_policy_invalid" {
		t.Fatalf("identity policy err=%v", err)
	}
}

func TestQueuedAutomationRevalidatesCurrentActionPermissionInsteadOfFreezingRoleSnapshot(t *testing.T) {
	rule := automationmodel.AutomationRuleSchema{
		Key: "order.after_update", ObjectKey: "order", Enabled: true,
		Trigger:      automationmodel.AutomationTriggerSchema{Phase: "after", Operation: "update"},
		Instructions: []automationmodel.AutomationInstructionSchema{{Key: "approve", Type: "invoke_business_action", Config: map[string]any{"action_key": "order.approve", "object_key": "order"}}},
	}
	registry := &automationFacadeRegistry{rules: map[string]automationmodel.AutomationRuleSchema{rule.Key: rule}}
	currentPermissions := []string{"order.read"}
	invocations := 0
	service := NewAutomationApplicationService(AutomationApplicationDependencies{
		Rules: registry,
		WorkerStore: instructionRepositoryStub{
			claim: func(_ context.Context, execution automationmodel.AutomationInstructionExecution, _, _ string) (automationmodel.AutomationInstructionExecution, bool, error) {
				execution.ID, execution.LeaseOwner, execution.FencingToken = "queued-instruction", "worker", 1
				return execution, true, nil
			},
			complete: func(context.Context, string, string, string, int64, string, map[string]any, string) (automationmodel.AutomationInstructionExecution, error) {
				return automationmodel.AutomationInstructionExecution{}, nil
			},
		},
		ExecutionRepository: &executionRepositoryStub{},
		Principal: func(_ context.Context, userID, roleKey, _ string) principalmodel.Principal {
			return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: userID}}, accessfixture.Bundle{Key: roleKey, Permissions: append([]string(nil), currentPermissions...)})
		},
		InvokeAction: func(_ context.Context, invocation actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
			invocations++
			for _, permission := range invocation.Principal.PermissionKeys() {
				if permission == "order.approve" {
					return actionmodel.ActionInvocationResult{InvocationID: "approved"}, nil
				}
			}
			return actionmodel.ActionInvocationResult{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.action.permission_denied"}
		},
	})
	event := automationmodel.AutomationLifecycleEvent{
		RuleKey: rule.Key, RecordVersion: "v1", Record: recordmodel.Record{ID: "order-1", Data: map[string]any{"status": "ready"}},
		ActorUserID: "operator-a", ActorRoleKey: "operator", IdentityPolicy: "revalidate_initiator",
	}
	message := integrationmodel.IntegrationOutboxMessage{WorkspaceID: "workspace-a", Payload: automationbusiness.LifecycleEventPayload(event)}
	if err := service.ExecuteOutboxMessage(t.Context(), message); apperror.CodeOf(err) != "backend.action.permission_denied" || invocations != 1 {
		t.Fatalf("revoked queued Action err=%v invocations=%d", err, invocations)
	}
	currentPermissions = []string{"order.read", "order.approve"}
	if err := service.ExecuteOutboxMessage(t.Context(), message); err != nil || invocations != 2 {
		t.Fatalf("newly authorized queued Action err=%v invocations=%d", err, invocations)
	}
}

func TestAutomationFacadeExecutesPureBeforeConditionAndDerivationWithoutIO(t *testing.T) {
	rule := automationmodel.AutomationRuleSchema{
		Key: "normalize", ObjectKey: "order", Enabled: true, AuditEvent: "order_normalized",
		Trigger:      automationmodel.AutomationTriggerSchema{Phase: "before", Operation: "create"},
		Conditions:   automationmodel.AutomationConditionGroup{Clauses: []automationmodel.AutomationConditionClause{{Reference: "$payload.active", Operator: "eq", Value: true}}},
		Instructions: []automationmodel.AutomationInstructionSchema{{Key: "derive", Type: "derive_fields", ResultAlias: "derive_result", Config: map[string]any{"fields": map[string]any{"normalized": true}}}},
	}
	registry := &automationFacadeRegistry{rules: map[string]automationmodel.AutomationRuleSchema{"normalize": rule}}
	audits := []string{}
	service := NewAutomationApplicationService(AutomationApplicationDependencies{
		Rules: registry, Connectors: automationFacadeConnectorCatalog{},
		Schema: func(context.Context, principalmodel.Principal) metadatamodel.MetadataSchemaSnapshot {
			return metadatamodel.MetadataSchemaSnapshot{}
		},
		Audit: func(_ context.Context, event, _, _ string, _ principalmodel.Principal, _ string, _, _, _ map[string]any) {
			audits = append(audits, event)
		},
		ValidateRule: func(context.Context, automationmodel.AutomationRuleSchema) error { return nil },
	})
	principal := automationFacadePrincipal()
	if rules := service.matchingRules("order", "before", "create", nil, map[string]any{"active": true}); len(rules) != 1 {
		t.Fatalf("matching rules=%#v", rules)
	}
	traces, err := service.RunBefore(t.Context(), "order", "create", "record-1", map[string]any{"active": true}, nil, map[string]any{"active": true}, principal)
	if err != nil || len(traces) != 1 || traces[0].Status != "succeeded" || len(traces[0].InstructionTraces) != 1 || len(audits) != 0 {
		t.Fatalf("traces=%#v audits=%v err=%v", traces, audits, err)
	}
	simulation, err := service.SimulateAutomationRule(t.Context(), "", automationcontract.AutomationSimulationRequest{
		Rule:  &rule,
		Input: map[string]any{"active": true},
	}, principal)
	if err != nil || !simulation.WouldSave || simulation.Status != "succeeded" || simulation.Candidate["normalized"] != true {
		t.Fatalf("before simulation=%#v err=%v", simulation, err)
	}
	if _, err := service.RunBefore(t.Context(), "order", "create", "record-1", nil, nil, map[string]any{}, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization err=%v", err)
	}
	service.workflows = automationFacadeWorkflowProbe{run: workflowmodel.WorkflowRunResult{Execution: workflowmodel.WorkflowExecution{ID: "workflow-execution", Status: "completed"}}}
	service.invokeAction = func(context.Context, actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error) {
		return actionmodel.ActionInvocationResult{InvocationID: "action-invocation", Output: map[string]any{"ok": true}}, nil
	}
	actionContext := &automationmodel.AutomationRenderContext{Payload: map[string]any{"id": "1"}, Input: map[string]any{}, Before: map[string]any{}, Results: map[string]automationmodel.AutomationInstructionResult{}}
	for _, instruction := range []automationmodel.AutomationInstructionSchema{
		{Key: "action", Type: "invoke_business_action", Config: map[string]any{"action_key": "order.normalize"}},
		{Key: "workflow", Type: "start_workflow", Config: map[string]any{"workflow_key": "flow", "payload": map[string]any{"id": "${payload.id}"}}},
		{Key: "event", Type: "emit_event", Config: map[string]any{"event": "done"}},
	} {
		if result, err := service.executeInstruction(t.Context(), rule, instruction, actionContext, nil, principal); err != nil || result.Status != "success" {
			t.Fatalf("instruction=%#v result=%#v err=%v", instruction, result, err)
		}
	}
}
