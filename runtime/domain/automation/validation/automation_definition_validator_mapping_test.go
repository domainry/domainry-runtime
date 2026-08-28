package validation

import (
	"encoding/json"
	"math"
	"testing"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestAutomationConnectorOperation(t *testing.T) {
	connector := integrationmodel.ConnectorSchema{Operations: []integrationmodel.ConnectorOperationSchema{{Key: " create "}, {Key: "delete"}}}
	if operation := AutomationConnectorOperation(connector, "create"); operation == nil || operation.Key != " create " {
		t.Fatalf("unexpected operation: %#v", operation)
	}
	if operation := AutomationConnectorOperation(connector, "missing"); operation != nil {
		t.Fatalf("expected missing operation, got %#v", operation)
	}
}

func TestAutomationValidateOperationInputContracts(t *testing.T) {
	operation := integrationmodel.ConnectorOperationSchema{Key: "send", Input: []definitionmodel.FieldSchema{
		{Key: "amount", Type: "decimal", Required: true},
		{Key: "recipient", Type: "email"},
	}}
	object := automationValidatorCatalog().Objects[0]
	outputs := map[string]map[string]string{"lookup": {"email": "email"}}
	tests := []struct {
		name  string
		input map[string]any
		code  string
	}{
		{"missing required", nil, "backend.automation.operation_input_required"},
		{"empty required", map[string]any{"amount": ""}, "backend.automation.operation_input_required"},
		{"unknown input", map[string]any{"amount": 1, "extra": true}, "backend.automation.operation_input_unknown"},
		{"unknown reference", map[string]any{"amount": "$input.missing"}, "backend.automation.input_reference_unknown"},
		{"type mismatch", map[string]any{"amount": true}, "backend.automation.operation_input_type_mismatch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertAutomationErrorCode(t, AutomationValidateOperationInput(operation, test.input, object, outputs), test.code)
		})
	}
	for _, input := range []map[string]any{
		{"amount": 42},
		{"amount": "$input.amount", "recipient": "$actions.lookup.email"},
	} {
		if err := AutomationValidateOperationInput(operation, input, object, outputs); err != nil {
			t.Fatalf("expected valid input %#v: %v", input, err)
		}
	}
}

func TestAutomationMappingValueType(t *testing.T) {
	object := automationValidatorCatalog().Objects[0]
	outputs := map[string]map[string]string{"lookup": {"count": "integer"}}
	tests := []struct {
		name  string
		value any
		type_ string
		known bool
	}{
		{"input", "$input.amount", "decimal", true},
		{"candidate", "$candidate.number", "text", true},
		{"action", "$actions.lookup.count", "integer", true},
		{"missing action field", "$actions.lookup.missing", "", false},
		{"event id", "$event.id", "text", true},
		{"timestamp", "$event.timestamp", "datetime", true},
		{"unknown reference", "$event.other", "", false},
		{"boolean", true, "boolean", true},
		{"integer", int16(4), "integer", true},
		{"float32 integer", float32(4), "integer", true},
		{"float32 decimal", float32(4.5), "decimal", true},
		{"float64 integer", float64(4), "integer", true},
		{"float64 decimal", float64(4.5), "decimal", true},
		{"nan", math.NaN(), "decimal", true},
		{"map", map[string]any{"ok": true}, "json", true},
		{"slice", []any{1}, "json", true},
		{"string", "plain", "text", true},
		{"nil", nil, "", false},
		{"other", struct{}{}, "json", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			typeName, known := AutomationMappingValueType(test.value, object, outputs)
			if typeName != test.type_ || known != test.known {
				t.Fatalf("got (%q,%v), want (%q,%v)", typeName, known, test.type_, test.known)
			}
		})
	}
}

func TestAutomationProtocolTypesCompatible(t *testing.T) {
	tests := []struct {
		source string
		target string
		want   bool
	}{
		{"boolean", "json", true},
		{" integer ", "INTEGER", true},
		{"integer", "decimal", true},
		{"number", "decimal", true},
		{"integer", "number", true},
		{"decimal", "number", true},
		{"currency", "number", true},
		{"email", "text", true},
		{"text", "url", true},
		{"boolean", "text", false},
		{"datetime", "number", false},
	}
	for _, test := range tests {
		if got := AutomationProtocolTypesCompatible(test.source, test.target); got != test.want {
			t.Errorf("compatible(%q,%q)=%v, want %v", test.source, test.target, got, test.want)
		}
	}
}

func TestAutomationValidateInstructionReferences(t *testing.T) {
	outputs := map[string]map[string]string{"lookup": {"email": "email"}}
	valid := automationmodel.AutomationInstructionSchema{Key: "send", Input: map[string]any{
		"plain": "$input.email",
		"map":   map[string]any{"email": "$actions.lookup.email"},
		"list":  []any{"$actions.lookup.email", 1},
	}, Config: map[string]any{"recipient": "$actions.lookup.email"}}
	if err := AutomationValidateInstructionReferences(valid, outputs); err != nil {
		t.Fatal(err)
	}
	for _, action := range []automationmodel.AutomationInstructionSchema{
		{Key: "send", Input: map[string]any{"x": "$actions.missing.email"}},
		{Key: "send", Config: map[string]any{"x": "$actions.lookup.missing"}},
		{Key: "send", Config: map[string]any{"x": "$actions.lookup"}},
		{Key: "send", Config: map[string]any{"x": []any{"plain", "$actions.missing.email"}}},
	} {
		assertAutomationErrorCode(t, AutomationValidateInstructionReferences(action, outputs), "backend.automation.output_reference_unknown")
	}
}

func TestAutomationValidatorSmallHelpers(t *testing.T) {
	if !hasAction([]definitionmodel.ActionSchema{{Key: "one"}, {Key: "two"}}, "two") || hasAction(nil, "two") {
		t.Fatal("unexpected action lookup")
	}
	if !hasWorkflow([]definitionmodel.WorkflowSchema{{Key: "one"}, {Key: "two"}}, "two") || hasWorkflow(nil, "two") {
		t.Fatal("unexpected workflow lookup")
	}
	for _, test := range []struct {
		value any
		want  int
	}{
		{7, 7}, {int64(8), 8}, {float64(9.8), 9}, {json.Number("10"), 10}, {json.Number("bad"), 3}, {"bad", 3},
	} {
		if got := intFromAny(test.value, 3); got != test.want {
			t.Errorf("intFromAny(%#v)=%d, want %d", test.value, got, test.want)
		}
	}
}
