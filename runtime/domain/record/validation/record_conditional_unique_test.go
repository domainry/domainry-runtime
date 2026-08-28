package validation

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestRecordConditionalUniquePolicyIsExplicitAndStateScoped(t *testing.T) {
	object := definitionmodel.ObjectSchema{
		Key: "class_booking",
		Fields: []definitionmodel.FieldSchema{
			{Key: "class_id"}, {Key: "member_id"}, {Key: "status"},
		},
		Validations: []definitionmodel.ValidationSchema{{
			Key: "one_active_booking", Type: ConditionalUniqueValidationType, Fields: []string{"class_id", "member_id"},
			Config: map[string]any{"condition_field": "status", "condition_values": []any{"booked", "waitlisted"}},
		}},
	}
	policies, err := RecordConditionalUniquePolicies(object)
	if err != nil || len(policies) != 1 {
		t.Fatalf("policies=%#v err=%v", policies, err)
	}
	policy := policies[0]
	if !RecordConditionalUniqueApplies(policy, map[string]any{"status": "booked"}) ||
		!RecordConditionalUniqueApplies(policy, map[string]any{"status": "waitlisted"}) ||
		RecordConditionalUniqueApplies(policy, map[string]any{"status": "cancelled"}) {
		t.Fatalf("conditional state set is not exact: %#v", policy)
	}
}

func TestRecordConditionalUniquePolicyValidationEdges(t *testing.T) {
	base := definitionmodel.ObjectSchema{
		Key: "booking",
		Fields: []definitionmodel.FieldSchema{
			{Key: "member_id"},
			{Key: "status"},
			{Key: "disabled_unique", DisabledAt: "now"},
			{Key: "disabled_condition", DisabledAt: "now"},
		},
	}
	rule := definitionmodel.ValidationSchema{
		Key:    "active_member",
		Type:   ConditionalUniqueValidationType,
		Fields: []string{"member_id"},
		Config: map[string]any{"condition_field": "status", "condition_values": []any{"active"}},
	}
	testCases := map[string]definitionmodel.ValidationSchema{
		"blank unique field": {
			Key: "blank", Type: ConditionalUniqueValidationType, Fields: []string{" "},
			Config: rule.Config,
		},
		"missing unique field": {
			Key: "missing", Type: ConditionalUniqueValidationType, Fields: []string{"missing"},
			Config: rule.Config,
		},
		"disabled unique field": {
			Key: "disabled", Type: ConditionalUniqueValidationType, Fields: []string{"disabled_unique"},
			Config: rule.Config,
		},
		"duplicate unique field": {
			Key: "duplicate", Type: ConditionalUniqueValidationType, Fields: []string{"member_id", "member_id"},
			Config: rule.Config,
		},
		"no unique fields": {
			Key: "none", Type: ConditionalUniqueValidationType,
			Config: rule.Config,
		},
		"blank condition field": {
			Key: "blank-condition", Type: ConditionalUniqueValidationType, Fields: rule.Fields,
			Config: map[string]any{"condition_values": []any{"active"}},
		},
		"missing condition field": {
			Key: "missing-condition", Type: ConditionalUniqueValidationType, Fields: rule.Fields,
			Config: map[string]any{"condition_field": "missing", "condition_values": []any{"active"}},
		},
		"disabled condition field": {
			Key: "disabled-condition", Type: ConditionalUniqueValidationType, Fields: rule.Fields,
			Config: map[string]any{"condition_field": "disabled_condition", "condition_values": []any{"active"}},
		},
		"blank condition value": {
			Key: "blank-value", Type: ConditionalUniqueValidationType, Fields: rule.Fields,
			Config: map[string]any{"condition_field": "status", "condition_values": []any{" "}},
		},
		"nil condition value": {
			Key: "nil-value", Type: ConditionalUniqueValidationType, Fields: rule.Fields,
			Config: map[string]any{"condition_field": "status", "condition_values": []any{nil}},
		},
		"duplicate condition value": {
			Key: "duplicate-value", Type: ConditionalUniqueValidationType, Fields: rule.Fields,
			Config: map[string]any{"condition_field": "status", "condition_values": []any{"active", "active"}},
		},
		"no condition values": {
			Key: "no-values", Type: ConditionalUniqueValidationType, Fields: rule.Fields,
			Config: map[string]any{"condition_field": "status", "condition_values": "active"},
		},
	}
	for name, invalidRule := range testCases {
		t.Run(name, func(t *testing.T) {
			object := base
			object.Validations = []definitionmodel.ValidationSchema{invalidRule}
			if _, err := RecordConditionalUniquePolicies(object); err == nil {
				t.Fatal("expected conditional unique validation error")
			}
		})
	}

	object := base
	object.Validations = []definitionmodel.ValidationSchema{
		{Key: "ignored", Type: "required"},
		{
			Key: "active_member", Type: ConditionalUniqueValidationType, Fields: []string{" member_id "},
			Config: map[string]any{"condition_field": " status ", "condition_values": []string{" active ", "pending"}},
		},
	}
	policies, err := RecordConditionalUniquePolicies(object)
	if err != nil || len(policies) != 1 || len(policies[0].ConditionValues) != 2 || policies[0].ConditionValues[0] != "active" {
		t.Fatalf("policies=%+v error=%v", policies, err)
	}
}

func TestRecordConditionalUniqueAppliesEmptyConditionEdges(t *testing.T) {
	policy := RecordConditionalUniquePolicy{ConditionField: "status", ConditionValues: []string{"active"}}
	if RecordConditionalUniqueApplies(policy, map[string]any{}) {
		t.Fatal("missing condition unexpectedly applies")
	}
	if RecordConditionalUniqueApplies(policy, map[string]any{"status": " "}) {
		t.Fatal("empty condition unexpectedly applies")
	}
}
