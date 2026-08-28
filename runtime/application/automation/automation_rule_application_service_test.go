// Rule domain service tests.
package automation

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type executionRepositoryStub struct {
	inserted []automationmodel.AutomationRuleExecution
}

func (s *executionRepositoryStub) InsertExecution(_ context.Context, _ string, value automationmodel.AutomationRuleExecution) (automationmodel.AutomationRuleExecution, error) {
	s.inserted = append(s.inserted, value)
	return value, nil
}

func (s *executionRepositoryStub) ListExecutions(context.Context, string, automationmodel.AutomationExecutionFilter) ([]automationmodel.AutomationRuleExecution, error) {
	return nil, nil
}

func TestRuleServiceTriggerTargetStopsBeforeContextInitialization(t *testing.T) {
	repository := &executionRepositoryStub{}
	initialized := false
	service := NewAutomationRuleApplicationService(repository, nil, nil)
	trace, err := service.Execute(WithSimulation(t.Context(), "trigger"), RuleExecutionRequest{
		Rule:  automationmodel.AutomationRuleSchema{Key: "notify", ObjectKey: "order", Trigger: automationmodel.AutomationTriggerSchema{Operation: "create"}},
		Phase: "after", Record: &recordmodel.Record{ID: "order-1", UpdatedAt: "version-1"}, Candidate: map[string]any{"status": "draft"}, WorkspaceID: "workspace-1",
		Principal:  principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1"}},
		Initialize: func() { initialized = true },
	})
	if err != nil || initialized || len(trace.NodeTraces) != 1 || trace.NodeTraces[0].NodeID != "trigger" {
		t.Fatalf("unexpected target result trace=%#v initialized=%v err=%v", trace, initialized, err)
	}
	if len(repository.inserted) != 1 || repository.inserted[0].RuleKey != "notify" {
		t.Fatalf("execution evidence missing: %#v", repository.inserted)
	}
}

func TestRuleServiceStoresInstructionAliasAndTerminalTrace(t *testing.T) {
	repository := &executionRepositoryStub{}
	stored := map[string]automationmodel.AutomationInstructionResult{}
	rule := automationmodel.AutomationRuleSchema{
		Key: "notify", ObjectKey: "order", Trigger: automationmodel.AutomationTriggerSchema{Operation: "create"},
		Instructions: []automationmodel.AutomationInstructionSchema{{Key: "send", Type: "emit_event", ResultAlias: "notification"}},
	}
	trace, err := NewAutomationRuleApplicationService(repository, nil, nil).Execute(t.Context(), RuleExecutionRequest{
		Rule: rule, Phase: "before", Candidate: map[string]any{}, WorkspaceID: "workspace-1",
		Principal:      principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1"}},
		MatchCondition: func(automationmodel.AutomationConditionClause) bool { return true },
		ExecuteInstruction: func(context.Context, automationmodel.AutomationInstructionSchema) (automationmodel.AutomationInstructionResult, error) {
			return automationmodel.AutomationInstructionResult{Key: "send", Type: "emit_event", Status: "success", Data: map[string]any{"sent": true}}, nil
		},
		StoreResult: func(alias string, result automationmodel.AutomationInstructionResult) { stored[alias] = result },
	})
	if err != nil || trace.Status != "succeeded" || len(trace.InstructionTraces) != 1 {
		t.Fatalf("unexpected rule result trace=%#v err=%v", trace, err)
	}
	if stored["notification"].Data["sent"] != true {
		t.Fatalf("result alias not stored: %#v", stored)
	}
	terminal := trace.NodeTraces[len(trace.NodeTraces)-1]
	if terminal.NodeID != "save" || terminal.Status != "succeeded" {
		t.Fatalf("unexpected terminal trace: %#v", terminal)
	}
}

func TestRuleServiceBeforeRecordEmptyAliasAndActionTarget(t *testing.T) {
	repository := &executionRepositoryStub{}
	rule := automationmodel.AutomationRuleSchema{Key: "rule", ObjectKey: "order", Trigger: automationmodel.AutomationTriggerSchema{Operation: "update"}, Instructions: []automationmodel.AutomationInstructionSchema{{Key: "send", Type: "emit_event"}}}
	request := RuleExecutionRequest{
		Rule: rule, Phase: "before", Record: &recordmodel.Record{ID: "order-1"}, Candidate: map[string]any{}, WorkspaceID: "workspace-1",
		Principal:      principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1"}},
		MatchCondition: func(automationmodel.AutomationConditionClause) bool { return true },
		ExecuteInstruction: func(context.Context, automationmodel.AutomationInstructionSchema) (automationmodel.AutomationInstructionResult, error) {
			return automationmodel.AutomationInstructionResult{Status: "success"}, nil
		},
		StoreResult: func(string, automationmodel.AutomationInstructionResult) {},
	}
	if trace, err := NewAutomationRuleApplicationService(repository, nil, nil).Execute(WithSimulation(t.Context(), "action:send"), request); err != nil || len(trace.InstructionTraces) != 1 {
		t.Fatalf("trace=%+v err=%v", trace, err)
	}
	rule.Instructions[0].Key = ""
	request.Rule = rule
	if trace, err := NewAutomationRuleApplicationService(repository, nil, nil).Execute(t.Context(), request); err != nil || trace.Status != "succeeded" {
		t.Fatalf("empty alias trace=%+v err=%v", trace, err)
	}
	if err := automationError(apperror.KindBadRequest, "code", nil, "", "value"); apperror.CodeOf(err) != "code" {
		t.Fatalf("empty param error=%v", err)
	}
}
