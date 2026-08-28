// Before domain service tests.
package automation

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationprojection "github.com/domainry/domainry-runtime/runtime/domain/automation/projection"

	"context"
	"errors"
	"testing"
	"time"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type beforeRuleRegistry struct {
	rules []automationmodel.AutomationRuleSchema
}

func TestBeforeServiceExplicitTimeoutAndStringMiss(t *testing.T) {
	rule := enabledBeforeRule("slow", 1)
	rule.Execution.TimeoutSeconds = 1
	service := NewAutomationBeforeApplicationService(BeforeServiceDependencies{
		Rules: beforeRuleRegistry{rules: []automationmodel.AutomationRuleSchema{rule}},
		ExecuteRule: func(ctx context.Context, _ automationmodel.AutomationRuleSchema, _, _, _ map[string]any, _ string, _ principalmodel.Principal) (automationprojection.AutomationRuleTrace, error) {
			<-ctx.Done()
			return automationprojection.AutomationRuleTrace{RuleKey: "slow"}, nil
		},
	})
	started := time.Now()
	if _, err := service.Run(t.Context(), "order", "create", "", nil, nil, map[string]any{}, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1"}}); apperror.CodeOf(err) != "backend.automation.rule_timeout" {
		t.Fatalf("timeout error=%v", err)
	}
	if time.Since(started) < time.Second || stringSliceContains([]string{"other"}, "slow") {
		t.Fatal("explicit timeout or string miss was not observed")
	}
}

func TestBeforeServiceExplicitDepthAndRepeatedEqualWrite(t *testing.T) {
	first, second := enabledBeforeRule("first", 1), enabledBeforeRule("second", 2)
	first.Execution.MaxDepth, second.Execution.MaxDepth = 3, 3
	service := NewAutomationBeforeApplicationService(BeforeServiceDependencies{
		Rules: beforeRuleRegistry{rules: []automationmodel.AutomationRuleSchema{first, second}},
		ExecuteRule: func(_ context.Context, rule automationmodel.AutomationRuleSchema, _, _, candidate map[string]any, _ string, _ principalmodel.Principal) (automationprojection.AutomationRuleTrace, error) {
			candidate["status"] = "same"
			return automationprojection.AutomationRuleTrace{RuleKey: rule.Key, Status: "succeeded"}, nil
		},
	})
	if traces, err := service.Run(t.Context(), "order", "create", "", nil, nil, map[string]any{}, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1"}}); err != nil || len(traces) != 2 {
		t.Fatalf("traces=%+v err=%v", traces, err)
	}
}

func (r beforeRuleRegistry) List() []automationmodel.AutomationRuleSchema {
	return append([]automationmodel.AutomationRuleSchema(nil), r.rules...)
}

func (r beforeRuleRegistry) Get(key string) (automationmodel.AutomationRuleSchema, bool) {
	for _, rule := range r.rules {
		if rule.Key == key {
			return rule, true
		}
	}
	return automationmodel.AutomationRuleSchema{}, false
}

func TestBeforeServiceRejectsRecursiveRule(t *testing.T) {
	rule := enabledBeforeRule("normalize", 1)
	service := NewAutomationBeforeApplicationService(BeforeServiceDependencies{
		Rules: beforeRuleRegistry{rules: []automationmodel.AutomationRuleSchema{rule}},
		ExecuteRule: func(context.Context, automationmodel.AutomationRuleSchema, map[string]any, map[string]any, map[string]any, string, principalmodel.Principal) (automationprojection.AutomationRuleTrace, error) {
			t.Fatal("recursive rule must not execute")
			return automationprojection.AutomationRuleTrace{}, nil
		},
	})
	_, err := service.Run(t.Context(), "order", "create", "", nil, nil, map[string]any{}, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1"}, VisitedRuleKeys: []string{" normalize "}})
	assertAutomationError(t, err, apperror.KindBadRequest, "backend.automation.recursion_detected")
}

func TestBeforeServiceRejectsConflictingRuleWrites(t *testing.T) {
	service := NewAutomationBeforeApplicationService(BeforeServiceDependencies{
		Rules: beforeRuleRegistry{rules: []automationmodel.AutomationRuleSchema{
			enabledBeforeRule("first", 1), enabledBeforeRule("second", 2),
		}},
		ExecuteRule: func(_ context.Context, rule automationmodel.AutomationRuleSchema, _, _, candidate map[string]any, _ string, principal principalmodel.Principal) (automationprojection.AutomationRuleTrace, error) {
			if principal.AutomationDepth != 1 || principal.VisitedRuleKeys[len(principal.VisitedRuleKeys)-1] != rule.Key {
				t.Fatalf("owner did not propagate recursion context: %#v", principal)
			}
			candidate["status"] = rule.Key
			return automationprojection.AutomationRuleTrace{RuleKey: rule.Key, Status: "succeeded"}, nil
		},
	})
	candidate := map[string]any{"status": "draft"}
	traces, err := service.Run(t.Context(), "order", "create", "order-1", nil, nil, candidate, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1"}})
	if len(traces) != 2 {
		t.Fatalf("expected both traces before conflict, got %#v", traces)
	}
	assertAutomationError(t, err, apperror.KindBadRequest, "backend.automation.write_conflict")
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Params["first_rule"] != "first" || appErr.Params["second_rule"] != "second" {
		t.Fatalf("unexpected conflict params: %#v", err)
	}
}

func enabledBeforeRule(key string, priority int) automationmodel.AutomationRuleSchema {
	return automationmodel.AutomationRuleSchema{
		Key: key, ObjectKey: "order", Enabled: true, Priority: priority,
		Trigger: automationmodel.AutomationTriggerSchema{Phase: "before", Operation: "create"},
	}
}

func assertAutomationError(t *testing.T, err error, kind apperror.ErrorKind, code string) {
	t.Helper()
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Kind != kind || appErr.Code != code {
		t.Fatalf("unexpected error: %#v", err)
	}
}
