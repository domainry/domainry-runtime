package validation

import (
	"encoding/json"
	"errors"
	"reflect"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

func TestNormalizeFieldMutationOwnsAllowedTypeAndRelationCardinality(t *testing.T) {
	objects := []definitionmodel.ObjectSchema{{Key: "customer"}, {Key: "order"}}
	payload, _ := json.Marshal(definitionmodel.FieldSchema{Key: "payload", Type: "json"})
	request := appschemamodel.ApplicationDefinitionUpsertRequest{ObjectKey: "order", Payload: payload}
	_, err := ApplicationSchemaNormalizeFieldMutation(request, objects, []string{"text", "relation"}, nil, 0)
	assertFieldMutationError(t, err, "backend.metadata.field_type_invalid", "allowed")
	payload, _ = json.Marshal(definitionmodel.FieldSchema{Key: "customer", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "customer"}, Config: map[string]any{"cardinality": "many_to_many"}})
	request.Payload = payload
	_, err = ApplicationSchemaNormalizeFieldMutation(request, objects, []string{"text", "relation"}, nil, 0)
	assertFieldMutationError(t, err, "backend.metadata.relation_cardinality_invalid", "cardinality")
}

func TestNormalizeFieldMutationPublishesExactCurrencyContract(t *testing.T) {
	objects := []definitionmodel.ObjectSchema{{Key: "order"}}
	payload, _ := json.Marshal(definitionmodel.FieldSchema{Key: "amount", Type: "currency"})
	request := appschemamodel.ApplicationDefinitionUpsertRequest{ObjectKey: "order", Payload: payload}
	normalized, err := ApplicationSchemaNormalizeFieldMutation(request, objects, []string{"currency"}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var field definitionmodel.FieldSchema
	if err := json.Unmarshal(normalized.Payload, &field); err != nil {
		t.Fatal(err)
	}
	if field.Config["precision"] != float64(19) || field.Config["scale"] != float64(2) || field.Config["rounding_mode"] != "half_even" || field.Config["currency_code"] != "XXX" {
		t.Fatalf("currency config=%v", field.Config)
	}
	payload, _ = json.Marshal(definitionmodel.FieldSchema{Key: "amount", Type: "currency", Config: map[string]any{"precision": 12, "scale": 4, "currency_code": "USD"}})
	request.Payload = payload
	if _, err := ApplicationSchemaNormalizeFieldMutation(request, objects, []string{"currency"}, nil, 0); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		config map[string]any
		code   string
	}{
		{map[string]any{"precision": 0}, "backend.decimal.precision_invalid"},
		{map[string]any{"scale": -1}, "backend.decimal.scale_invalid"},
		{map[string]any{"rounding_mode": "unknown"}, "backend.decimal.rounding_mode_invalid"},
		{map[string]any{"currency_code": "US"}, "backend.decimal.currency_code_invalid"},
	} {
		payload, _ = json.Marshal(definitionmodel.FieldSchema{Key: "amount", Type: "currency", Config: testCase.config})
		request.Payload = payload
		_, err = ApplicationSchemaNormalizeFieldMutation(request, objects, []string{"currency"}, nil, 0)
		assertFieldMutationError(t, err, testCase.code, "field")
	}
	if metadataDecimalErrorCode(errors.New("plain")) != "backend.decimal.value_invalid" {
		t.Fatal("unexpected decimal fallback code")
	}
}

func TestNormalizeFieldMutationPublishesClosedFilePolicy(t *testing.T) {
	objects := []definitionmodel.ObjectSchema{{Key: "document"}}
	requestFor := func(field definitionmodel.FieldSchema) appschemamodel.ApplicationDefinitionUpsertRequest {
		payload, _ := json.Marshal(field)
		return appschemamodel.ApplicationDefinitionUpsertRequest{ObjectKey: "document", Payload: payload}
	}
	normalized, err := ApplicationSchemaNormalizeFieldMutation(requestFor(definitionmodel.FieldSchema{Key: "attachments", Type: "file_list"}), objects, []string{"file", "file_list"}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var field definitionmodel.FieldSchema
	if err := json.Unmarshal(normalized.Payload, &field); err != nil {
		t.Fatal(err)
	}
	if field.Config["max_size_bytes"] != float64(5<<20) || field.Config["max_files"] != float64(10) || field.Config["scan_required"] != true {
		t.Fatalf("file defaults=%#v", field.Config)
	}
	for _, testCase := range []struct {
		field definitionmodel.FieldSchema
		code  string
	}{
		{definitionmodel.FieldSchema{Key: "attachment", Type: "file", Unique: true}, "backend.file.unique_unsupported"},
		{definitionmodel.FieldSchema{Key: "attachment", Type: "file", DefaultValue: map[string]any{"file_id": "shared"}}, "backend.file.default_unsupported"},
		{definitionmodel.FieldSchema{Key: "attachment", Type: "file", Config: map[string]any{"max_files": 2}}, "backend.file.max_files_invalid"},
		{definitionmodel.FieldSchema{Key: "attachments", Type: "file_list", Config: map[string]any{"allowed_mime_types": []any{"application/pdf", "application/pdf"}}}, "backend.file.allowed_mime_types_invalid"},
		{definitionmodel.FieldSchema{Key: "attachment", Type: "file", Config: map[string]any{"scan_required": "yes"}}, "backend.file.scan_required_invalid"},
		{definitionmodel.FieldSchema{Key: "attachment", Type: "file", Config: map[string]any{"indexed": "false"}}, "backend.structured.indexed_invalid"},
		{definitionmodel.FieldSchema{Key: "title", Type: "text", Config: map[string]any{"max_files": 2}}, "backend.file.config_on_non_file"},
	} {
		_, err := ApplicationSchemaNormalizeFieldMutation(requestFor(testCase.field), objects, []string{"file", "file_list", "text"}, nil, 0)
		assertFieldMutationError(t, err, testCase.code, "field")
	}
}

func TestNormalizeFieldMutationPublishesClosedStructuredPolicies(t *testing.T) {
	objects := []definitionmodel.ObjectSchema{{Key: "item"}}
	requestFor := func(field definitionmodel.FieldSchema) appschemamodel.ApplicationDefinitionUpsertRequest {
		payload, _ := json.Marshal(field)
		return appschemamodel.ApplicationDefinitionUpsertRequest{ObjectKey: "item", Payload: payload}
	}
	multi := definitionmodel.FieldSchema{Key: "tags", Type: "multi_select", DefaultValue: []any{"b", "a", "b"}}
	normalized, err := ApplicationSchemaNormalizeFieldMutation(requestFor(multi), objects, []string{"multi_select", "json", "text"}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var field definitionmodel.FieldSchema
	if err := json.Unmarshal(normalized.Payload, &field); err != nil {
		t.Fatal(err)
	}
	if field.Config["max_items"] != float64(100) || !reflect.DeepEqual(field.DefaultValue, []any{"a", "b"}) {
		t.Fatalf("multi field=%#v", field)
	}
	jsonField := definitionmodel.FieldSchema{Key: "payload", Type: "json", Config: map[string]any{"json_shape": "array"}, DefaultValue: []any{map[string]any{"b": 2, "a": 1}}}
	normalized, err = ApplicationSchemaNormalizeFieldMutation(requestFor(jsonField), objects, []string{"multi_select", "json", "text"}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(normalized.Payload, &field); err != nil {
		t.Fatal(err)
	}
	if field.Config["json_shape"] != "array" || field.Config["max_json_bytes"] != float64(64<<10) {
		t.Fatalf("json field=%#v", field)
	}
	for _, testCase := range []struct {
		field definitionmodel.FieldSchema
		code  string
	}{
		{definitionmodel.FieldSchema{Key: "tags", Type: "multi_select", Unique: true}, "backend.structured.unique_unsupported"},
		{definitionmodel.FieldSchema{Key: "tags", Type: "multi_select", Config: map[string]any{"indexed": true}}, "backend.structured.index_unsupported"},
		{definitionmodel.FieldSchema{Key: "tags", Type: "multi_select", Config: map[string]any{"indexed": "false"}}, "backend.structured.indexed_invalid"},
		{definitionmodel.FieldSchema{Key: "payload", Type: "json", Config: map[string]any{"json_shape": "scalar"}}, "backend.json.shape_invalid"},
		{definitionmodel.FieldSchema{Key: "payload", Type: "json", Options: []any{map[string]any{"value": "x", "label": "X"}}}, "backend.json.options_unsupported"},
		{definitionmodel.FieldSchema{Key: "title", Type: "text", Config: map[string]any{"max_items": 2}}, "backend.structured.config_on_non_structured"},
	} {
		_, err := ApplicationSchemaNormalizeFieldMutation(requestFor(testCase.field), objects, []string{"multi_select", "json", "text"}, nil, 0)
		assertFieldMutationError(t, err, testCase.code, "field")
	}
}

func assertFieldMutationError(t *testing.T, err error, code, parameter string) {
	t.Helper()
	var appError *apperror.AppError
	if !errors.As(err, &appError) || appError.Code != code || appError.Params[parameter] == "" {
		t.Fatalf("error=%#v", err)
	}
}

func TestNormalizeFieldMutationRejectsInvalidUpgradeRule(t *testing.T) {
	objects := []definitionmodel.ObjectSchema{{Key: "customer"}}
	payload, _ := json.Marshal(definitionmodel.FieldSchema{Key: "region", Type: "text", Upgrade: &definitionmodel.FieldUpgradeRule{ExistingRows: "exempt"}})
	request := appschemamodel.ApplicationDefinitionUpsertRequest{ObjectKey: "customer", Payload: payload}
	_, err := ApplicationSchemaNormalizeFieldMutation(request, objects, []string{"text"}, nil, 0)
	assertFieldMutationError(t, err, "backend.metadata.field_upgrade_rule_invalid", "reason")
	payload, _ = json.Marshal(definitionmodel.FieldSchema{Key: "region", Type: "text", Required: true, Upgrade: &definitionmodel.FieldUpgradeRule{ExistingRows: "exempt"}})
	request.Payload = payload
	if _, err := ApplicationSchemaNormalizeFieldMutation(request, objects, []string{"text"}, nil, 0); err != nil {
		t.Fatalf("valid exempt rule rejected: %v", err)
	}
}
