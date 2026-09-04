package validation_test

import (
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	automationpolicy "github.com/domainry/domainry-runtime/runtime/domain/automation/policy"
	automationvalidation "github.com/domainry/domainry-runtime/runtime/domain/automation/validation"
)

func TestAutomationAuthoringFragmentExamplesExecutePublishedOwnerPolicy(t *testing.T) {
	for _, capability := range automationpolicy.AutomationAuthoringDomain().Capabilities {
		if capability.Key == "automation.rule" {
			continue
		}
		if capability.ValidationEndpoint != "POST /automation/rules/authoring-fragments/{capabilityKey}/validate" {
			t.Fatalf("capability %s validation endpoint=%q", capability.Key, capability.ValidationEndpoint)
		}
		for _, example := range capability.Examples {
			err := automationvalidation.AutomationValidateAuthoringFragment(capability.Key, example.Value)
			code := apperror.CodeOf(err)
			if len(example.ExpectedErrorCodes) == 0 && err != nil {
				t.Fatalf("capability=%s example=%s err=%v", capability.Key, example.Name, err)
			}
			if len(example.ExpectedErrorCodes) > 0 && code != example.ExpectedErrorCodes[0] {
				t.Fatalf("capability=%s example=%s code=%s want=%s", capability.Key, example.Name, code, example.ExpectedErrorCodes[0])
			}
		}
	}
}

func TestAutomationAuthoringFragmentRejectsUnknownCapability(t *testing.T) {
	if code := apperror.CodeOf(automationvalidation.AutomationValidateAuthoringFragment("automation.unknown", map[string]any{})); code != "backend.automation.authoring_capability_unsupported" {
		t.Fatalf("code=%s", code)
	}
}

func TestAutomationAuthoringFragmentRejectsUnknownTopLevelFields(t *testing.T) {
	err := automationvalidation.AutomationValidateAuthoringFragment("automation.instruction.emit_event", map[string]any{
		"key": "emit", "name": "Emit", "on_error": "continue", "config": map[string]any{"event_type": "order.changed"}, "unknown": true,
	})
	if code := apperror.CodeOf(err); code != "backend.automation.authoring_fragment_invalid" {
		t.Fatalf("code=%q err=%v", code, err)
	}
}

func TestAutomationAuthoringFragmentRejectsMalformedPayloadsAndDerivesInstructionType(t *testing.T) {
	tests := []struct {
		capability string
		value      map[string]any
	}{
		{"automation.trigger", map[string]any{"phase": make(chan int)}},
		{"automation.trigger", map[string]any{"phase": 1}},
		{"automation.condition_group", map[string]any{"mode": 1}},
		{"automation.execution_policy", map[string]any{"mode": 1}},
	}
	for _, test := range tests {
		if code := apperror.CodeOf(automationvalidation.AutomationValidateAuthoringFragment(test.capability, test.value)); code != "backend.automation.authoring_fragment_invalid" {
			t.Fatalf("capability=%s code=%s", test.capability, code)
		}
	}
	if err := automationvalidation.AutomationValidateAuthoringFragment(
		"automation.instruction.derive_fields",
		map[string]any{"key": "instruction", "config": map[string]any{"fields": map[string]any{"status": "ready"}}},
	); err != nil {
		t.Fatalf("route-derived instruction type was not applied: %v", err)
	}
}

func TestAutomationAuthoringFragmentRejectsNilAssertSource(t *testing.T) {
	if code := apperror.CodeOf(automationvalidation.AutomationValidateAuthoringFragment(
		"automation.instruction.assert",
		map[string]any{"key": "guard", "type": "assert", "config": map[string]any{"source": nil}},
	)); code != "backend.automation.assert_source_required" {
		t.Fatalf("nil assert source code=%s", code)
	}
	if code := apperror.CodeOf(automationvalidation.AutomationValidateAuthoringFragment(
		"automation.instruction.assert",
		map[string]any{"key": "guard", "type": "assert", "config": map[string]any{"source": ""}},
	)); code != "backend.automation.assert_source_required" {
		t.Fatalf("blank assert source code=%s", code)
	}
}
