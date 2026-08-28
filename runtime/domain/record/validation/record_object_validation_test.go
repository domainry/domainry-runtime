package validation

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestObjectValidationRuleVariants(t *testing.T) {
	fields := []definitionmodel.FieldSchema{
		{Key: "a", Type: "number"}, {Key: "b", Type: "number"}, {Key: "status", Type: "text"}, {Key: "required", Type: "text"},
		{Key: "flags", Type: "text", Validation: definitionmodel.FieldValidation{Options: []string{"one", "two"}}},
		{Key: "start", Type: "date"}, {Key: "end", Type: "date"}, {Key: "segments", Type: "text"}, {Key: "dates", Type: "text"},
	}
	tests := []struct {
		name       string
		validation definitionmodel.ValidationSchema
		data       map[string]any
		prev       map[string]any
		code       string
	}{
		{name: "required fields", validation: definitionmodel.ValidationSchema{Key: "r", Type: "required_fields", Fields: []string{"required"}}, code: "backend.validation.required"},
		{name: "required warning skipped", validation: definitionmodel.ValidationSchema{Key: "r", Type: "required_fields", Severity: "warning", Fields: []string{"required"}}},
		{name: "condition skipped", validation: definitionmodel.ValidationSchema{Key: "r", Type: "required_fields", Fields: []string{"required"}, Config: map[string]any{"when_field": "status", "value": "active"}}, data: map[string]any{"status": "closed"}},
		{name: "conditional config", validation: definitionmodel.ValidationSchema{Key: "c", Type: "conditional_required", Config: map[string]any{"when_field": "status", "when_in": []string{"active"}, "required_fields": []string{"required"}}}, data: map[string]any{"status": "active"}, code: "backend.validation.required"},
		{name: "conditional value config", validation: definitionmodel.ValidationSchema{Key: "c", Type: "conditional_required", Fields: []string{"required"}, Config: map[string]any{"when_field": "status", "value": "active"}}, data: map[string]any{"status": "closed"}},
		{name: "conditional incomplete legacy", validation: definitionmodel.ValidationSchema{Key: "c", Type: "conditional_required", Fields: []string{"status"}}},
		{name: "conditional legacy", validation: definitionmodel.ValidationSchema{Key: "c", Type: "conditional_required", Fields: []string{"status", "required"}}, data: map[string]any{"status": "delivery"}, code: "backend.validation.required"},
		{name: "required when value", validation: definitionmodel.ValidationSchema{Key: "r", Type: "required_when", FieldKey: "status", Fields: []string{"required"}, Config: map[string]any{"value": "active"}}, data: map[string]any{"status": "active"}, code: "backend.validation.required"},
		{name: "required when mismatch", validation: definitionmodel.ValidationSchema{Key: "r", Type: "required_when", FieldKey: "status", Fields: []string{"required"}, Config: map[string]any{"value": "active"}}, data: map[string]any{"status": "closed"}},
		{name: "cross field", validation: definitionmodel.ValidationSchema{Key: "x", Type: "cross_field", Fields: []string{"a", "b"}}, data: map[string]any{"a": 1, "b": 2}, code: "backend.validation.invalid"},
		{name: "cross field incomplete", validation: definitionmodel.ValidationSchema{Key: "x", Type: "cross_field", Fields: []string{"a"}}},
		{name: "not equal", validation: definitionmodel.ValidationSchema{Key: "n", Type: "not_equal", Fields: []string{"status", "required"}}, data: map[string]any{"status": "same", "required": "same"}, code: "backend.validation.invalid"},
		{name: "not equal empty", validation: definitionmodel.ValidationSchema{Key: "n", Type: "not_equal", Fields: []string{"status", "required"}}},
		{name: "numeric min", validation: definitionmodel.ValidationSchema{Key: "n", Type: "numeric_min", FieldKey: "a", Config: map[string]any{"min": 2}}, data: map[string]any{"a": 1}, code: "backend.validation.invalid"},
		{name: "numeric min missing field", validation: definitionmodel.ValidationSchema{Key: "n", Type: "numeric_min"}},
		{name: "range exclusive min", validation: definitionmodel.ValidationSchema{Key: "r", Type: "range", FieldKey: "a", Config: map[string]any{"exclusive_min": 1}}, data: map[string]any{"a": 1}, code: "backend.validation.invalid"},
		{name: "range min", validation: definitionmodel.ValidationSchema{Key: "r", Type: "range", FieldKey: "a", Config: map[string]any{"min": 2}}, data: map[string]any{"a": 1}, code: "backend.validation.invalid"},
		{name: "range min field", validation: definitionmodel.ValidationSchema{Key: "r", Type: "range", FieldKey: "a", Config: map[string]any{"min_field": "b"}}, data: map[string]any{"a": 1, "b": 2}, code: "backend.validation.invalid"},
		{name: "range max", validation: definitionmodel.ValidationSchema{Key: "r", Type: "range", FieldKey: "a", Config: map[string]any{"max": 1}}, data: map[string]any{"a": 2}, code: "backend.validation.invalid"},
		{name: "range exclusive max field", validation: definitionmodel.ValidationSchema{Key: "r", Type: "range", FieldKey: "a", Config: map[string]any{"exclusive_max_field": "b"}}, data: map[string]any{"a": 2, "b": 2}, code: "backend.validation.invalid"},
		{name: "range max field", validation: definitionmodel.ValidationSchema{Key: "r", Type: "range", FieldKey: "a", Config: map[string]any{"max_field": "b"}}, data: map[string]any{"a": 3, "b": 2}, code: "backend.validation.invalid"},
		{name: "date min field", validation: definitionmodel.ValidationSchema{Key: "r", Type: "range", FieldKey: "end", Config: map[string]any{"min_field": "start"}}, data: map[string]any{"start": "2026-07-18", "end": "2026-07-17"}, code: "backend.validation.invalid"},
		{name: "date max field", validation: definitionmodel.ValidationSchema{Key: "r", Type: "range", FieldKey: "start", Config: map[string]any{"max_field": "end"}}, data: map[string]any{"start": "2026-07-19", "end": "2026-07-18"}, code: "backend.validation.invalid"},
		{name: "date exclusive max field", validation: definitionmodel.ValidationSchema{Key: "r", Type: "range", FieldKey: "start", Config: map[string]any{"exclusive_max_field": "end"}}, data: map[string]any{"start": "2026-07-18", "end": "2026-07-18"}, code: "backend.validation.invalid"},
		{name: "range unsupported value", validation: definitionmodel.ValidationSchema{Key: "r", Type: "range", FieldKey: "a"}, data: map[string]any{"a": "not-a-number-or-date"}},
		{name: "truthy", validation: definitionmodel.ValidationSchema{Key: "b", Type: "boolean_true", FieldKey: "status"}, data: map[string]any{"status": false}, code: "backend.validation.invalid"},
		{name: "truthy missing field", validation: definitionmodel.ValidationSchema{Key: "b", Type: "boolean_true"}},
		{name: "enum min", validation: definitionmodel.ValidationSchema{Key: "e", Type: "enum_array", FieldKey: "flags", Config: map[string]any{"min_items": 2}}, data: map[string]any{"flags": []string{"one"}}, code: "backend.validation.invalid"},
		{name: "enum option", validation: definitionmodel.ValidationSchema{Key: "e", Type: "enum_array", FieldKey: "flags"}, data: map[string]any{"flags": []string{"bad"}}, code: "backend.validation.invalid"},
		{name: "enum unknown field", validation: definitionmodel.ValidationSchema{Key: "e", Type: "enum_array", FieldKey: "unknown"}},
		{name: "enum invalid json", validation: definitionmodel.ValidationSchema{Key: "e", Type: "enum_array", FieldKey: "flags"}, data: map[string]any{"flags": 1}, code: "backend.validation.json_array"},
		{name: "business hours", validation: definitionmodel.ValidationSchema{Key: "h", Type: "business_hours_segments", FieldKey: "segments"}, data: map[string]any{"segments": []any{map[string]any{}}}, code: "backend.validation.business_hours_required"},
		{name: "business hours field fallback", validation: definitionmodel.ValidationSchema{Key: "h", Type: "business_hours_segments", Fields: []string{"segments"}}, data: map[string]any{"segments": []any{}}},
		{name: "special dates custom message", validation: definitionmodel.ValidationSchema{Key: "d", Type: "special_dates_schedule", FieldKey: "dates", Message: "backend.custom.invalid_dates"}, data: map[string]any{"dates": []any{map[string]any{}}}, code: "backend.custom.invalid_dates"},
		{name: "special dates field fallback", validation: definitionmodel.ValidationSchema{Key: "d", Type: "special_dates_schedule", Fields: []string{"dates"}}, data: map[string]any{"dates": []any{}}},
		{name: "state transition", validation: definitionmodel.ValidationSchema{Key: "s", Type: "state_transition", FieldKey: "status", Config: map[string]any{"allowed_from": []string{"draft"}}}, data: map[string]any{"status": "closed"}, code: "backend.validation.invalid"},
		{name: "state transition no states", validation: definitionmodel.ValidationSchema{Key: "s", Type: "state_transition", FieldKey: "status"}},
		{name: "relation exists", validation: definitionmodel.ValidationSchema{Key: "r", Type: "relation_exists", FieldKey: "required"}, code: "backend.validation.required"},
		{name: "relation missing field config", validation: definitionmodel.ValidationSchema{Key: "r", Type: "relation_exists"}},
		{name: "capability key", validation: definitionmodel.ValidationSchema{Key: "c", Type: "capability_available"}, code: "backend.validation.capability_missing_key"},
		{name: "state machine missing field", validation: definitionmodel.ValidationSchema{Key: "s", Type: "state_machine", Config: map[string]any{"transitions": []any{}}}, code: "backend.validation.state_machine_missing_field"},
		{name: "state machine empty config", validation: definitionmodel.ValidationSchema{Key: "s", Type: "state_machine"}},
		{name: "state machine unchanged", validation: definitionmodel.ValidationSchema{Key: "s", Type: "state_machine", FieldKey: "status", Config: map[string]any{"transitions": []any{}}}, data: map[string]any{"status": "draft"}, prev: map[string]any{"status": "draft"}},
		{
			name: "state machine denied",
			validation: definitionmodel.ValidationSchema{Key: "s", Type: "state_machine", FieldKey: "status", Config: map[string]any{
				"transitions": []any{map[string]any{"from": "draft", "to": []string{"active"}}},
			}},
			data: map[string]any{"status": "closed"}, prev: map[string]any{"status": "draft"}, code: "backend.transition.invalid_transition",
		},
		{
			name: "state machine allowed",
			validation: definitionmodel.ValidationSchema{Key: "s", Type: "state_machine", FieldKey: "status", Config: map[string]any{
				"transitions": []any{map[string]any{"from": "draft", "to": []string{"active"}}},
			}},
			data: map[string]any{"status": "active"}, prev: map[string]any{"status": "draft"},
		},
		{name: "composite unique", validation: definitionmodel.ValidationSchema{Key: "u", Type: "composite_unique", Fields: []string{"status", "required"}}, data: map[string]any{"status": "active"}, code: "backend.validation.required"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			object := definitionmodel.ObjectSchema{Key: "case", Fields: fields, Validations: []definitionmodel.ValidationSchema{test.validation}}
			err := validateObjectRules(object, test.data, test.prev)
			assertValidationCode(t, err, test.code)
		})
	}
}

func TestValidateDataControlFlow(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "case", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text", Required: true, Config: map[string]any{"min_length": 2}}}}
	assertValidationCode(t, RecordValidateDataWithPrev(object, map[string]any{"unknown": "x"}, nil, false), "backend.validation.unknown_field")
	assertValidationCode(t, RecordValidateDataWithPrev(object, map[string]any{"name": 1}, nil, false), "backend.validation.string")
	assertValidationCode(t, RecordValidateDataWithPrev(object, map[string]any{"name": "x"}, nil, false), "backend.validation.min_length")
	assertValidationCode(t, RecordValidateDataWithPrev(object, map[string]any{}, nil, false), "backend.validation.required")
	assertValidationCode(t, RecordValidateDataWithPrev(object, map[string]any{}, nil, true), "")
	assertValidationCode(t, RecordValidateDataWithPrev(object, nil, nil, true), "")
	object.Fields[0].Required = false
	object.Validations = []definitionmodel.ValidationSchema{{Key: "required-name", Type: "required_fields", Fields: []string{"name"}}}
	assertValidationCode(t, RecordValidateDataWithPrev(object, map[string]any{}, nil, false), "backend.validation.required")
}
