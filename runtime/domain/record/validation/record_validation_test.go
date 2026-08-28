package validation

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestValidateDataRejectsMissingRequiredAndUnknownFields(t *testing.T) {
	object := definitionmodel.ObjectSchema{
		Key:  "customer",
		Name: "Customer",
		Fields: []definitionmodel.FieldSchema{
			{Key: "name", Name: "Name", Type: "text", Required: true},
			{Key: "status", Name: "Status", Type: "text"},
		},
	}

	if err := RecordValidateData(object, map[string]any{"status": "active"}, false); err == nil {
		t.Fatalf("expected missing required field to fail")
	}

	if err := RecordValidateData(object, map[string]any{"name": "Acme", "unknown": "x"}, false); err == nil {
		t.Fatalf("expected unknown field to fail")
	}

	if err := RecordValidateData(object, map[string]any{"name": "Acme", "status": "active"}, false); err != nil {
		t.Fatalf("expected valid data, got %v", err)
	}
}
