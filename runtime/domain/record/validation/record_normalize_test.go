package validation

import (
	"encoding/json"
	"errors"
	"math"
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestNormalizeDataAndFieldTypes(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "order", Fields: []definitionmodel.FieldSchema{
		{Key: "amount", Type: "currency"}, {Key: "active", Type: "boolean"}, {Key: "day", Type: "date"},
		{Key: "at", Type: "datetime"}, {Key: "name", Type: "text"}, {Key: "optional", Type: "text"},
	}}
	normalized, err := RecordNormalizeData(object, map[string]any{
		"amount": "12.5", "active": "yes", "day": "2026-07-18", "at": "2026-07-18T10:00:00Z", "name": " Alice ", "optional": " ",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if normalized["amount"] != "12.50" || normalized["active"] != true || normalized["name"] != "Alice" || normalized["optional"] != "" {
		t.Fatalf("normalized=%#v", normalized)
	}
	partial, err := RecordNormalizeData(object, map[string]any{"optional": nil, "name": " "}, true)
	if err != nil || len(partial) != 1 || partial["optional"] != nil {
		t.Fatalf("partial=%#v err=%v", partial, err)
	}
	if empty, err := RecordNormalizeData(object, nil, true); err != nil || len(empty) != 0 {
		t.Fatalf("empty=%#v err=%v", empty, err)
	}
	if _, err := RecordNormalizeData(object, map[string]any{"unknown": 1}, false); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestNormalizeFieldValueErrors(t *testing.T) {
	tests := []struct {
		name  string
		field definitionmodel.FieldSchema
		value any
		code  string
	}{
		{name: "number type", field: definitionmodel.FieldSchema{Key: "n", Type: "number"}, value: true, code: "backend.validation.number"},
		{name: "nan", field: definitionmodel.FieldSchema{Key: "n", Type: "number"}, value: math.NaN(), code: "backend.validation.finite_number"},
		{name: "infinity", field: definitionmodel.FieldSchema{Key: "n", Type: "number"}, value: math.Inf(1), code: "backend.validation.finite_number"},
		{name: "currency binary float", field: definitionmodel.FieldSchema{Key: "amount", Type: "currency"}, value: 1.2, code: "backend.decimal.value_invalid"},
		{name: "currency config", field: definitionmodel.FieldSchema{Key: "amount", Type: "currency", Config: map[string]any{"scale": -1}}, value: "1.2", code: "backend.decimal.scale_invalid"},
		{name: "boolean", field: definitionmodel.FieldSchema{Key: "b", Type: "boolean"}, value: "maybe", code: "backend.validation.boolean"},
		{name: "date type", field: definitionmodel.FieldSchema{Key: "d", Type: "date"}, value: 1, code: "backend.validation.date_string"},
		{name: "date format", field: definitionmodel.FieldSchema{Key: "d", Type: "date"}, value: "18/07/2026", code: "backend.validation.date_format"},
		{name: "datetime type", field: definitionmodel.FieldSchema{Key: "d", Type: "datetime"}, value: 1, code: "backend.validation.datetime_string"},
		{name: "datetime format", field: definitionmodel.FieldSchema{Key: "d", Type: "datetime"}, value: "today", code: "backend.validation.datetime_format"},
		{name: "string type", field: definitionmodel.FieldSchema{Key: "s", Type: "text"}, value: 1, code: "backend.validation.string"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := RecordNormalizeFieldValue(test.field, test.value)
			assertValidationCode(t, err, test.code)
		})
	}
	for _, test := range []struct {
		field definitionmodel.FieldSchema
		value any
		want  any
	}{
		{definitionmodel.FieldSchema{Type: "text"}, nil, nil},
		{definitionmodel.FieldSchema{Type: "date"}, " ", ""},
		{definitionmodel.FieldSchema{Type: "datetime"}, " ", ""},
		{definitionmodel.FieldSchema{Type: "boolean"}, 1, true},
		{definitionmodel.FieldSchema{Type: "number"}, json.Number("2"), float64(2)},
		{definitionmodel.FieldSchema{Type: "currency"}, json.Number("2"), "2.00"},
		{definitionmodel.FieldSchema{Type: "integer"}, int64(math.MaxInt64), int64(math.MaxInt64)},
	} {
		actual, err := RecordNormalizeFieldValue(test.field, test.value)
		if err != nil || actual != test.want {
			t.Errorf("RecordNormalizeFieldValue(%#v)=%#v err=%v, want %#v", test.value, actual, err, test.want)
		}
	}
	assertValidationCode(t, recordDecimalValidationError(errors.New("plain"), "amount"), "backend.decimal.value_invalid")
}

func TestValidateFieldTypeAndRules(t *testing.T) {
	for _, test := range []struct {
		field definitionmodel.FieldSchema
		value any
		code  string
	}{
		{definitionmodel.FieldSchema{Key: "x", Type: "text"}, 1, "backend.validation.string"},
		{definitionmodel.FieldSchema{Key: "x", Type: "number"}, "1", "backend.validation.number"},
		{definitionmodel.FieldSchema{Key: "x", Type: "number"}, math.Inf(1), "backend.validation.finite_number"},
		{definitionmodel.FieldSchema{Key: "x", Type: "boolean"}, "true", "backend.validation.boolean"},
		{definitionmodel.FieldSchema{Key: "x", Type: "text"}, nil, ""},
		{definitionmodel.FieldSchema{Key: "x", Type: "number"}, float32(1), ""},
		{definitionmodel.FieldSchema{Key: "x", Type: "currency"}, "1.20", ""},
		{definitionmodel.FieldSchema{Key: "x", Type: "currency", Config: map[string]any{"rounding_mode": "bad"}}, "1.20", "backend.decimal.rounding_mode_invalid"},
		{definitionmodel.FieldSchema{Key: "x", Type: "currency"}, true, "backend.decimal.value_invalid"},
		{definitionmodel.FieldSchema{Key: "x", Type: "unknown"}, struct{}{}, ""},
	} {
		assertValidationCode(t, validateFieldType(test.field, test.value), test.code)
	}
	text := definitionmodel.FieldSchema{Key: "name", Type: "text", Config: map[string]any{"min_length": 2, "max_length": 4, "pattern": "^[a-z]+$"}}
	assertValidationCode(t, validateFieldRules(text, "a"), "backend.validation.min_length")
	assertValidationCode(t, validateFieldRules(text, "abcde"), "backend.validation.max_length")
	assertValidationCode(t, validateFieldRules(text, "AB"), "backend.validation.invalid_format")
	text.Config["pattern"] = "["
	assertValidationCode(t, validateFieldRules(text, "ab"), "backend.validation.invalid_pattern")
	selectField := definitionmodel.FieldSchema{Key: "status", Type: "select", Validation: definitionmodel.FieldValidation{Options: []string{"active"}}}
	assertValidationCode(t, validateFieldRules(selectField, "blocked"), "backend.validation.invalid_option")
	number := definitionmodel.FieldSchema{Key: "amount", Type: "number", Config: map[string]any{"min": 1, "max": 3}}
	assertValidationCode(t, validateFieldRules(number, 0), "backend.validation.numeric_min")
	assertValidationCode(t, validateFieldRules(number, 4), "backend.validation.numeric_max")
	assertValidationCode(t, validateFieldRules(number, 2), "")
	assertValidationCode(t, validateFieldRules(number, nil), "")
}

func TestSelectFieldOptionsSources(t *testing.T) {
	tests := []struct {
		name  string
		field definitionmodel.FieldSchema
		want  string
	}{
		{name: "validation", field: definitionmodel.FieldSchema{Validation: definitionmodel.FieldValidation{Options: []string{"", " validation "}}}, want: "validation"},
		{name: "typed dictionary", field: definitionmodel.FieldSchema{Options: []appschemamodel.DictionaryItemSchema{{Key: "k", Value: "value"}}}, want: "value"},
		{name: "map dictionary", field: definitionmodel.FieldSchema{Options: []map[string]any{{"key": "map-key"}}}, want: "map-key"},
		{name: "mixed dictionary", field: definitionmodel.FieldSchema{Options: []any{appschemamodel.DictionaryItemSchema{Key: "typed"}, map[string]any{"value": "mapped"}, "direct", 1}}, want: "typed"},
		{name: "config options", field: definitionmodel.FieldSchema{Config: map[string]any{"options": []any{"configured"}}}, want: "configured"},
		{name: "snake value domain", field: definitionmodel.FieldSchema{Config: map[string]any{"value_domain": map[string]any{"items": []any{"snake"}}}}, want: "snake"},
		{name: "camel value domain", field: definitionmodel.FieldSchema{Config: map[string]any{"valueDomain": map[string]any{"items": []any{"camel"}}}}, want: "camel"},
		{name: "typed fallback", field: definitionmodel.FieldSchema{Config: map[string]any{"options": []string{"typed-fallback"}}}, want: "typed-fallback"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := selectFieldOptions(test.field)
			if len(options) == 0 || options[0] != test.want {
				t.Fatalf("options=%#v want first=%q", options, test.want)
			}
		})
	}
	if options := dictionaryItemOptionKeys(1); options != nil {
		t.Fatalf("unexpected options %#v", options)
	}
	if actual := dictionaryItemOptionKey(" key ", " "); actual != "key" {
		t.Fatalf("key fallback=%q", actual)
	}
	if actual := dictionaryItemOptionKey("<nil>", "<nil>"); actual != "" {
		t.Fatalf("nil-like option=%q", actual)
	}
}

func TestRuleConfigHelpers(t *testing.T) {
	config := map[string]any{"text": " value ", "blank": " ", "float": 1.5, "int": 2, "int64": int64(3), "list": []any{"a", 2}, "typed": []string{"b"}}
	if value, ok := stringConfig(config, "text"); !ok || value != " value " {
		t.Fatalf("string config=(%q,%v)", value, ok)
	}
	if _, ok := stringConfig(config, "blank"); ok {
		t.Fatal("blank string config accepted")
	}
	if value, ok := intConfig(config, "float"); !ok || value != 1 {
		t.Fatalf("int config=(%d,%v)", value, ok)
	}
	for key, want := range map[string]float64{"float": 1.5, "int": 2, "int64": 3} {
		if value, ok := floatConfig(config, key); !ok || value != want {
			t.Errorf("floatConfig(%s)=(%v,%v)", key, value, ok)
		}
	}
	if _, ok := floatConfig(config, "text"); ok {
		t.Fatal("text accepted as float config")
	}
	if actual := stringListConfig(config, "list"); len(actual) != 2 || actual[1] != "2" {
		t.Fatalf("list=%#v", actual)
	}
	if actual := stringListConfig(config, "typed"); len(actual) != 1 || actual[0] != "b" {
		t.Fatalf("typed list=%#v", actual)
	}
	if actual := stringListConfig(config, "missing"); actual != nil {
		t.Fatalf("missing list=%#v", actual)
	}
}
