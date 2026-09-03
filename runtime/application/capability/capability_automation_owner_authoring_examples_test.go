package capability

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	automationpolicy "github.com/domainry/domainry-runtime/runtime/domain/automation/policy"
	automationvalidation "github.com/domainry/domainry-runtime/runtime/domain/automation/validation"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestAutomationInstructionExamplesExecuteOwnerValidator(t *testing.T) {
	validator := automationOwnerExampleValidator()
	for _, capability := range automationpolicy.AutomationAuthoringDomain().Capabilities {
		if len(capability.Key) < len("automation.instruction.") || capability.Key[:len("automation.instruction.")] != "automation.instruction." {
			continue
		}
		for _, example := range capability.Examples {
			normalized := automationvalidation.AutomationAuthoringFragmentWithDefaults(capability.Key, example.Value)
			payload, err := json.Marshal(normalized)
			if err != nil {
				t.Fatal(err)
			}
			var instruction automationmodel.AutomationInstructionSchema
			if err := json.Unmarshal(payload, &instruction); err != nil {
				t.Fatal(err)
			}
			phase, operation := "after", "update"
			if instruction.Type == "assert" || instruction.Type == "derive_fields" {
				phase = "before"
			}
			rule := automationmodel.AutomationRuleSchema{
				Key: "order.example", Name: "Order example", ObjectKey: "order", Enabled: true,
				Trigger: automationmodel.AutomationTriggerSchema{Phase: phase, Operation: operation}, Instructions: []automationmodel.AutomationInstructionSchema{instruction},
			}
			err = validator.Validate(t.Context(), rule)
			if len(example.ExpectedErrorCodes) == 0 {
				if err != nil {
					t.Fatalf("capability=%s example=%s err=%v", capability.Key, example.Name, err)
				}
				continue
			}
			if code := apperror.CodeOf(err); code != example.ExpectedErrorCodes[0] {
				t.Fatalf("capability=%s example=%s code=%s want=%s err=%v", capability.Key, example.Name, code, example.ExpectedErrorCodes[0], err)
			}
		}
	}
}

func TestAutomationRuleAndComponentExamplesExecuteOwnerValidator(t *testing.T) {
	validator := automationOwnerExampleValidator()
	for _, capability := range automationpolicy.AutomationAuthoringDomain().Capabilities {
		if strings.HasPrefix(capability.Key, "automation.instruction.") {
			continue
		}
		for _, example := range capability.Examples {
			rule := automationOwnerExampleRule(t, capability.Key, example.Value)
			err := validator.Validate(t.Context(), rule)
			if len(example.ExpectedErrorCodes) == 0 {
				if err != nil {
					t.Fatalf("capability=%s example=%s err=%v", capability.Key, example.Name, err)
				}
				continue
			}
			if code := apperror.CodeOf(err); code != example.ExpectedErrorCodes[0] {
				t.Fatalf("capability=%s example=%s code=%s want=%s err=%v", capability.Key, example.Name, code, example.ExpectedErrorCodes[0], err)
			}
		}
	}
}

func automationOwnerExampleValidator() automationvalidation.AutomationDefinitionValidator {
	return automationvalidation.AutomationDefinitionValidator{Catalog: automationvalidation.AutomationDefinitionCatalog{
		Objects:   []definitionmodel.ObjectSchema{{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}}}},
		Actions:   []definitionmodel.ActionSchema{{Key: "order.complete", ObjectKey: "order", PayloadFields: []definitionmodel.ActionPayloadField{{Key: "source", Type: "text"}}}},
		Workflows: []definitionmodel.WorkflowSchema{{Key: "order.approval", Enabled: true, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"}}},
	}}
}

func automationOwnerExampleRule(t *testing.T, capabilityKey string, value map[string]any) automationmodel.AutomationRuleSchema {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if capabilityKey == "automation.rule" {
		var rule automationmodel.AutomationRuleSchema
		if err := json.Unmarshal(payload, &rule); err != nil {
			t.Fatal(err)
		}
		return rule
	}
	rule := automationmodel.AutomationRuleSchema{
		Key: "order.example", Name: "Order example", ObjectKey: "order", Enabled: true,
		Trigger:      automationmodel.AutomationTriggerSchema{Phase: "before", Operation: "create"},
		Instructions: []automationmodel.AutomationInstructionSchema{{Key: "derive", Type: "derive_fields", Config: map[string]any{"fields": map[string]any{"status": "ready"}}}},
	}
	switch capabilityKey {
	case "automation.trigger":
		if err := json.Unmarshal(payload, &rule.Trigger); err != nil {
			t.Fatal(err)
		}
		if rule.Trigger.Phase == "after" {
			rule.Instructions = []automationmodel.AutomationInstructionSchema{{Key: "emit", Type: "emit_event", Config: map[string]any{"event_type": "order.changed"}}}
		}
	case "automation.condition_group":
		if err := json.Unmarshal(payload, &rule.Conditions); err != nil {
			t.Fatal(err)
		}
	case "automation.execution_policy":
		if err := json.Unmarshal(payload, &rule.Execution); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unsupported capability %s", capabilityKey)
	}
	return rule
}
