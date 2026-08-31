package appschema

import (
	"archive/zip"
	"bytes"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	"strings"
	"testing"
)

func TestLocalizedTextCSVExportsStableKeysAndText(t *testing.T) {
	payload := localizedTextCSV([]appschemamodel.LocalizedText{
		{
			WorkspaceID: "workspace-primary",
			EntityType:  "field",
			EntityKey:   "customer.status.active",
			Property:    "label",
			Locale:      "zh-CN",
			Text:        "活跃",
		},
	})
	got := string(payload)
	want := strings.Join([]string{
		"workspace_id,entity_type,entity_key,property,locale,text",
		"workspace-primary,field,customer.status.active,label,zh-CN,活跃",
		"",
	}, "\n")
	if got != want {
		t.Fatalf("csv mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestLocalizedTextXLSXExportsStableKeysAndText(t *testing.T) {
	payload := localizedTextXLSX([]appschemamodel.LocalizedText{
		{
			WorkspaceID: "workspace-primary",
			EntityType:  "field",
			EntityKey:   "customer.status.active",
			Property:    "label",
			Locale:      "zh-CN",
			Text:        "活跃",
		},
	})
	reader, err := zip.NewReader(bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatalf("open xlsx zip: %v", err)
	}
	var worksheet string
	for _, file := range reader.File {
		if file.Name != "xl/worksheets/sheet1.xml" {
			continue
		}
		handle, err := file.Open()
		if err != nil {
			t.Fatalf("open worksheet: %v", err)
		}
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(handle); err != nil {
			t.Fatalf("read worksheet: %v", err)
		}
		_ = handle.Close()
		worksheet = buf.String()
	}
	if !strings.Contains(worksheet, "customer.status.active") || !strings.Contains(worksheet, "活跃") {
		t.Fatalf("worksheet missing stable key/text: %s", worksheet)
	}
}

func TestLocalizedTextCSVImportUsesHeaderOrderAndRejectsLocalizedKeys(t *testing.T) {
	input := strings.Join([]string{
		"text,locale,property,entity_key,entity_type,workspace_id",
		"Active,en-US,label,customer.status.active,field,workspace-primary",
	}, "\n")
	requests, err := localizedTextUpsertRequestsFromCSV(bytes.NewBufferString(input))
	if err != nil {
		t.Fatalf("parse localized text csv: %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("expected one request, got %d", len(requests))
	}
	req := requests[0]
	if req.EntityType != "field" || req.EntityKey != "customer.status.active" || req.Property != "label" || req.Locale != "en-US" || req.Text != "Active" {
		t.Fatalf("unexpected parsed request: %+v", req)
	}
	if req.SourceKind != "user" || req.SourceID != "metadata_csv_import" {
		t.Fatalf("expected csv import source, got %+v", req)
	}
}

func TestLocalizedTextCSVImportRequiresStableColumns(t *testing.T) {
	input := strings.Join([]string{
		"workspace_id,entity_type,entity_key,property,locale,text",
		"workspace-primary,field,,label,zh-CN,活跃",
	}, "\n")
	if _, err := localizedTextUpsertRequestsFromCSV(bytes.NewBufferString(input)); err == nil {
		t.Fatal("expected missing entity key to fail")
	}
}

func TestLocalizedTextCSVImportValidatesSchemaKeys(t *testing.T) {
	requests := []appschemamodel.LocalizedTextUpsertRequest{
		{EntityType: "field", EntityKey: "customer.status", Property: "name", Locale: "en-US", Text: "Status"},
		{EntityType: "menu", EntityKey: "lead_workbench", Property: "label", Locale: "en-US", Text: "Lead Workbench"},
	}
	allowed := map[string]bool{
		localizedTextImportKey("field", "customer.status", "name"): true,
		localizedTextImportKey("menu", "lead_workbench", "label"):  true,
	}
	if err := validateLocalizedTextUpsertRequests(requests, allowed); err != nil {
		t.Fatalf("expected valid localized text requests, got %v", err)
	}
	requests[1].EntityKey = "missing_menu"
	if err := validateLocalizedTextUpsertRequests(requests, allowed); err == nil {
		t.Fatal("expected unknown menu key to fail")
	}
}

func TestLocalizedTextCSVImportStructuralFailures(t *testing.T) {
	if _, err := localizedTextUpsertRequestsFromCSV(strings.NewReader(`"unterminated`)); err == nil {
		t.Fatal("expected malformed csv")
	}
	if _, err := localizedTextUpsertRequestsFromCSV(strings.NewReader("")); err == nil {
		t.Fatal("expected empty csv")
	}
	if _, err := localizedTextUpsertRequestsFromCSV(strings.NewReader("workspace_id,entity_type")); err == nil {
		t.Fatal("expected missing columns")
	}
	short := "workspace_id,entity_type,entity_key,property,locale,text\nworkspace-primary,field,key,label,en-US\n"
	if _, err := localizedTextUpsertRequestsFromCSV(strings.NewReader(short)); err == nil {
		t.Fatal("expected short row")
	}
}

func TestLocalizedTextCSVImportRequiresEachValue(t *testing.T) {
	header := []string{"workspace-primary", "field", "key", "label", "en-US", "Text"}
	for index := 1; index < len(header); index++ {
		values := append([]string(nil), header...)
		values[index] = ""
		input := "workspace_id,entity_type,entity_key,property,locale,text\n" + strings.Join(values, ",") + "\n"
		if _, err := localizedTextUpsertRequestsFromCSV(strings.NewReader(input)); err == nil {
			t.Fatalf("expected index %d to fail", index)
		}
	}
	if err := validateLocalizedTextUpsertRequests([]appschemamodel.LocalizedTextUpsertRequest{}, map[string]bool{}); err == nil {
		t.Fatal("expected empty allowed catalog")
	}
	if spreadsheetColumnName(0) != "A" || spreadsheetColumnName(27) != "AA" {
		t.Fatal("spreadsheet column conversion failed")
	}
}
