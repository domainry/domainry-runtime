package validation

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRecordContainsPreservesLiteralSubstringsAndRejectsNonText(t *testing.T) {
	for _, kind := range []string{"text", "long_text", "email", "phone", "url"} {
		object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "name", Type: kind}}}
		for _, literal := range []string{" 50%_off~猫 ", "@example.", "", "' OR 1=1 --", "[a-z]"} {
			filter := recordmodel.RecordFilterExpression{Field: "name", Operator: "contains", Value: literal}
			got, err := RecordNormalizeFilterExpression(object, &filter)
			if err != nil || got.Value != literal {
				t.Fatalf("type=%s literal=%q normalized=%+v error=%v", kind, literal, got, err)
			}
		}
	}
	for _, kind := range []string{"currency", "number", "integer", "percent", "boolean", "date", "datetime", "select", "relation", "reference", "user"} {
		object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "value", Type: kind}}}
		filter := recordmodel.RecordFilterExpression{Field: "value", Operator: "contains", Value: "1"}
		if _, err := RecordNormalizeFilterExpression(object, &filter); err == nil {
			t.Fatalf("contains accepted %s", kind)
		}
	}
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	for _, filter := range []recordmodel.RecordFilterExpression{
		{Field: "name", Operator: "contains", Value: 1},
		{Field: "name", Operator: "contains"},
		{Field: "name", Operator: "contains", Value: "x", Values: []any{"x"}},
		{Field: "name", Operator: "contains", Value: "x", Children: []recordmodel.RecordFilterExpression{{Field: "id", Operator: "eq", Value: "x"}}},
		{Field: "missing", Operator: "contains", Value: "x"},
		{Field: "created_at", Operator: "contains", Value: "2026"},
		{Field: "updated_at", Operator: "contains", Value: "2026"},
		{Field: "owner_org_id", Operator: "contains", Value: "store"},
	} {
		if _, err := RecordNormalizeFilterExpression(object, &filter); err == nil {
			t.Fatalf("invalid filter accepted: %+v", filter)
		}
	}
	filter := recordmodel.RecordFilterExpression{Field: "id", Operator: "contains", Value: "order-"}
	if _, err := RecordNormalizeFilterExpression(object, &filter); err != nil {
		t.Fatalf("record identity substring: %v", err)
	}
}
