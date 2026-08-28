package policy

import (
	"reflect"
	"testing"
)

func TestCapabilityValidationRuleEmptyAndAllowedValues(t *testing.T) {
	for _, test := range []struct {
		kind   string
		config map[string]any
		data   map[string]any
	}{
		{kind: "approval", data: map[string]any{"approval_status": nil}},
		{kind: "approval", data: map[string]any{"approval_status": ""}},
		{kind: "approval", data: map[string]any{"approval_status": "approved"}},
		{kind: "state_machine", config: map[string]any{"allowed_from": []string{"draft"}}, data: map[string]any{"state": nil}},
		{kind: "state_machine", config: map[string]any{"allowed_from": []string{"draft"}}, data: map[string]any{"state": ""}},
		{kind: "state_machine", config: map[string]any{"allowed_from": []string{"draft"}}, data: map[string]any{"state": "draft"}},
	} {
		if err := CapabilityValidateRule(test.kind, test.config, test.data); err != nil {
			t.Fatalf("%s %#v rejected: %v", test.kind, test.data, err)
		}
	}
}

func TestCapabilityConditionDefaultErrorAndValueHelpers(t *testing.T) {
	_, err := CapabilityApplyOperation("validate_condition", map[string]any{"field": "status", "expected_values": "active"}, map[string]any{"status": "blocked"})
	assertValidationCode(t, err, "backend.capability.condition_not_met")

	for _, test := range []struct {
		value any
		want  float64
		ok    bool
	}{{float64(1.5), 1.5, true}, {float32(2.5), 2.5, true}, {int(3), 3, true}, {int64(4), 4, true}, {" 5.5 ", 5.5, true}, {"bad", 0, false}, {true, 0, false}} {
		got, ok := numericAny(test.value)
		if got != test.want || ok != test.ok {
			t.Fatalf("numeric %#v = %v/%v", test.value, got, ok)
		}
	}
	for _, test := range []struct {
		value any
		want  bool
		ok    bool
	}{{true, true, true}, {false, false, true}, {"true", true, true}, {"1", true, true}, {"yes", true, true}, {"false", false, true}, {"0", false, true}, {"no", false, true}, {"bad", false, false}, {1, false, false}} {
		got, ok := boolAny(test.value)
		if got != test.want || ok != test.ok {
			t.Fatalf("bool %#v = %v/%v", test.value, got, ok)
		}
	}
	for _, test := range []struct {
		value any
		want  []string
	}{{[]string{"a"}, []string{"a"}}, {[]any{" a ", "", 2}, []string{"a", "2"}}, {" value ", []string{"value"}}, {"", nil}, {true, nil}} {
		if got := stringListAny(test.value); !reflect.DeepEqual(got, test.want) {
			t.Fatalf("string list %#v = %#v, want %#v", test.value, got, test.want)
		}
	}
}

func TestCapabilityValidationErrorContractAndIsolation(t *testing.T) {
	err := &CapabilityValidationError{Code: " code ", Params: map[string]string{"field": "status"}}
	if err.Error() != " code " || err.ErrorCode() != "code" {
		t.Fatalf("error contract = %q/%q", err.Error(), err.ErrorCode())
	}
	params := err.ErrorParams()
	params["field"] = "changed"
	if err.Params["field"] != "status" {
		t.Fatal("error params result aliases source")
	}
	if (&CapabilityValidationError{}).ErrorParams() != nil {
		t.Fatal("empty params must return nil")
	}
	constructed := validationError(" code ", "", "ignored", "field", "status", "orphan").(*CapabilityValidationError)
	if constructed.Code != "code" || !reflect.DeepEqual(constructed.Params, map[string]string{"field": "status"}) {
		t.Fatalf("constructed error = %#v", constructed)
	}
}
