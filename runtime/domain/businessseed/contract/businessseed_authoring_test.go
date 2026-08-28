package contract

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestBusinessSeedAuthoringPublishesGenericAndSpecializedSchemas(t *testing.T) {
	domain := BusinessSeedAuthoringDomain()
	if domain.Key != "seed" || len(domain.Capabilities) != 1 || domain.Capabilities[0].Key != "seed.record" || len(domain.Capabilities[0].Examples) != 0 {
		t.Fatalf("domain=%#v", domain)
	}

	defaultText, legacyText := any("default"), any("legacy")
	object := definitionmodel.ObjectSchema{Key: "asset", Fields: []definitionmodel.FieldSchema{
		{Key: "required_text", Type: "text", Required: true},
		{Key: "optional_text", Type: "text"},
		{Key: "default_text", Type: "text", Required: true, Default: defaultText},
		{Key: "legacy_text", Type: "text", Required: true, DefaultValue: legacyText},
		{Key: "integer_value", Type: "integer", Required: true},
		{Key: "number_value", Type: "number", Required: true},
		{Key: "percent_value", Type: "percent", Required: true},
		{Key: "boolean_value", Type: "boolean", Required: true},
		{Key: "json_value", Type: "json", Required: true},
		{Key: "date_value", Type: "date", Required: true},
		{Key: "datetime_value", Type: "datetime", Required: true},
		{Key: "status", Type: "select", Required: true, Validation: definitionmodel.FieldValidation{Options: []string{"active", "inactive"}}},
	}}
	capability := SpecializeSeedRecordAuthoringCapability(object)
	if capability.Key != "seed.record" || capability.InputSchema == nil || capability.OutputSchema == nil || len(capability.Examples) != 3 {
		t.Fatalf("capability=%#v", capability)
	}
	data := capability.InputSchema.Properties["data"]
	if len(data.Properties) != len(object.Fields) || len(data.Required) != 9 || data.Properties["integer_value"].Type != "integer" || data.Properties["number_value"].Type != "number" || data.Properties["percent_value"].Type != "number" || data.Properties["boolean_value"].Type != "boolean" || data.Properties["json_value"].Type != "object" {
		t.Fatalf("data schema=%#v", data)
	}
	if got := data.Properties["default_text"].Default; got != defaultText {
		t.Fatalf("default=%#v", got)
	}
	if got := data.Properties["legacy_text"].Default; got != legacyText {
		t.Fatalf("legacy default=%#v", got)
	}
	if got := data.Properties["status"].Enum; len(got) != 2 || got[0] != "active" || got[1] != "inactive" {
		t.Fatalf("enum=%#v", got)
	}
	if businessSeedIntPointer(7) == nil {
		t.Fatal("integer pointer helper returned nil")
	}
}

func TestBusinessSeedExampleValuesCoverEveryGenericFieldKind(t *testing.T) {
	defaultValue, legacyValue := any("default"), any("legacy")
	tests := []struct {
		field definitionmodel.FieldSchema
		want  any
	}{
		{field: definitionmodel.FieldSchema{Default: defaultValue}, want: defaultValue},
		{field: definitionmodel.FieldSchema{DefaultValue: legacyValue}, want: legacyValue},
		{field: definitionmodel.FieldSchema{Type: "integer"}, want: 1},
		{field: definitionmodel.FieldSchema{Type: "number"}, want: 1.0},
		{field: definitionmodel.FieldSchema{Type: "percent"}, want: 1.0},
		{field: definitionmodel.FieldSchema{Type: "boolean"}, want: true},
		{field: definitionmodel.FieldSchema{Type: "json"}, want: map[string]any{}},
		{field: definitionmodel.FieldSchema{Type: "date"}, want: "2026-01-01"},
		{field: definitionmodel.FieldSchema{Type: "datetime"}, want: "2026-01-01T00:00:00Z"},
		{field: definitionmodel.FieldSchema{Key: "name", Type: "text"}, want: "example_name"},
	}
	for _, test := range tests {
		if got := businessSeedExampleValue(test.field); !businessSeedValuesEqual(got, test.want) {
			t.Fatalf("field=%#v got=%#v want=%#v", test.field, got, test.want)
		}
	}
}

func businessSeedValuesEqual(left, right any) bool {
	leftMap, leftOK := left.(map[string]any)
	rightMap, rightOK := right.(map[string]any)
	if leftOK || rightOK {
		return leftOK && rightOK && len(leftMap) == len(rightMap)
	}
	return left == right
}
