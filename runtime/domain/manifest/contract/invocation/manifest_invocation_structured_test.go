package invocation

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	bindingcontract "github.com/domainry/domainry-runtime/runtime/domain/manifest/contract/binding"
)

func TestActionInvocationAcceptsStructuredPayloadValuesAsJSON(t *testing.T) {
	action := definitionmodel.ActionSchema{Key: "customer.register_accounts", ObjectKey: "customer", PayloadFields: []definitionmodel.ActionPayloadField{
		{Key: "accounts", Type: "object", Required: true, Repeated: true, Fields: []definitionmodel.ActionPayloadField{{Key: "bank", Type: "text", Required: true}}},
		{Key: "assignees", Type: "user", Repeated: true},
		{Key: "address", Type: "object", Fields: []definitionmodel.ActionPayloadField{{Key: "city", Type: "text"}}},
	}}
	if issues := ValidateAction(action, "customer", map[string]any{
		"accounts": []any{map[string]any{"bank": "First"}}, "assignees": []any{"user-1"}, "address": map[string]any{"city": "Bonn"},
	}, nil); len(issues) != 0 {
		t.Fatalf("structured invocation issues=%#v", issues)
	}
	// JSON is the top of the binding lattice: static validation only checks
	// that the value exists; item-level shape is enforced at invocation time.
	if issues := ValidateAction(action, "customer", map[string]any{"accounts": "$workflow.missing"}, nil); len(issues) != 1 || issues[0].Code != "invocation.input_reference_unknown" {
		t.Fatalf("unknown reference issues=%#v", issues)
	}
	required := ValidateAction(action, "customer", map[string]any{}, nil)
	if len(required) != 1 || required[0].Code != "invocation.input_required" || required[0].Expected != string(bindingcontract.TypeJSON) {
		t.Fatalf("required issues=%#v", required)
	}
	environment := bindingcontract.NewEnvironment(bindingcontract.Fact{Reference: "$workflow.accounts", Type: bindingcontract.TypeJSON})
	if issues := ValidateAction(action, "customer", map[string]any{"accounts": "$workflow.accounts"}, environment); len(issues) != 0 {
		t.Fatalf("JSON reference issues=%#v", issues)
	}
}
