package validation

import (
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestRecordValidateFieldUpgradeRule(t *testing.T) {
	for _, test := range []struct {
		name   string
		field  definitionmodel.FieldSchema
		reason string
	}{
		{name: "no rule", field: definitionmodel.FieldSchema{Key: "tier", Type: "text"}},
		{name: "backfill text", field: definitionmodel.FieldSchema{Key: "tier", Type: "text", Required: true, Upgrade: &definitionmodel.FieldUpgradeRule{ExistingRows: "backfill", BackfillValue: "standard"}}},
		{name: "backfill integer from float", field: definitionmodel.FieldSchema{Key: "level", Type: "integer", Upgrade: &definitionmodel.FieldUpgradeRule{ExistingRows: "backfill", BackfillValue: float64(3)}}},
		{name: "exempt required", field: definitionmodel.FieldSchema{Key: "region", Type: "text", Required: true, Upgrade: &definitionmodel.FieldUpgradeRule{ExistingRows: "exempt"}}},
		{name: "unknown rule", field: definitionmodel.FieldSchema{Key: "tier", Type: "text", Upgrade: &definitionmodel.FieldUpgradeRule{ExistingRows: "drop"}}, reason: "existing_rows_invalid"},
		{name: "backfill without value", field: definitionmodel.FieldSchema{Key: "tier", Type: "text", Upgrade: &definitionmodel.FieldUpgradeRule{ExistingRows: "backfill"}}, reason: "backfill_value_required"},
		{name: "backfill wrong type", field: definitionmodel.FieldSchema{Key: "level", Type: "integer", Upgrade: &definitionmodel.FieldUpgradeRule{ExistingRows: "backfill", BackfillValue: "many"}}, reason: "backfill_value_invalid"},
		{name: "backfill empty", field: definitionmodel.FieldSchema{Key: "tier", Type: "text", Upgrade: &definitionmodel.FieldUpgradeRule{ExistingRows: "backfill", BackfillValue: "  "}}, reason: "backfill_value_empty"},
		{name: "exempt optional", field: definitionmodel.FieldSchema{Key: "region", Type: "text", Upgrade: &definitionmodel.FieldUpgradeRule{ExistingRows: "exempt"}}, reason: "exempt_requires_required"},
		{name: "exempt with value", field: definitionmodel.FieldSchema{Key: "region", Type: "text", Required: true, Upgrade: &definitionmodel.FieldUpgradeRule{ExistingRows: "exempt", BackfillValue: "x"}}, reason: "exempt_rejects_backfill_value"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := RecordValidateFieldUpgradeRule("customer", test.field)
			if test.reason == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			var coded *apperror.CodedError
			if !errors.As(err, &coded) || coded.Code != FieldUpgradeRuleInvalidCode || coded.Params["reason"] != test.reason || coded.Params["object"] != "customer" || coded.Params["field"] != test.field.Key {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestExemptUpgradeRuleRelaxesRequiredOnlyForRowsThatNeverHadTheField(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{
		{Key: "name", Type: "text", Required: true},
		{Key: "region", Type: "text", Required: true, Upgrade: &definitionmodel.FieldUpgradeRule{ExistingRows: definitionmodel.FieldUpgradeExempt}},
	}}
	// Old row without the field can still be updated.
	assertValidationCode(t, RecordValidateDataWithPrev(object, map[string]any{"name": "Acme"}, map[string]any{"name": "Acme"}, false), "")
	// Create still requires the field.
	assertValidationCode(t, RecordValidateData(object, map[string]any{"name": "Acme"}, false), "backend.validation.required")
	assertValidationCode(t, RecordValidateDataWithPrev(object, map[string]any{"name": "Acme"}, nil, false), "backend.validation.required")
	// Clearing a previously filled value is rejected.
	assertValidationCode(t, RecordValidateDataWithPrev(object, map[string]any{"name": "Acme", "region": ""}, map[string]any{"name": "Acme", "region": "north"}, false), "backend.validation.required")
	// Required fields without the rule keep failing on updates too.
	assertValidationCode(t, RecordValidateDataWithPrev(object, map[string]any{"region": "north"}, map[string]any{"region": "north"}, false), "backend.validation.required")
}
