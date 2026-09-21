package validation

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRecordImportHeaderAliasesAndSummary(t *testing.T) {
	field := definitionmodel.FieldSchema{Key: "customer_name", Name: "Customer Name", Config: map[string]any{"label": "Client", "aliases": []any{"Buyer", nil}, "ui": map[string]any{"display_label": "Display"}}}
	aliases := RecordImportFieldHeaderAliases(definitionmodel.ObjectSchema{Key: "order", Fields: []definitionmodel.FieldSchema{field}})
	for _, key := range []string{"customername", "client", "buyer", "display"} {
		if aliases[key].Key != "customer_name" {
			t.Fatalf("missing %s: %#v", key, aliases)
		}
	}
	if RecordNormalizeImportHeaderAlias(" Customer_Name ") != "customername" {
		t.Fatal("normalize")
	}
	if got := RecordImportErrorSummary(recordmodel.RecordImportPreviewRow{}); got != "" {
		t.Fatalf("%q", got)
	}
	if got := RecordImportErrorSummary(recordmodel.RecordImportPreviewRow{Duplicate: true}); got != "backend.import.duplicate_row" {
		t.Fatalf("%q", got)
	}
	row := recordmodel.RecordImportPreviewRow{Issues: []recordmodel.RecordImportRowIssue{{Field: "name", Code: "bad"}, {Message: "fallback"}, {}}}
	if got := RecordImportErrorSummary(row); got != "name:bad; fallback" {
		t.Fatalf("%q", got)
	}
	if importMapValue("bad") != nil || importMapValue(map[string]any{"x": 1})["x"] != 1 {
		t.Fatal("map value")
	}
}

func TestRecordImportValueDomains(t *testing.T) {
	items := []any{
		appschemamodel.DictionaryItemSchema{Key: "open", Value: "OPEN", Label: "Open", Config: map[string]any{"aliases": []string{"Active"}}},
		map[string]any{"key": "closed", "value": "CLOSED", "label": "Closed", "description": "Finished", "ui": map[string]any{"zh": "关闭"}},
		"pending", " ", 123,
	}
	field := definitionmodel.FieldSchema{Key: "status", Type: "status", Config: map[string]any{"value_domain": map[string]any{"items": items}}}
	for input, want := range map[string]string{"open": "OPEN", "Open": "OPEN", "active": "OPEN", "Finished": "CLOSED", "关闭": "CLOSED", "pending": "pending"} {
		got, issue := RecordCoerceImportValue("order", field, input)
		if issue != "" || got != want {
			t.Fatalf("%s => %#v %s", input, got, issue)
		}
	}
	if got, issue := RecordCoerceImportValue("order", field, "unknown"); got != nil || issue != "backend.import.invalid_value_domain_option" {
		t.Fatalf("%#v %s", got, issue)
	}
	candidates := RecordImportValueDomainCandidates(field)
	if len(candidates) != 3 {
		t.Fatalf("%#v", candidates)
	}
	for _, tc := range []struct {
		field definitionmodel.FieldSchema
		input string
		want  any
	}{
		{definitionmodel.FieldSchema{Type: "number"}, "1.5", float64(1.5)},
		{definitionmodel.FieldSchema{Type: "number"}, "bad", "bad"},
		{definitionmodel.FieldSchema{Type: "currency"}, "1.5", "1.50"},
		{definitionmodel.FieldSchema{Type: "currency"}, "bad", "bad"},
		{definitionmodel.FieldSchema{Type: "currency", Config: map[string]any{"scale": -1}}, "1.5", "1.5"},
		{definitionmodel.FieldSchema{Type: "percent", Config: map[string]any{"precision": 8, "scale": 4, "rounding_mode": "half_even"}}, "1.23456", "1.2346"},
		{definitionmodel.FieldSchema{Type: "percent", Config: map[string]any{"precision": 20, "scale": 4, "rounding_mode": "half_even"}}, "9007199254740993.0000", "9007199254740993.0000"},
		{definitionmodel.FieldSchema{Type: "boolean"}, "yes", true},
		{definitionmodel.FieldSchema{Type: "boolean"}, "no", false},
		{definitionmodel.FieldSchema{Type: "text"}, "x", "x"},
	} {
		got, issue := RecordCoerceImportValue("o", tc.field, tc.input)
		if issue != "" || got != tc.want {
			t.Fatalf("%#v => %#v %s", tc, got, issue)
		}
	}

	for _, value := range []any{
		[]appschemamodel.DictionaryItemSchema{{Key: "a"}},
		[]string{" a ", ""},
		[]any{map[string]any{"key": "a"}, "b"},
		"invalid",
	} {
		_ = dictionaryItemsFromAny(value)
	}
	for _, config := range []map[string]any{
		{"valueDomain": map[string]any{"options": []string{"a"}}},
		{"domain": map[string]any{"items": []string{"a"}}},
		{"value_domain_items": []string{"a"}},
		{"valueDomainItems": []string{"a"}},
		{"options": []string{"a"}},
	} {
		if len(importValueDomainItems(definitionmodel.FieldSchema{Config: config})) != 1 {
			t.Fatalf("%#v", config)
		}
	}
	optionField := definitionmodel.FieldSchema{Config: map[string]any{}, Validation: definitionmodel.FieldValidation{Options: []string{" a ", ""}}}
	if len(importValueDomainItems(optionField)) != 1 {
		t.Fatal("validation options")
	}
	if importValueDomainItems(definitionmodel.FieldSchema{}) != nil || importValueDomainI18nAliases("o", "f", "") != nil {
		t.Fatal("nil domain")
	}
	if got := normalizeImportValueDomainText(" A_B-C "); got != "abc" {
		t.Fatalf("%q", got)
	}
}

func TestRecordImportStructuredFieldsUseJSONAndValueDomains(t *testing.T) {
	multi := definitionmodel.FieldSchema{
		Key: "tags", Type: recordmodel.RecordMultiSelectFieldType,
		Options: []any{map[string]any{"value": "red", "label": "Red"}, map[string]any{"value": "blue", "label": "Blue"}},
	}
	got, issue := RecordCoerceImportValue("item", multi, `["Blue","red","Blue"]`)
	if issue != "" || !reflect.DeepEqual(got, []string{"blue", "red"}) {
		t.Fatalf("multi import=%#v issue=%q", got, issue)
	}
	if got, issue := RecordCoerceImportValue("item", multi, `["unknown"]`); got != nil || issue != "backend.import.invalid_value_domain_option" {
		t.Fatalf("invalid multi=%#v issue=%q", got, issue)
	}
	jsonField := definitionmodel.FieldSchema{Key: "payload", Type: recordmodel.RecordJSONFieldType}
	got, issue = RecordCoerceImportValue("item", jsonField, `{"count":9007199254740993}`)
	object, ok := got.(map[string]any)
	if issue != "" || !ok || fmt.Sprint(object["count"]) != "9007199254740993" {
		t.Fatalf("json import=%#v issue=%q", got, issue)
	}
	if got, issue := RecordCoerceImportValue("item", jsonField, `[1]`); got != nil || issue != "backend.import.invalid_json" {
		t.Fatalf("invalid json=%#v issue=%q", got, issue)
	}
}

func TestRecordImportAnyConversions(t *testing.T) {
	if got := importMapFromAny(nil); len(got) != 0 {
		t.Fatalf("%#v", got)
	}
	source := map[string]any{"x": map[string]any{"y": 1}}
	got := importMapFromAny(source)
	got["new"] = 2
	if _, exists := source["new"]; exists {
		t.Fatal("map was not cloned")
	}
	if got := importMapFromAny(struct {
		X string `json:"x"`
	}{X: "a"}); got["x"] != "a" {
		t.Fatalf("%#v", got)
	}
	if got := importMapFromAny(func() {}); len(got) != 0 {
		t.Fatalf("%#v", got)
	}
	for _, tc := range []struct {
		value any
		want  int
	}{{[]string{"a"}, 1}, {[]any{" a ", nil, ""}, 1}, {" a ", 1}, {42, 0}} {
		if got := importStringListFromAny(tc.value); len(got) != tc.want {
			t.Fatalf("%#v => %#v", tc.value, got)
		}
	}
}

func TestRecordImportRemainingStatements(t *testing.T) {
	field := definitionmodel.FieldSchema{Key: "stage", Type: "select", Config: map[string]any{"options": []any{map[string]any{"key": "open"}, map[string]any{}}}}
	if value, ok := importValueDomainValue("o", field, " "); ok || value != "" {
		t.Fatalf("%q %v", value, ok)
	}
	if value, ok := importValueDomainValue("o", field, "open"); !ok || value != "open" {
		t.Fatalf("%q %v", value, ok)
	}
	if issue := importValueDomainIssue(field, "unknown"); issue != "backend.import.invalid_value_domain_option" {
		t.Fatalf("%s", issue)
	}
	emptyDomain := definitionmodel.FieldSchema{Key: "stage", Type: "select", Config: map[string]any{"options": []any{map[string]any{}}}}
	if issue := importValueDomainIssue(emptyDomain, "unknown"); issue != "backend.import.invalid_value_domain" {
		t.Fatalf("%s", issue)
	}
	candidates := importValueDomainCandidates(definitionmodel.FieldSchema{Config: map[string]any{"options": []any{map[string]any{"key": "a"}, map[string]any{"value": "b"}, map[string]any{"value": "b"}, map[string]any{}}}})
	if len(candidates) != 2 {
		t.Fatalf("%#v", candidates)
	}
	if got := importValueDomainItems(definitionmodel.FieldSchema{Config: map[string]any{}}); got != nil {
		t.Fatalf("%#v", got)
	}
	if got := importMapFromAny([]string{"not", "object"}); len(got) != 0 {
		t.Fatalf("%#v", got)
	}
	if importValueDomainString(nil) != "" || importValueDomainString("<nil>") != "" || importValueDomainString(" x ") != "x" {
		t.Fatal("domain string")
	}
	originalLookup := importValueDomainLookup
	t.Cleanup(func() { importValueDomainLookup = originalLookup })
	importValueDomainLookup = func(locale, key string) (string, bool) {
		if strings.HasSuffix(key, ".empty") {
			return "", true
		}
		if strings.HasSuffix(key, ".open") {
			return " Open Localized ", true
		}
		return "", false
	}
	aliases := importValueDomainI18nAliases("order", "status", "open")
	if len(aliases) != 1 || aliases[0] != "Open Localized" {
		t.Fatalf("%#v", aliases)
	}
	localizedField := definitionmodel.FieldSchema{Key: "status", Type: "select", Config: map[string]any{"options": []string{"open"}}}
	if value, ok := importValueDomainValue("order", localizedField, "Open Localized"); !ok || value != "open" {
		t.Fatalf("%q %v", value, ok)
	}
	_, _ = importValueDomainValue("order", localizedField, "Different")
	_ = importValueDomainI18nAliases("order", "status", "empty")
}
