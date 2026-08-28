package service

import (
	"errors"
	"regexp"
	"testing"
	"time"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationruntime "github.com/domainry/domainry-runtime/runtime/domain/automation/runtime"

	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestInstructionEvidenceRoundTrip(t *testing.T) {
	want := automationmodel.AutomationInstructionResult{
		Key: "notify", Type: "emit_event", Status: "succeeded", InvocationID: "inv-1", Data: map[string]any{"sent": true},
	}
	got := automationruntime.AutomationInstructionResult(automationruntime.AutomationInstructionResultMap(want))
	if got.Key != want.Key || got.Type != want.Type || got.Status != want.Status || got.InvocationID != want.InvocationID || got.Data["sent"] != true {
		t.Fatalf("unexpected round trip: %#v", got)
	}
}

func TestInstructionIdempotencyKeyUsesStableUnknowns(t *testing.T) {
	rule := automationmodel.AutomationRuleSchema{Key: "notify", ObjectKey: "order", Trigger: automationmodel.AutomationTriggerSchema{Operation: "update"}}
	instruction := automationmodel.AutomationInstructionSchema{Key: "send"}
	got := automationruntime.AutomationInstructionIdempotencyKey(rule, instruction, nil)
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(got) {
		t.Fatalf("unexpected key: %q", got)
	}
	if ruleKey := automationruntime.AutomationRuleIdempotencyKey(rule, nil); ruleKey == got || !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(ruleKey) {
		t.Fatalf("rule and instruction scopes are not distinct: rule=%q instruction=%q", ruleKey, got)
	}
}

func TestFailedNodeTracePreservesStructuredError(t *testing.T) {
	cause := errors.New("boom")
	err := &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.automation.failed", Params: map[string]string{"instruction": "send"}, Err: cause}
	trace := FailedNodeTrace("action:send", "emit_event", time.Now(), err, "inv-1", map[string]any{"a": 1}, nil)
	if trace.ErrorCode != "backend.automation.failed" || trace.ErrorParams["instruction"] != "send" || trace.InvocationID != "inv-1" {
		t.Fatalf("unexpected trace: %#v", trace)
	}
}
