package validation

import (
	"context"
	"errors"
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	"math"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestAutomationValidateRuleForAuthoringCallbackAndDistinctEdges(t *testing.T) {
	validRule := automationmodel.AutomationRuleSchema{Instructions: []automationmodel.AutomationInstructionSchema{{Key: "emit", Type: "emit_event"}, {Key: "action", Type: "invoke_business_action"}, {Key: "workflow", Type: "start_workflow"}}}
	result, err := AutomationValidateRuleForAuthoring(context.Background(), validRule, nil)
	if err != nil || !result.Valid || len(result.Errors) != 0 {
		t.Fatalf("valid rule = %#v, err=%v", result, err)
	}
	result, err = AutomationValidateRuleForAuthoring(context.Background(), validRule, func(context.Context, automationmodel.AutomationRuleSchema) error { return nil })
	if err != nil || !result.Valid {
		t.Fatalf("nil callback result = %#v, err=%v", result, err)
	}
	badRequest := definitionError(apperror.KindBadRequest, "backend.automation.phase_invalid", nil)
	result, err = AutomationValidateRuleForAuthoring(context.Background(), validRule, func(context.Context, automationmodel.AutomationRuleSchema) error { return badRequest })
	if err != nil || result.Valid || len(result.Errors) != 1 {
		t.Fatalf("validation error result = %#v, err=%v", result, err)
	}
	wantError := errors.New("catalog unavailable")
	result, err = AutomationValidateRuleForAuthoring(context.Background(), validRule, func(context.Context, automationmodel.AutomationRuleSchema) error { return wantError })
	if !errors.Is(err, wantError) || result.Valid {
		t.Fatalf("system error result = %#v, err=%v", result, err)
	}
	forbidden := &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.automation.forbidden"}
	result, err = AutomationValidateRuleForAuthoring(context.Background(), validRule, func(context.Context, automationmodel.AutomationRuleSchema) error { return forbidden })
	if !errors.Is(err, forbidden) || result.Valid {
		t.Fatalf("non-validation app error result = %#v, err=%v", result, err)
	}
	issue := AutomationValidationIssue{ErrorCode: "code", FieldPath: "field", InstructionKey: "step"}
	issues := AutomationAppendDistinctValidationIssue(nil, issue)
	if got := AutomationAppendDistinctValidationIssue(issues, issue); len(got) != 1 {
		t.Fatalf("duplicate issues = %#v", got)
	}
	other := issue
	other.FieldPath = "other"
	if got := AutomationAppendDistinctValidationIssue(issues, other); len(got) != 2 {
		t.Fatalf("distinct issues = %#v", got)
	}
	other = issue
	other.InstructionKey = "other-step"
	if got := AutomationAppendDistinctValidationIssue(issues, other); len(got) != 2 {
		t.Fatalf("distinct instruction issues = %#v", got)
	}
	other = issue
	other.ErrorCode = "other-code"
	if got := AutomationAppendDistinctValidationIssue(issues, other); len(got) != 2 {
		t.Fatalf("distinct code issues = %#v", got)
	}
}

func TestAutomationValidateActiveInstructionsAndErrorDetails(t *testing.T) {
	valid := automationmodel.AutomationRuleSchema{Instructions: []automationmodel.AutomationInstructionSchema{{Key: "emit", Type: "emit_event"}}}
	if err := AutomationValidateActiveInstructions(valid); err != nil {
		t.Fatal(err)
	}
	invalid := automationmodel.AutomationRuleSchema{Instructions: []automationmodel.AutomationInstructionSchema{{Key: "bad", Type: "unknown"}}}
	if err := AutomationValidateActiveInstructions(invalid); apperror.CodeOf(err) != "backend.automation.instruction_type_invalid" {
		t.Fatalf("active instruction error = %v", err)
	}
	if code, params := automationErrorDetails(errors.New("plain")); code != "backend.internal" || params != nil {
		t.Fatalf("plain error details = %q/%#v", code, params)
	}
}

func TestAutomationValidationIssueLocationsCapabilitiesAndActionLookup(t *testing.T) {
	rule := automationmodel.AutomationRuleSchema{Instructions: []automationmodel.AutomationInstructionSchema{
		{Key: "first", Type: "invoke_business_action", Operation: "send", ConnectionKey: "primary", ConnectorKey: "mail"},
		{Key: "second", Type: "start_workflow", Operation: "start"},
	}}
	tests := []struct {
		code   string
		params []string
		path   string
	}{
		{"backend.automation.identity_invalid", nil, "key"},
		{"backend.automation.object_not_found", nil, "object_key"},
		{"backend.automation.phase_invalid", nil, "trigger.phase"},
		{"backend.automation.operation_invalid", nil, "trigger.operation"},
		{"backend.automation.changed_field_invalid", nil, "trigger.changed_fields"},
		{"backend.automation.transition_state_invalid", []string{"field", "to_state"}, "trigger.to_state"},
		{"backend.automation.transition_filter_invalid", nil, "trigger.operation"},
		{"backend.automation.run_as_invalid", nil, "execution.run_as"},
		{"backend.automation.instruction_type_invalid", []string{"instruction", "first"}, "instructions[0].type"},
		{"backend.automation.action_key_invalid", []string{"action", "first"}, "instructions[0].key"},
		{"backend.automation.before_action_unsupported", []string{"instruction", "first"}, "instructions[0].type"},
		{"backend.automation.after_action_unsupported", []string{"instruction", "first"}, "instructions[0].type"},
		{"backend.automation.target_action_invalid", []string{"instruction", "first"}, "instructions[0].config.action_key"},
		{"backend.automation.action_key_required", []string{"instruction", "first"}, "instructions[0].config.action_key"},
		{"backend.automation.workflow_invalid", []string{"instruction", "second"}, "instructions[1].config.workflow_key"},
		{"backend.automation.connection_invalid", []string{"connection", "primary"}, "instructions[0].connection_key"},
		{"backend.automation.connector_not_found", []string{"connector", "mail"}, "instructions[0].connector_key"},
		{"backend.automation.operation_input_invalid", []string{"operation", "send", "field", "recipient"}, "instructions[0].input.recipient"},
		{"backend.automation.operation_input_invalid", []string{"operation", "send"}, "instructions[0].input"},
		{"backend.automation.operation_invalid_instruction", []string{"operation", "send"}, "instructions[0].operation"},
		{"backend.automation.output_reference_invalid", []string{"instruction", "first"}, "instructions[0].input"},
		{"backend.automation.input_reference_unknown", nil, "rule"},
		{"backend.automation.before_must_block", []string{"instruction", "first"}, "instructions[0].on_error"},
		{"backend.automation.action_other", []string{"instruction", "first"}, "instructions[0]"},
	}
	for _, test := range tests {
		issue := AutomationValidationIssueFromError(rule, definitionError(apperror.KindBadRequest, test.code, nil, test.params...))
		if issue.FieldPath != test.path || issue.ErrorCode != test.code || issue.ContractVersion == "" {
			t.Fatalf("issue %s = %#v", test.code, issue)
		}
	}
	for code, want := range map[string]string{"trigger": "automation.trigger", "condition": "automation.condition_group", "execution": "automation.execution_policy", "other": "automation.rule"} {
		if got := validationCapabilityKey(code); got != want {
			t.Fatalf("capability %q = %q", code, got)
		}
	}
	if validationActionIndex(rule, map[string]string{}) != -1 ||
		validationActionIndex(rule, map[string]string{"instruction": "missing"}) != -1 ||
		validationActionIndex(rule, map[string]string{"action": "missing"}) != -1 ||
		validationActionIndex(rule, map[string]string{"operation": "missing"}) != -1 ||
		validationActionIndex(rule, map[string]string{"connection": "missing"}) != -1 ||
		validationActionIndex(rule, map[string]string{"connector": "missing"}) != -1 {
		t.Fatal("missing action index resolved")
	}
}

func TestAutomationDefinitionValidatorConditionEdges(t *testing.T) {
	validator := AutomationDefinitionValidator{Catalog: automationValidatorCatalog()}
	rule := automationValidatorRule()
	rule.Execution.RunAs = ""
	if err := validator.Validate(t.Context(), rule); err != nil {
		t.Fatalf("empty run-as should use runtime default: %v", err)
	}
	for _, instruction := range []automationmodel.AutomationInstructionSchema{
		{Key: "invoke", Type: "invoke_business_action", Config: map[string]any{"action_key": ""}},
		{Key: "start", Type: "start_workflow", Config: map[string]any{"workflow_key": ""}},
	} {
		rule := automationValidatorRule()
		rule.Instructions = []automationmodel.AutomationInstructionSchema{instruction}
		if err := validator.Validate(t.Context(), rule); err == nil {
			t.Fatalf("explicit empty target should fail: %#v", instruction)
		}
	}

	validNested := automationmodel.AutomationConditionGroup{Groups: []automationmodel.AutomationConditionGroup{{Mode: "all"}}}
	if err := AutomationValidateConditionGroup(validNested, "conditions", 0); err != nil {
		t.Fatalf("valid nested group: %v", err)
	}
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{
		{Key: "status", Type: "select"},
		{Key: "state", Type: "text"},
		{Key: "stage", Type: "text"},
		{Key: "fallback", Type: "select"},
	}}
	if err := AutomationValidateTriggerFilters(automationmodel.AutomationTriggerSchema{Operation: "create", ToState: "draft"}, object); err == nil {
		t.Fatal("to-state filter on create should fail")
	}
	if err := AutomationValidateTriggerFilters(automationmodel.AutomationTriggerSchema{Operation: "transition"}, object); err != nil {
		t.Fatalf("transition without configured options should remain valid: %v", err)
	}
}

func TestAutomationMappingAndOperationInputConditionEdges(t *testing.T) {
	operation := connectormodel.ConnectorOperationSchema{Key: "send", Input: []definitionmodel.FieldSchema{{Key: "value", Type: "text"}}}
	object := automationValidatorCatalog().Objects[0]
	for _, input := range []map[string]any{{"value": "plain"}, {"value": nil}} {
		if err := AutomationValidateOperationInput(operation, input, object, nil); err != nil {
			t.Fatalf("input %#v: %v", input, err)
		}
	}
	tests := []any{
		"$other.value.more",
		"$actions.missing.value",
		float32(math.NaN()),
		float32(math.Inf(1)),
		math.Inf(1),
	}
	for _, value := range tests {
		AutomationMappingValueType(value, object, map[string]map[string]string{})
	}
	err := definitionError(apperror.KindBadRequest, "backend.test", nil, "", "ignored")
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Params != nil {
		t.Fatalf("empty parameter key should be ignored: %#v", err)
	}
}
