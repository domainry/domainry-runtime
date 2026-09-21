package recordmodel

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestRecordNormalizeMultiSelectCanonicalSet(t *testing.T) {
	field := definitionmodel.FieldSchema{Key: "tags", Type: RecordMultiSelectFieldType, Config: map[string]any{"max_items": 4}}
	got, err := RecordNormalizeStructuredFieldValue(field, []any{" blue ", "red", "blue"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"blue", "red"}) {
		t.Fatalf("normalized=%#v", got)
	}
	deduplicated, err := RecordNormalizeStructuredFieldValue(definitionmodel.FieldSchema{Key: "tags", Type: RecordMultiSelectFieldType, Config: map[string]any{"max_items": 1}}, []any{"blue", " blue ", "blue"})
	if err != nil || !reflect.DeepEqual(deduplicated, []string{"blue"}) {
		t.Fatalf("deduplicated=%#v err=%v", deduplicated, err)
	}
	encoded, err := RecordEncodeStructuredFieldValue(field, got)
	if err != nil || encoded != `["blue","red"]` {
		t.Fatalf("encoded=%q err=%v", encoded, err)
	}
	decoded, err := RecordDecodeStructuredFieldValue(field, encoded)
	if err != nil || !reflect.DeepEqual(decoded, got) {
		t.Fatalf("decoded=%#v err=%v", decoded, err)
	}
}

func TestRecordNormalizeMultiSelectRejectsInvalidContracts(t *testing.T) {
	base := definitionmodel.FieldSchema{Key: "tags", Type: RecordMultiSelectFieldType}
	for _, test := range []struct {
		name  string
		field definitionmodel.FieldSchema
		value any
		code  string
	}{
		{name: "scalar", field: base, value: "blue", code: "backend.validation.multi_select"},
		{name: "non string", field: base, value: []any{"blue", 1}, code: "backend.validation.multi_select"},
		{name: "blank", field: base, value: []any{" "}, code: "backend.validation.multi_select_item"},
		{name: "too many", field: definitionmodel.FieldSchema{Key: "tags", Type: RecordMultiSelectFieldType, Config: map[string]any{"max_items": 1}}, value: []any{"a", "b"}, code: "backend.validation.multi_select_max_items"},
		{name: "unique", field: definitionmodel.FieldSchema{Key: "tags", Type: RecordMultiSelectFieldType, Unique: true}, value: []any{}, code: "backend.structured.unique_unsupported"},
		{name: "indexed", field: definitionmodel.FieldSchema{Key: "tags", Type: RecordMultiSelectFieldType, Config: map[string]any{"indexed": true}}, value: []any{}, code: "backend.structured.index_unsupported"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := RecordNormalizeStructuredFieldValue(test.field, test.value)
			if err == nil || !strings.Contains(err.Error(), test.code) {
				t.Fatalf("err=%v want %s", err, test.code)
			}
		})
	}
}

func TestRecordNormalizeJSONBoundsShapeAndNumberIdentity(t *testing.T) {
	field := definitionmodel.FieldSchema{Key: "payload", Type: RecordJSONFieldType, Config: map[string]any{"json_shape": "object", "max_json_bytes": 1024}}
	decoder := json.NewDecoder(strings.NewReader(`{"count":9007199254740993,"nested":[true,null]}`))
	decoder.UseNumber()
	var input any
	if err := decoder.Decode(&input); err != nil {
		t.Fatal(err)
	}
	got, err := RecordNormalizeStructuredFieldValue(field, input)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := RecordEncodeStructuredFieldValue(field, got)
	if err != nil || encoded != `{"count":9007199254740993,"nested":[true,null]}` {
		t.Fatalf("encoded=%q err=%v", encoded, err)
	}
	decoded, err := RecordDecodeStructuredFieldValue(field, encoded)
	if err != nil || !reflect.DeepEqual(decoded, got) {
		t.Fatalf("decoded=%#v err=%v", decoded, err)
	}
	if _, err := RecordNormalizeStructuredFieldValue(field, []any{"wrong-shape"}); err == nil || !strings.Contains(err.Error(), "backend.validation.json_shape") {
		t.Fatalf("shape err=%v", err)
	}
	tiny := field
	tiny.Config = map[string]any{"max_json_bytes": 2}
	if _, err := RecordNormalizeStructuredFieldValue(tiny, map[string]any{"a": 1}); err == nil || !strings.Contains(err.Error(), "backend.validation.json_too_large") {
		t.Fatalf("size err=%v", err)
	}
}

func TestRecordStructuredDefinitionRejectsJSONOptionsAndInvalidDefaults(t *testing.T) {
	jsonField := definitionmodel.FieldSchema{Key: "payload", Type: RecordJSONFieldType, Validation: definitionmodel.FieldValidation{Options: []string{"x"}}}
	if err := RecordValidateStructuredFieldDefinition(jsonField); err == nil || !strings.Contains(err.Error(), "backend.json.options_unsupported") {
		t.Fatalf("options err=%v", err)
	}
	multi := definitionmodel.FieldSchema{Key: "tags", Type: RecordMultiSelectFieldType, DefaultValue: "not-an-array"}
	if err := RecordValidateStructuredFieldDefinition(multi); err == nil || !strings.Contains(err.Error(), "backend.validation.multi_select") {
		t.Fatalf("default err=%v", err)
	}
}
