package validation

import (
	"testing"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestAutomationDefinitionValidatorRejectsInvalidRuleContracts(t *testing.T) {
	validator := AutomationDefinitionValidator{Catalog: automationValidatorCatalog()}
	valid := automationValidatorRule()
	tests := []struct {
		name string
		edit func(*automationmodel.AutomationRuleSchema)
		code string
	}{
		{"missing key", func(rule *automationmodel.AutomationRuleSchema) { rule.Key = " " }, "backend.automation.identity_required"},
		{"missing name", func(rule *automationmodel.AutomationRuleSchema) { rule.Name = "" }, "backend.automation.identity_required"},
		{"unknown object", func(rule *automationmodel.AutomationRuleSchema) { rule.ObjectKey = "missing" }, "backend.automation.object_not_found"},
		{"invalid phase", func(rule *automationmodel.AutomationRuleSchema) { rule.Trigger.Phase = "during" }, "backend.automation.phase_invalid"},
		{"invalid run as", func(rule *automationmodel.AutomationRuleSchema) { rule.Execution.RunAs = "system" }, "backend.automation.run_as_invalid"},
		{"invalid execution mode", func(rule *automationmodel.AutomationRuleSchema) { rule.Execution.Mode = "eventual" }, "backend.automation.execution_mode_invalid"},
		{"invalid result notification", func(rule *automationmodel.AutomationRuleSchema) { rule.Execution.ResultNotification = "always" }, "backend.automation.result_notification_invalid"},
		{"invalid operation", func(rule *automationmodel.AutomationRuleSchema) { rule.Trigger.Operation = "read" }, "backend.automation.operation_invalid"},
		{"invalid trigger filter", func(rule *automationmodel.AutomationRuleSchema) { rule.Trigger.ChangedFields = []string{"number"} }, "backend.automation.changed_fields_operation_invalid"},
		{"unknown changed field", func(rule *automationmodel.AutomationRuleSchema) {
			rule.Trigger.Operation = "update"
			rule.Trigger.ChangedFields = []string{"missing"}
		}, "backend.automation.changed_field_not_found"},
		{"invalid condition", func(rule *automationmodel.AutomationRuleSchema) { rule.Conditions.Mode = "none" }, "backend.automation.condition_mode_invalid"},
		{"unknown idempotency field", func(rule *automationmodel.AutomationRuleSchema) { rule.Execution.IdempotencyKeys = []string{"missing"} }, "backend.automation.idempotency_field_not_found"},
		{"unknown output reference", func(rule *automationmodel.AutomationRuleSchema) {
			rule.Instructions[0].Config = map[string]any{"value": "$actions.missing.value"}
		}, "backend.automation.output_reference_unknown"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rule := valid
			test.edit(&rule)
			assertAutomationErrorCode(t, validator.Validate(t.Context(), rule), test.code)
		})
	}
}

func TestAutomationDefinitionValidatorRejectsInvalidInstructions(t *testing.T) {
	validator := AutomationDefinitionValidator{Catalog: automationValidatorCatalog()}
	tests := []struct {
		name         string
		phase        string
		instructions []automationmodel.AutomationInstructionSchema
		code         string
	}{
		{"empty key", "after", []automationmodel.AutomationInstructionSchema{{Type: "emit_event"}}, "backend.automation.instruction_key_invalid"},
		{"duplicate key", "after", []automationmodel.AutomationInstructionSchema{{Key: "same", Type: "emit_event"}, {Key: "same", Type: "emit_event"}}, "backend.automation.instruction_key_invalid"},
		{"unsupported before type", "before", []automationmodel.AutomationInstructionSchema{{Key: "emit", Type: "emit_event"}}, "backend.automation.before_instruction_unsupported"},
		{"unsupported after type", "after", []automationmodel.AutomationInstructionSchema{{Key: "other", Type: "other"}}, "backend.automation.after_instruction_unsupported"},
		{"action key missing", "after", []automationmodel.AutomationInstructionSchema{{Key: "invoke", Type: "invoke_business_action"}}, "backend.automation.business_action_key_required"},
		{"action not found", "after", []automationmodel.AutomationInstructionSchema{{Key: "invoke", Type: "invoke_business_action", Config: map[string]any{"action_key": "missing"}}}, "backend.automation.business_action_not_found"},
		{"workflow key missing", "after", []automationmodel.AutomationInstructionSchema{{Key: "start", Type: "start_workflow"}}, "backend.automation.workflow_key_required"},
		{"workflow not found", "after", []automationmodel.AutomationInstructionSchema{{Key: "start", Type: "start_workflow", Config: map[string]any{"workflow_key": "missing"}}}, "backend.automation.target_workflow_not_found"},
		{"derive fields missing", "before", []automationmodel.AutomationInstructionSchema{{Key: "derive", Type: "derive_fields"}}, "backend.automation.derive_fields_required"},
		{"assert source missing", "before", []automationmodel.AutomationInstructionSchema{{Key: "guard", Type: "assert"}}, "backend.automation.assert_source_required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rule := automationValidatorRule()
			rule.Trigger.Phase = test.phase
			rule.Instructions = test.instructions
			assertAutomationErrorCode(t, validator.Validate(t.Context(), rule), test.code)
		})
	}
}

func TestAutomationDefinitionValidatorAcceptsSupportedInstructions(t *testing.T) {
	validator := AutomationDefinitionValidator{Catalog: automationValidatorCatalog()}
	tests := []struct {
		name         string
		phase        string
		instructions []automationmodel.AutomationInstructionSchema
	}{
		{"before pure instructions", "before", []automationmodel.AutomationInstructionSchema{{Key: "derive", Type: "derive_fields", Config: map[string]any{"fields": map[string]any{"status": "ready"}}}, {Key: "guard", Type: "assert", Config: map[string]any{"source": "$payload.status", "operator": "eq", "value": "ready"}}}},
		{"after instructions", "after", []automationmodel.AutomationInstructionSchema{{Key: "invoke", Type: "invoke_business_action", Config: map[string]any{"action_key": "order.normalize"}}, {Key: "start", Type: "start_workflow", Config: map[string]any{"workflow_key": "order.approve"}}, {Key: "emit", Type: "emit_event"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rule := automationValidatorRule()
			rule.Trigger.Phase = test.phase
			rule.Instructions = test.instructions
			if err := validator.Validate(t.Context(), rule); err != nil {
				t.Fatalf("expected valid rule, got %v", err)
			}
		})
	}
}

func TestAutomationExecutionPolicyAcceptsExplicitResultNotificationModes(t *testing.T) {
	for _, mode := range []string{"", "none", "failures", "all"} {
		if err := AutomationValidateExecutionPolicyShape(automationmodel.AutomationExecutionPolicy{RunAs: "initiator", ResultNotification: mode}); err != nil {
			t.Fatalf("mode %q rejected: %v", mode, err)
		}
	}
}

func automationValidatorRule() automationmodel.AutomationRuleSchema {
	return automationmodel.AutomationRuleSchema{
		Key: "order.created", Name: "Order created", ObjectKey: "order",
		Trigger:      automationmodel.AutomationTriggerSchema{Phase: "after", Operation: "create"},
		Execution:    automationmodel.AutomationExecutionPolicy{RunAs: "initiator", IdempotencyKeys: []string{"number"}},
		Instructions: []automationmodel.AutomationInstructionSchema{{Key: "emit", Type: "emit_event"}},
	}
}

func automationValidatorCatalog() AutomationDefinitionCatalog {
	return AutomationDefinitionCatalog{
		Objects: []definitionmodel.ObjectSchema{{Key: "order", Fields: []definitionmodel.FieldSchema{
			{Key: "number", Type: "text"},
			{Key: "amount", Type: "decimal"},
			{Key: "status", Type: "select", Validation: definitionmodel.FieldValidation{Options: []string{"draft", "approved"}}},
		}}},
		Actions: []definitionmodel.ActionSchema{{Key: "order.normalize", ObjectKey: "order"}},
		Workflows: []definitionmodel.WorkflowSchema{{
			Key: "order.approve", Enabled: true, TriggerContract: &definitionmodel.WorkflowTriggerContract{Type: "manual"},
		}},
	}
}

func assertAutomationErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil || apperror.CodeOf(err) != code {
		t.Fatalf("expected error %q, got %v (%q)", code, err, apperror.CodeOf(err))
	}
}
