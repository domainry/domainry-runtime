package validation

import (
	"testing"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestAutomationValidateConditionGroupContracts(t *testing.T) {
	tests := []struct {
		name  string
		group automationmodel.AutomationConditionGroup
		depth int
		code  string
	}{
		{"depth", automationmodel.AutomationConditionGroup{}, 21, "backend.automation.condition_depth_exceeded"},
		{"mode", automationmodel.AutomationConditionGroup{Mode: "none"}, 0, "backend.automation.condition_mode_invalid"},
		{"reference", automationmodel.AutomationConditionGroup{Clauses: []automationmodel.AutomationConditionClause{{Operator: "eq"}}}, 0, "backend.automation.condition_reference_required"},
		{"operator", automationmodel.AutomationConditionGroup{Clauses: []automationmodel.AutomationConditionClause{{Reference: "$record.id", Operator: "contains"}}}, 0, "backend.automation.condition_operator_invalid"},
		{"nested", automationmodel.AutomationConditionGroup{Groups: []automationmodel.AutomationConditionGroup{{Mode: "none"}}}, 0, "backend.automation.condition_mode_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertAutomationErrorCode(t, AutomationValidateConditionGroup(test.group, "conditions", test.depth), test.code)
		})
	}
	validOperators := []string{"empty", "eq", "future_date", "gt", "gte", "in", "lt", "lte", "ne", "not_empty"}
	for _, operator := range validOperators {
		group := automationmodel.AutomationConditionGroup{Mode: "any", Clauses: []automationmodel.AutomationConditionClause{{Reference: "$record.amount", Operator: operator}}}
		if err := AutomationValidateConditionGroup(group, "conditions", 0); err != nil {
			t.Fatalf("operator %q should be valid: %v", operator, err)
		}
	}
}

func TestAutomationValidateTriggerFilters(t *testing.T) {
	object := automationValidatorCatalog().Objects[0]
	tests := []struct {
		name    string
		trigger automationmodel.AutomationTriggerSchema
		code    string
	}{
		{"changed field on create", automationmodel.AutomationTriggerSchema{Operation: "create", ChangedFields: []string{"amount"}}, "backend.automation.changed_fields_operation_invalid"},
		{"unknown changed field", automationmodel.AutomationTriggerSchema{Operation: "update", ChangedFields: []string{"missing"}}, "backend.automation.changed_field_not_found"},
		{"transition filter on update", automationmodel.AutomationTriggerSchema{Operation: "update", FromState: "draft"}, "backend.automation.transition_filter_operation_invalid"},
		{"unknown from state", automationmodel.AutomationTriggerSchema{Operation: "transition", FromState: "missing"}, "backend.automation.transition_state_invalid"},
		{"unknown to state", automationmodel.AutomationTriggerSchema{Operation: "transition", ToState: "missing"}, "backend.automation.transition_state_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertAutomationErrorCode(t, AutomationValidateTriggerFilters(test.trigger, object), test.code)
		})
	}
	for _, trigger := range []automationmodel.AutomationTriggerSchema{
		{Operation: "create"},
		{Operation: "update", ChangedFields: []string{"amount"}},
		{Operation: "transition", FromState: "draft", ToState: "approved"},
	} {
		if err := AutomationValidateTriggerFilters(trigger, object); err != nil {
			t.Fatalf("expected valid trigger %#v: %v", trigger, err)
		}
	}
}

func TestAutomationValidateTriggerFiltersFallsBackToFirstSelect(t *testing.T) {
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{
		{Key: "name", Type: "text"},
		{Key: "lifecycle", Type: "select", Validation: definitionmodel.FieldValidation{Options: []string{"open", "closed"}}},
	}}
	if err := AutomationValidateTriggerFilters(automationmodel.AutomationTriggerSchema{Operation: "transition", FromState: "open", ToState: "closed"}, object); err != nil {
		t.Fatal(err)
	}
	assertAutomationErrorCode(t, AutomationValidateTriggerFilters(automationmodel.AutomationTriggerSchema{Operation: "transition", ToState: "unknown"}, definitionmodel.ObjectSchema{}), "backend.automation.transition_state_invalid")
}
