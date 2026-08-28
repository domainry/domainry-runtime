package validation

import (
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestAutomationValidationReturnsEveryInvalidInstructionLocation(t *testing.T) {
	rule := automationmodel.AutomationRuleSchema{Key: "customer.invalid", Instructions: []automationmodel.AutomationInstructionSchema{{Key: "first", Type: "magic"}, {Key: "second", Type: "unknown"}}}
	validator := AutomationDefinitionValidator{}
	result, err := AutomationValidateRuleForAuthoring(t.Context(), rule, validator.Validate)
	if err != nil || result.Valid || len(result.Errors) != 3 {
		t.Fatalf("expected two instruction issues and one identity issue: result=%#v err=%v", result, err)
	}
	for index, issue := range result.Errors[:2] {
		if issue.InstructionKey != rule.Instructions[index].Key || issue.Section != "instructions" || issue.FieldPath != "instructions["+stringIndex(index)+"].type" || issue.Params["allowed"] == "" || issue.Params["actual"] == "" || issue.CapabilityKey != "automation.rule" || issue.ContractVersion == "" {
			t.Fatalf("invalid instruction issue %d: %#v", index, issue)
		}
	}
}

func TestAutomationValidationAggregatesInstructionAndRuleIssues(t *testing.T) {
	rule := automationmodel.AutomationRuleSchema{
		Key: "broken", Name: "Broken", ObjectKey: "order",
		Trigger:      automationmodel.AutomationTriggerSchema{Phase: "invented", Operation: "create"},
		Instructions: []automationmodel.AutomationInstructionSchema{{Key: "one", Type: "unknown_one"}, {Key: "two", Type: "unknown_two"}},
	}
	validator := AutomationDefinitionValidator{Catalog: AutomationDefinitionCatalog{Objects: []definitionmodel.ObjectSchema{{Key: "order", Name: "Order"}}}}
	result, err := AutomationValidateRuleForAuthoring(t.Context(), rule, validator.Validate)
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid || len(result.Errors) != 3 {
		t.Fatalf("expected two instruction issues plus phase issue, got %#v", result)
	}
	if result.Errors[0].InstructionKey != "one" || result.Errors[1].InstructionKey != "two" || result.Errors[2].FieldPath != "trigger.phase" {
		t.Fatalf("unexpected aggregated automation issue order: %#v", result.Errors)
	}
}

func stringIndex(index int) string {
	if index == 0 {
		return "0"
	}
	return "1"
}
