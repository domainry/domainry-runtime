package validation

import (
	"encoding/json"
	"errors"

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

func assertFieldMutationError(t *testing.T, err error, code, parameter string) {
	t.Helper()
	var appError *apperror.AppError
	if !errors.As(err, &appError) || appError.Code != code || appError.Params[parameter] == "" {
		t.Fatalf("error=%#v", err)
	}
}
