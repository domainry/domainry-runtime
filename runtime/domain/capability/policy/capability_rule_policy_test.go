package policy

import (
	"errors"
	"testing"
)

func TestValidateCapabilityRuleVariants(t *testing.T) {
	tests := []struct {
		name   string
		kind   string
		config map[string]any
		data   map[string]any
		code   string
	}{
		{name: "inventory default fields", kind: "inventory", data: map[string]any{"quantity": 3, "stock": 2}, code: "backend.capability.inventory_insufficient"},
		{name: "inventory custom fields", kind: "inventory", config: map[string]any{"quantity_field": "qty", "stock_field": "left"}, data: map[string]any{"qty": 1, "left": 2}},
		{name: "inventory incomplete", kind: "inventory", data: map[string]any{"quantity": "bad", "stock": 0}},
		{name: "inventory missing stock", kind: "inventory", data: map[string]any{"quantity": 1, "stock": "bad"}},
		{name: "inactive promotion", kind: "promotion", data: map[string]any{"is_active": "no"}, code: "backend.capability.promotion_inactive"},
		{name: "active promotion", kind: "promotion", config: map[string]any{"active_field": "enabled"}, data: map[string]any{"enabled": true}},
		{name: "unparseable promotion status", kind: "promotion", data: map[string]any{"is_active": "unknown"}},
		{name: "approval required", kind: "approval", config: map[string]any{"status_field": "status", "required_status": "accepted"}, data: map[string]any{"status": "pending"}, code: "backend.capability.approval_required"},
		{name: "approval empty", kind: "approval", data: map[string]any{}},
		{name: "state denied", kind: "state_machine", config: map[string]any{"state_field": "phase", "allowed_from": []string{"draft"}}, data: map[string]any{"phase": "closed"}, code: "backend.capability.state_transition_denied"},
		{name: "state unrestricted", kind: "state_machine", data: map[string]any{"state": "closed"}},
		{name: "state missing", kind: "state_machine", config: map[string]any{"allowed_from": []string{"draft"}}, data: map[string]any{}},
		{name: "pricing accepted", kind: "pricing"},
		{name: "loyalty accepted", kind: "loyalty"},
		{name: "unknown fails closed", kind: "future_capability", code: "backend.capability.kind_unsupported"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := CapabilityValidateRule(test.kind, test.config, test.data)
			assertValidationCode(t, err, test.code)
		})
	}
}

func TestApplyCapabilityOperationVariants(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		config    map[string]any
		data      map[string]any
		wantKey   string
		wantValue float64
		wantText  string
		code      string
	}{
		{name: "transition missing target", operation: "transition_state", code: "backend.capability.transition_state_missing_target"},
		{name: "transition denied", operation: "transition_state", config: map[string]any{"to": "paid", "allowed_from": []string{"draft"}}, data: map[string]any{"state": "closed"}, code: "backend.capability.state_transition_denied"},
		{name: "transition custom", operation: "transition_state", config: map[string]any{"state_field": "phase", "to": "paid", "allowed_from": []any{"draft"}}, data: map[string]any{"phase": "draft"}, wantKey: "phase", wantText: "paid"},
		{name: "transition unrestricted", operation: "transition_state", config: map[string]any{"to": "paid"}, data: map[string]any{"state": "draft"}, wantKey: "state", wantText: "paid"},
		{name: "calculate missing base", operation: "calculate_price"},
		{name: "calculate discounts", operation: "calculate_price", config: map[string]any{"discount_field": "discount", "discount_rate": .1}, data: map[string]any{"base_price": 100, "discount": 5}, wantKey: "calculated_price", wantValue: 85},
		{name: "calculate custom base invalid rate", operation: "calculate_price", config: map[string]any{"base_price_field": "amount", "discount_rate": "bad"}, data: map[string]any{"amount": 10}, wantKey: "calculated_price", wantValue: 10},
		{name: "calculate clamp", operation: "calculate_price", config: map[string]any{"output_field": "total", "discount_rate": 2}, data: map[string]any{"base_price": 10}, wantKey: "total", wantValue: 0},
		{name: "promotion missing price", operation: "apply_promotion"},
		{name: "promotion discounts", operation: "apply_promotion", config: map[string]any{"price_field": "amount", "output_field": "net", "discount_rate": .2, "discount_amount": 5}, data: map[string]any{"amount": 100}, wantKey: "net", wantValue: 75},
		{name: "promotion invalid flat", operation: "apply_promotion", config: map[string]any{"discount_amount": "bad"}, data: map[string]any{"price": 10}, wantKey: "discounted_price", wantValue: 10},
		{name: "promotion clamp", operation: "apply_promotion", config: map[string]any{"discount_amount": 20}, data: map[string]any{"price": 10}, wantKey: "discounted_price", wantValue: 0},
		{name: "numeric adjustment missing fields", operation: "adjust_numeric"},
		{name: "numeric adjustment missing source field", operation: "adjust_numeric", config: map[string]any{"target_field": "counter"}},
		{name: "numeric adjustment missing source", operation: "adjust_numeric", config: map[string]any{"target_field": "counter", "source_field": "delta"}},
		{name: "numeric adjustment default multiplier", operation: "adjust_numeric", config: map[string]any{"target_field": "counter", "source_field": "delta"}, data: map[string]any{"delta": 10, "counter": 2}, wantKey: "counter", wantValue: 12},
		{name: "numeric adjustment custom multiplier", operation: "adjust_numeric", config: map[string]any{"target_field": "counter", "source_field": "base", "multiplier": .5}, data: map[string]any{"base": 20, "counter": 1}, wantKey: "counter", wantValue: 11},
		{name: "inventory missing stock", operation: "consume_inventory", data: map[string]any{"quantity": 1}},
		{name: "inventory missing quantity", operation: "consume_inventory", data: map[string]any{"stock": 1}},
		{name: "inventory insufficient", operation: "consume_inventory", data: map[string]any{"stock": 1, "quantity": 2}, code: "backend.capability.inventory_insufficient"},
		{name: "inventory custom", operation: "consume_inventory", config: map[string]any{"stock_field": "left", "quantity_field": "qty"}, data: map[string]any{"left": 5, "qty": 2}, wantKey: "left", wantValue: 3},
		{name: "condition missing field", operation: "validate_condition"},
		{name: "condition no expected", operation: "validate_condition", config: map[string]any{"field": "status"}},
		{name: "condition custom error", operation: "validate_condition", config: map[string]any{"field": "status", "expected_values": []string{"active"}, "error_code": "backend.custom.denied"}, data: map[string]any{"status": "blocked"}, code: "backend.custom.denied"},
		{name: "condition accepted", operation: "validate_condition", config: map[string]any{"field": "status", "expected_values": []any{"active"}}, data: map[string]any{"status": "active"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			patch, err := CapabilityApplyOperation(test.operation, test.config, test.data)
			assertValidationCode(t, err, test.code)
			if test.code != "" || test.wantKey == "" {
				return
			}
			if test.wantText != "" {
				if patch[test.wantKey] != test.wantText {
					t.Fatalf("patch=%#v", patch)
				}
				return
			}
			if patch[test.wantKey] != test.wantValue {
				t.Fatalf("patch=%#v want %s=%v", patch, test.wantKey, test.wantValue)
			}
		})
	}
	for _, operation := range []string{"aggregate_records", "emit_audit", "notify", "unknown"} {
		patch, err := CapabilityApplyOperation(operation, nil, nil)
		if err != nil || patch != nil {
			t.Fatalf("operation %q patch=%#v err=%v", operation, patch, err)
		}
	}
}

func assertValidationCode(t *testing.T, err error, code string) {
	t.Helper()
	if code == "" {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	var validation *CapabilityValidationError
	if !errors.As(err, &validation) || validation.Code != code {
		t.Fatalf("error=%v code=%q, want %q", err, validationCodeOf(validation), code)
	}
}

func validationCodeOf(err *CapabilityValidationError) string {
	if err == nil {
		return ""
	}
	return err.Code
}
