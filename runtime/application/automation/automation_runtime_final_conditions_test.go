package automation

import (
	"context"
	"errors"
	"testing"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	automationcontract "github.com/domainry/domainry-runtime/runtime/domain/automation/contract"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationprojection "github.com/domainry/domainry-runtime/runtime/domain/automation/projection"
	automationbusiness "github.com/domainry/domainry-runtime/runtime/domain/automation/service"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type automationFailingNotificationCommitter struct{ err error }

func (c automationFailingNotificationCommitter) CommitAutomationExecution(context.Context, automationmodel.AutomationRuleExecution) error {
	return c.err
}

func (c automationFailingNotificationCommitter) CommitAutomationExecutionNotification(context.Context, automationmodel.AutomationRuleExecution, notificationmodel.NotificationEvent) error {
	return c.err
}

func TestExecuteBeforeRuleCoversSkipCancellationRenderAndAssertEdges(t *testing.T) {
	principal := automationFacadePrincipal()
	skipped := automationmodel.AutomationRuleSchema{Key: "skip", Conditions: automationmodel.AutomationConditionGroup{Clauses: []automationmodel.AutomationConditionClause{{Reference: "$payload.active", Operator: "eq", Value: true}}}}
	if trace, err := executeBeforeRule(t.Context(), skipped, nil, nil, map[string]any{"active": false}, principal); err != nil || trace.Matched || trace.Status != "skipped" {
		t.Fatalf("skip trace=%#v err=%v", trace, err)
	}

	canceledContext, cancel := context.WithCancel(t.Context())
	cancel()
	rule := automationmodel.AutomationRuleSchema{Key: "before", Instructions: []automationmodel.AutomationInstructionSchema{{Key: "assert", Type: "assert", Config: map[string]any{}}}}
	if trace, err := executeBeforeRule(canceledContext, rule, nil, nil, map[string]any{}, principal); err == nil || trace.Status != "blocked" {
		t.Fatalf("cancel trace=%#v err=%v", trace, err)
	}

	rule.Instructions = []automationmodel.AutomationInstructionSchema{{Key: "derive", Type: "derive_fields", Config: map[string]any{"fields": "not-an-object"}}}
	if _, err := executeBeforeRule(t.Context(), rule, nil, nil, map[string]any{}, principal); err == nil {
		t.Fatal("non-object derived fields accepted")
	}
	candidate := map[string]any{}
	rule.Instructions = []automationmodel.AutomationInstructionSchema{{Key: "derive", Type: "derive_fields", Config: map[string]any{"fields": map[string]any{" ": "ignored", "status": "ready"}}}}
	if trace, err := executeBeforeRule(t.Context(), rule, nil, nil, candidate, principal); err != nil || trace.Status != "succeeded" || candidate["status"] != "ready" {
		t.Fatalf("derive trace=%#v candidate=%#v err=%v", trace, candidate, err)
	}

	rule.Instructions = []automationmodel.AutomationInstructionSchema{{Key: "assert", Type: "assert", Config: map[string]any{"source": "$payload.active", "operator": "eq", "value": true}}}
	if _, err := executeBeforeRule(t.Context(), rule, nil, nil, map[string]any{"active": false}, principal); err == nil {
		t.Fatal("failed assertion accepted")
	}
	if trace, err := executeBeforeRule(t.Context(), rule, nil, nil, map[string]any{"active": true}, principal); err != nil || trace.Status != "succeeded" {
		t.Fatalf("successful assertion trace=%#v err=%v", trace, err)
	}
}

func TestAutomationTerminalNotificationCompoundConditions(t *testing.T) {
	compiler := func(intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{EventType: intent.EventType}, nil
	}
	service := NewAutomationApplicationService(AutomationApplicationDependencies{NotificationCompiler: compiler})
	rule := automationmodel.AutomationRuleSchema{Key: "rule", Execution: automationmodel.AutomationExecutionPolicy{ResultNotification: "none"}}
	trace := automationprojection.AutomationRuleTrace{ExecutionID: "execution", Status: "failed"}
	principal := automationFacadePrincipal()
	principal.UserID = ""
	if _, notify, err := service.terminalResultNotification(rule, trace, principal, nil); err != nil || notify {
		t.Fatalf("empty user notify=%v err=%v", notify, err)
	}
	principal.UserID = "operator"
	if _, notify, err := service.terminalResultNotification(rule, trace, principal, nil); err != nil || notify {
		t.Fatalf("none mode notify=%v err=%v", notify, err)
	}
	rule.Execution.ResultNotification = "failures"
	trace.Status = "succeeded"
	if _, notify, err := service.terminalResultNotification(rule, trace, principal, nil); err != nil || notify {
		t.Fatalf("successful failures-only notify=%v err=%v", notify, err)
	}
	trace.Status = "failed"
	if _, notify, err := service.terminalResultNotification(rule, trace, principal, nil); err != nil || !notify {
		t.Fatalf("failed status notify=%v err=%v", notify, err)
	}
	trace.Status = "blocked"
	if _, notify, err := service.terminalResultNotification(rule, trace, principal, nil); err != nil || !notify {
		t.Fatalf("blocked status notify=%v err=%v", notify, err)
	}
}

func TestAutomationSimulationSaveTargetWithAfterRuleDoesNotUseBeforeShortcut(t *testing.T) {
	rule := automationmodel.AutomationRuleSchema{Key: "after", Enabled: true, Trigger: automationmodel.AutomationTriggerSchema{Phase: "after"}}
	service := newAutomationFacade(&automationFacadeRegistry{rules: map[string]automationmodel.AutomationRuleSchema{"after": rule}}, &automationFacadeMetadataProbe{})
	result, err := service.SimulateAutomationRule(t.Context(), "after", automationcontract.AutomationSimulationRequest{TargetNodeID: "save"}, automationFacadePrincipal())
	if err != nil || result.TestedNodeID != "save" {
		t.Fatalf("simulation=%#v err=%v", result, err)
	}
}

func TestAutomationFailingNotificationCommitterImplementsBothPaths(t *testing.T) {
	wantErr := errors.New("commit")
	committer := automationFailingNotificationCommitter{err: wantErr}
	if !errors.Is(committer.CommitAutomationExecution(t.Context(), automationmodel.AutomationRuleExecution{}), wantErr) {
		t.Fatal("execution commit error lost")
	}
	if !errors.Is(committer.CommitAutomationExecutionNotification(t.Context(), automationmodel.AutomationRuleExecution{}, notificationmodel.NotificationEvent{}), wantErr) {
		t.Fatal("notification commit error lost")
	}
}

func TestAutomationOutboxCoversTerminalCommitMatrix(t *testing.T) {
	rule := automationmodel.AutomationRuleSchema{Key: "after", ObjectKey: "order", Enabled: true, Trigger: automationmodel.AutomationTriggerSchema{Phase: "after"}}
	registry := &automationFacadeRegistry{rules: map[string]automationmodel.AutomationRuleSchema{"after": rule}}
	message := integrationmodel.IntegrationOutboxMessage{WorkspaceID: "workspace-1", Payload: automationbusiness.LifecycleEventPayload(automationmodel.AutomationLifecycleEvent{
		RuleKey: "after", RecordVersion: "v1", Record: recordmodel.Record{ID: "order-1"}, ActorUserID: "operator", ActorRoleKey: "admin",
	})}

	service := newAutomationFacade(registry, &automationFacadeMetadataProbe{})
	service.compileNotification = func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{}, errors.New("compile")
	}
	rule.Execution.ResultNotification = "all"
	registry.rules["after"] = rule
	if err := service.ExecuteOutboxMessage(t.Context(), message); apperror.CodeOf(err) != "backend.automation.notification_compile_failed" {
		t.Fatalf("compile error=%v", err)
	}

	service.compileNotification = func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{}, nil
	}
	if err := service.ExecuteOutboxMessage(t.Context(), message); apperror.CodeOf(err) != "backend.automation.notification_committer_unavailable" {
		t.Fatalf("missing committer error=%v", err)
	}

	rule.Execution.ResultNotification = ""
	registry.rules["after"] = rule
	service.compileNotification = nil
	service.commitNotification = automationFailingNotificationCommitter{err: errors.New("commit")}
	if err := service.ExecuteOutboxMessage(t.Context(), message); apperror.CodeOf(err) != "backend.automation.execution_commit_failed" {
		t.Fatalf("execution commit error=%v", err)
	}

	service.commitNotification = nil
	service.executionRepo = nil
	if err := service.ExecuteOutboxMessage(t.Context(), message); err != nil {
		t.Fatalf("optional persistence error=%v", err)
	}

	rule.Execution.ResultNotification = "all"
	registry.rules["after"] = rule
	service.compileNotification = func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{}, nil
	}
	service.commitNotification = automationFailingNotificationCommitter{err: errors.New("commit notification")}
	if err := service.ExecuteOutboxMessage(t.Context(), message); apperror.CodeOf(err) != "backend.automation.execution_commit_failed" {
		t.Fatalf("notification commit error=%v", err)
	}
}
