package automation

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationprojection "github.com/domainry/domainry-runtime/runtime/domain/automation/projection"
	automationbusiness "github.com/domainry/domainry-runtime/runtime/domain/automation/service"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestAutomationAuthoringFragmentRejectsUnknownPrincipal(t *testing.T) {
	service := NewAutomationApplicationService(AutomationApplicationDependencies{})
	if _, err := service.ValidateAutomationAuthoringFragment(
		t.Context(),
		"automation.instruction.emit_event",
		map[string]any{},
		principalmodel.Principal{},
	); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("unknown principal error=%v", err)
	}

	readOnly := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true,
		WorkspaceID: "workspace-1"},
	}, accessfixture.Bundle{Permissions: []string{"automation.rule.read"}},
	)
	if _, err := service.ValidateAutomationAuthoringFragment(
		t.Context(), "automation.trigger", map[string]any{}, readOnly,
	); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("read-only principal error=%v", err)
	}

	manager := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true,
		WorkspaceID: "workspace-1"},
	}, accessfixture.Bundle{Permissions: []string{"automation.rule.write"}},
	)
	if result, err := service.ValidateAutomationAuthoringFragment(
		t.Context(),
		"automation.trigger",
		map[string]any{"phase": "after", "operation": "update"},
		manager,
	); err != nil || !result.Valid {
		t.Fatalf("valid fragment result=%+v err=%v", result, err)
	}
	if result, err := service.ValidateAutomationAuthoringFragment(
		t.Context(), "automation.unknown", map[string]any{}, manager,
	); err != nil || result.Valid || len(result.Errors) != 1 {
		t.Fatalf("invalid fragment result=%+v err=%v", result, err)
	}

	service = newAutomationFacade(&automationFacadeRegistry{rules: map[string]automationmodel.AutomationRuleSchema{}}, &automationFacadeMetadataProbe{})
	if versions, err := service.AutomationRuleVersions(t.Context(), " rule-1 ", manager); err != nil ||
		len(versions) != 1 || versions[0].ResourceKey != "rule-1" {
		t.Fatalf("rule versions=%+v err=%v", versions, err)
	}
}

func TestAutomationRunBeforePersistsExecutionAndAfterRuleStoresResult(t *testing.T) {
	beforeRule := automationmodel.AutomationRuleSchema{
		Key: "before", ObjectKey: "order", Enabled: true,
		Trigger: automationmodel.AutomationTriggerSchema{Phase: "before", Operation: "create"},
	}
	repository := &executionRepositoryStub{}
	service := NewAutomationApplicationService(AutomationApplicationDependencies{
		Rules:               &automationFacadeRegistry{rules: map[string]automationmodel.AutomationRuleSchema{"before": beforeRule}},
		ExecutionRepository: repository,
		WorkerStore: instructionRepositoryStub{
			claim: func(_ context.Context, execution automationmodel.AutomationInstructionExecution, _, _ string) (automationmodel.AutomationInstructionExecution, bool, error) {
				execution.ID, execution.LeaseOwner, execution.FencingToken = "instruction-1", "worker", 1
				return execution, true, nil
			},
			complete: func(context.Context, string, string, string, int64, string, map[string]any, string) (automationmodel.AutomationInstructionExecution, error) {
				return automationmodel.AutomationInstructionExecution{}, nil
			},
		},
	})
	principal := automationFacadePrincipal()
	if traces, err := service.RunBefore(
		t.Context(), "order", "create", "order-1", nil, nil, map[string]any{"active": true}, principal,
	); err != nil || len(traces) != 1 || len(repository.inserted) != 1 {
		t.Fatalf("before traces=%+v inserted=%+v err=%v", traces, repository.inserted, err)
	}

	afterRule := automationmodel.AutomationRuleSchema{
		Key: "after", ObjectKey: "order", Enabled: true,
		Trigger: automationmodel.AutomationTriggerSchema{Phase: "after", Operation: "update"},
		Conditions: automationmodel.AutomationConditionGroup{Clauses: []automationmodel.AutomationConditionClause{{
			Reference: "$payload.active", Operator: "eq", Value: true,
		}}},
		Instructions: []automationmodel.AutomationInstructionSchema{{
			Key: "event", Type: "emit_event", ResultAlias: "event_result",
			Config: map[string]any{"event_type": "order.ready"},
		}},
	}
	trace, err := service.executeRule(
		t.Context(), afterRule, "after", nil, nil, map[string]any{"active": true}, nil, principal,
	)
	if err != nil || trace.Status != "succeeded" || len(trace.InstructionTraces) != 1 {
		t.Fatalf("after trace=%+v err=%v", trace, err)
	}
}

func TestAutomationOutboxRejectsRevalidatedRoleMismatch(t *testing.T) {
	rule := automationmodel.AutomationRuleSchema{
		Key: "after", ObjectKey: "order", Enabled: true,
		Trigger: automationmodel.AutomationTriggerSchema{Phase: "after", Operation: "update"},
	}
	service := NewAutomationApplicationService(AutomationApplicationDependencies{
		Rules: &automationFacadeRegistry{rules: map[string]automationmodel.AutomationRuleSchema{"after": rule}},
		Principal: func(_ context.Context, userID, _ string, _ string) principalmodel.Principal {
			return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true,
				UserID: userID},
			}, accessfixture.Bundle{
				Key:         "replacement-role",
				Permissions: []string{"workspace.admin"},
			},
			)
		},
	})
	event := automationmodel.AutomationLifecycleEvent{
		RuleKey: "after", ActorUserID: "operator", ActorRoleKey: "original-role",
		Record: recordmodel.Record{ID: "order-1"},
	}
	message := integrationmodel.IntegrationOutboxMessage{
		WorkspaceID: "workspace-1",
		Payload:     automationbusiness.LifecycleEventPayload(event),
	}
	if err := service.ExecuteOutboxMessage(t.Context(), message); apperror.CodeOf(err) != "backend.automation.identity_revoked" {
		t.Fatalf("role mismatch error=%v", err)
	}
}

func TestAutomationEmitInstructionEventFallbacksAndErrors(t *testing.T) {
	service := NewAutomationApplicationService(AutomationApplicationDependencies{})
	principal := automationFacadePrincipal()

	result, err := service.emitInstructionEvent(
		t.Context(),
		automationmodel.AutomationRuleSchema{Key: "rule", ObjectKey: "order", AuditEvent: "order.changed"},
		automationmodel.AutomationInstructionSchema{Key: "emit"},
		&automationmodel.AutomationRenderContext{Record: map[string]any{"id": "order-1"}},
		principal,
	)
	if err != nil || result["event_type"] != "order.changed" || result["record_id"] != "order-1" {
		t.Fatalf("fallback event result=%v err=%v", result, err)
	}

	if _, err := service.emitInstructionEvent(
		t.Context(),
		automationmodel.AutomationRuleSchema{},
		automationmodel.AutomationInstructionSchema{Key: "missing-event"},
		nil,
		principal,
	); apperror.CodeOf(err) != "backend.automation.event_type_required" {
		t.Fatalf("missing event type error=%v", err)
	}

	result, err = service.emitInstructionEvent(
		t.Context(),
		automationmodel.AutomationRuleSchema{Key: "rule", ObjectKey: "order"},
		automationmodel.AutomationInstructionSchema{
			Key:    "nil-render",
			Config: map[string]any{"event_type": "order.ready"},
		},
		nil,
		principal,
	)
	if err != nil || result["record_id"] != "" {
		t.Fatalf("nil render result=%v err=%v", result, err)
	}

	result, err = service.emitInstructionEvent(
		t.Context(),
		automationmodel.AutomationRuleSchema{Key: "rule", ObjectKey: "order"},
		automationmodel.AutomationInstructionSchema{
			Key: "explicit",
			Config: map[string]any{
				"event_type": "order.ready",
				"record_id":  "order-2",
				"metadata":   map[string]any{"source": "test"},
			},
		},
		nil,
		principal,
	)
	if err != nil || result["record_id"] != "order-2" {
		t.Fatalf("explicit event result=%v err=%v", result, err)
	}

	if _, err := service.emitInstructionEvent(
		t.Context(),
		automationmodel.AutomationRuleSchema{},
		automationmodel.AutomationInstructionSchema{
			Key:    "bad-metadata",
			Config: map[string]any{"event_type": "order.ready", "metadata": "not-an-object"},
		},
		nil,
		principal,
	); apperror.CodeOf(err) != "backend.action.data_object_required" {
		t.Fatalf("metadata error=%v", err)
	}
}

func TestAutomationTerminalResultNotificationIsExplicitAndTyped(t *testing.T) {
	intents := []notificationmodel.NotificationIntent{}
	service := NewAutomationApplicationService(AutomationApplicationDependencies{NotificationCompiler: func(intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		intents = append(intents, intent)
		return notificationmodel.NotificationEvent{EventType: intent.EventType}, nil
	}})
	principal := automationFacadePrincipal()
	principal.UserID = "operator-1"
	trace := automationprojection.AutomationRuleTrace{ExecutionID: "execution-1", RuleKey: "notify", Status: "succeeded"}
	rule := automationmodel.AutomationRuleSchema{Key: "notify", ObjectKey: "order"}
	if _, notify, err := service.terminalResultNotification(rule, trace, principal, nil); err != nil || notify {
		t.Fatalf("implicit mode notify=%v err=%v", notify, err)
	}
	rule.Execution.ResultNotification = "failures"
	if _, notify, err := service.terminalResultNotification(rule, trace, principal, nil); err != nil || notify {
		t.Fatalf("success failures-only notify=%v err=%v", notify, err)
	}
	if len(intents) != 0 {
		t.Fatalf("optional success notification emitted without all policy: %+v", intents)
	}
	rule.Execution.ResultNotification = "all"
	if _, notify, err := service.terminalResultNotification(rule, trace, principal, nil); err != nil || !notify {
		t.Fatalf("all mode notify=%v err=%v", notify, err)
	}
	if len(intents) != 1 || intents[0].EventType != "automation.execution.completed" || intents[0].SubjectType != "automation_rule" || intents[0].SubjectID != "notify" || len(intents[0].Variables) != 5 {
		t.Fatalf("completed intent=%+v", intents)
	}
	rule.Execution.ResultNotification = "failures"
	trace.ExecutionID, trace.Status, trace.ErrorCode = "execution-2", "blocked", "backend.automation.rule_timeout"
	if _, notify, err := service.terminalResultNotification(rule, trace, principal, errors.New("execution failed")); err != nil || !notify {
		t.Fatalf("failed notify=%v err=%v", notify, err)
	}
	if len(intents) != 2 || intents[1].EventType != "automation.execution.failed" || intents[1].Variables["error_code"] != "backend.automation.rule_timeout" {
		t.Fatalf("failed intent=%+v", intents)
	}
	service.compileNotification = func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{}, errors.New("notification unavailable")
	}
	if _, _, err := service.terminalResultNotification(rule, trace, principal, errors.New("execution failed")); err == nil {
		t.Fatal("expected compiler failure")
	}
}
