package record

import (
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	"testing"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestEnrichObjectsWithFieldValueDomainsAndCoerceImportValue(t *testing.T) {
	field := definitionmodel.FieldSchema{Key: "status", Type: "status"}
	field.Validation.Options = []string{"open", "closed"}
	field.Config = map[string]any{"value_domain_items": []any{
		map[string]any{"key": "open", "value": "open", "label": "Open"},
		map[string]any{"key": "closed", "value": "closed", "label": "Closed"},
	}}
	if value, issue := recordvalidation.RecordCoerceImportValue("ticket", field, "Closed"); value != "closed" || issue != "" {
		t.Fatalf("value=%#v issue=%q", value, issue)
	}
	if value, issue := recordvalidation.RecordCoerceImportValue("ticket", field, "Unknown"); value != nil || issue != "backend.import.invalid_value_domain_option" {
		t.Fatalf("value=%#v issue=%q", value, issue)
	}
}

func TestImportHeaderAliasesAndErrorSummary(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "ticket", Fields: []definitionmodel.FieldSchema{{Key: "assignee_id", Name: "Assignee", Config: map[string]any{"aliases": []any{"Owner", "负责人"}}}}}
	aliases := recordvalidation.RecordImportFieldHeaderAliases(object)
	if aliases["owner"].Key != "assignee_id" || aliases["负责人"].Key != "assignee_id" || aliases["assigneeid"].Key != "assignee_id" {
		t.Fatalf("aliases=%#v", aliases)
	}
	row := recordmodel.RecordImportPreviewRow{Issues: []recordmodel.RecordImportRowIssue{{Field: "assignee_id", Code: "backend.import.invalid_value"}}}
	if summary := recordvalidation.RecordImportErrorSummary(row); summary != "assignee_id:backend.import.invalid_value" {
		t.Fatalf("summary=%q", summary)
	}
}
