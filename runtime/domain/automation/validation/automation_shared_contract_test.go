package validation

import (
	"testing"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestAutomationOutputsAreOrderedTypedAndAliasScoped(t *testing.T) {
	catalog := automationValidatorCatalog()
	catalog.Actions = []definitionmodel.ActionSchema{
		{Key: "order.inspect", ObjectKey: "order", OutputFields: []definitionmodel.ActionOutputField{{Key: "score", Type: "integer"}}},
		{Key: "order.record_score", ObjectKey: "order", PayloadFields: []definitionmodel.ActionPayloadField{{Key: "score", Type: "integer", Required: true}}},
	}
	validator := AutomationDefinitionValidator{Catalog: catalog}
	rule := automationValidatorRule()
	rule.Trigger.Phase = "after"
	rule.Instructions = []automationmodel.AutomationInstructionSchema{
		{Key: "inspect", ResultAlias: "inspection", Type: "invoke_business_action", Config: map[string]any{"action_key": "order.inspect"}},
		{Key: "record", Type: "invoke_business_action", Config: map[string]any{"action_key": "order.record_score", "input": map[string]any{"score": "$actions.inspection.data.score"}}},
	}
	if err := validator.Validate(t.Context(), rule); err != nil {
		t.Fatalf("ordered typed Action output rejected: %v", err)
	}
	rule.Instructions[1].Config["input"] = map[string]any{"score": "$steps.inspection.data.score"}
	if err := validator.Validate(t.Context(), rule); err != nil {
		t.Fatalf("ordered typed step output rejected: %v", err)
	}
	rule.Instructions[0], rule.Instructions[1] = rule.Instructions[1], rule.Instructions[0]
	assertAutomationErrorCode(t, validator.Validate(t.Context(), rule), "backend.automation.business_action_input_reference_unknown")
}

func TestAutomationConditionsAndWorkflowInvocationsShareRuntimeContracts(t *testing.T) {
	catalog := automationValidatorCatalog()
	catalog.Workflows = []definitionmodel.WorkflowSchema{{
		Key: "order.review", Enabled: true, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"},
		InputFields: []definitionmodel.WorkflowInputField{{Key: "reason", Type: "text", Required: true}},
	}}
	validator := AutomationDefinitionValidator{Catalog: catalog}
	rule := automationValidatorRule()
	rule.Trigger.Phase = "after"
	rule.Conditions = automationmodel.AutomationConditionGroup{Clauses: []automationmodel.AutomationConditionClause{{Reference: "$payload.amount", Operator: "gt", Value: true}}}
	rule.Instructions = []automationmodel.AutomationInstructionSchema{{Key: "review", Type: "start_workflow", Config: map[string]any{"workflow_key": "order.review", "payload": map[string]any{"reason": "check"}}}}
	assertAutomationErrorCode(t, validator.Validate(t.Context(), rule), "backend.automation.condition_type_mismatch")

	rule.Conditions = automationmodel.AutomationConditionGroup{Clauses: []automationmodel.AutomationConditionClause{{Reference: "$payload.unknown", Operator: "eq", Value: "x"}}}
	assertAutomationErrorCode(t, validator.Validate(t.Context(), rule), "backend.automation.condition_reference_unknown")

	rule.Conditions = automationmodel.AutomationConditionGroup{}
	rule.Instructions[0].Config["payload"] = map[string]any{}
	assertAutomationErrorCode(t, validator.Validate(t.Context(), rule), "backend.automation.workflow_input_required")

	catalog.Workflows[0].Enabled = false
	validator.Catalog = catalog
	rule.Instructions[0].Config["payload"] = map[string]any{"reason": "check"}
	assertAutomationErrorCode(t, validator.Validate(t.Context(), rule), "backend.automation.workflow_workflow_disabled")
}
