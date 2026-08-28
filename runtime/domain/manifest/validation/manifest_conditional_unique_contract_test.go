package validation

import (
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestManifestConditionalUniqueRequiresExplicitFieldsConditionAndValues(t *testing.T) {
	fields := []definitionmodel.FieldSchema{
		{Key: "class_id", Type: "relation"},
		{Key: "member_id", Type: "relation"},
		{Key: "status", Type: "select"},
		{Key: "disabled", Type: "text", DisabledAt: "2026-07-23T00:00:00Z"},
	}
	valid := definitionmodel.ValidationSchema{
		Key: "active_booking", Type: "conditional_unique", Fields: []string{"class_id", "member_id"},
		Config: map[string]any{"condition_field": "status", "condition_values": []any{"booked", "waitlisted"}},
	}
	state := newValidationState(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{
		Key: "class_booking", Fields: fields, Validations: []definitionmodel.ValidationSchema{valid},
	}}}, nil)
	state.validateObjects()
	if len(state.errs) != 0 {
		t.Fatalf("valid conditional unique diagnostics=%v", state.errs)
	}

	invalid := []definitionmodel.ValidationSchema{
		{Key: "missing_fields", Type: "conditional_unique", Config: valid.Config},
		{Key: "duplicate_field", Type: "conditional_unique", Fields: []string{"class_id", "class_id"}, Config: valid.Config},
		{Key: "disabled_field", Type: "conditional_unique", Fields: []string{"class_id", "disabled"}, Config: valid.Config},
		{Key: "missing_condition", Type: "conditional_unique", Fields: valid.Fields, Config: map[string]any{"condition_values": []any{"booked"}}},
		{Key: "disabled_condition", Type: "conditional_unique", Fields: valid.Fields, Config: map[string]any{"condition_field": "disabled", "condition_values": []any{"booked"}}},
		{Key: "missing_values", Type: "conditional_unique", Fields: valid.Fields, Config: map[string]any{"condition_field": "status"}},
		{Key: "duplicate_values", Type: "conditional_unique", Fields: valid.Fields, Config: map[string]any{"condition_field": "status", "condition_values": []any{"booked", "booked"}}},
	}
	state = newValidationState(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{
		Key: "class_booking", Fields: fields, Validations: invalid,
	}}}, nil)
	state.validateObjects()
	diagnostics := state.errs.Error()
	for _, code := range []string{
		"backend.definition.conditional_unique_fields_required",
		"backend.definition.conditional_unique_field_duplicate",
		"backend.definition.conditional_unique_field_disabled",
		"backend.definition.conditional_unique_condition_field_required",
		"backend.definition.conditional_unique_condition_field_disabled",
		"backend.definition.conditional_unique_condition_values_required",
		"backend.definition.conditional_unique_condition_value_duplicate",
	} {
		if !strings.Contains(diagnostics, code) {
			t.Fatalf("missing %q in diagnostics: %s", code, diagnostics)
		}
	}
}
