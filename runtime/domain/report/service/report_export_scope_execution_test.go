package service

import (
	"testing"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

func TestReportExportRuntimeQueryAndTagsNarrowAndDeduplicateRows(t *testing.T) {
	row := func(orderID, orderNo, customer, assignmentID, definitionID, stableKey string, active bool) reportDatasetRow {
		return reportDatasetRow{
			"orders":                 &recordmodel.Record{ID: orderID, Data: map[string]any{"order_no": orderNo, "customer_name": customer}},
			"export_tags":            &recordmodel.Record{ID: assignmentID, Data: map[string]any{"target_id": orderID, "tag_definition_id": definitionID, "active": active}},
			"export_tag_definitions": &recordmodel.Record{ID: definitionID, Data: map[string]any{"stable_key": stableKey}},
		}
	}
	rows := []reportDatasetRow{
		row("order-1", "PT-188", "Acme Import", "a-1", "priority-v1-historical", "priority", true),
		row("order-1", "PT-188", "Acme Import", "a-2", "customer-v1-historical", "customer", true),
		row("order-1", "PT-188", "Acme Import", "a-3", "priority-v1-historical", "priority", true),
		row("order-2", "PT-200", "Other", "a-4", "priority-v2-current", "priority", true),
		row("order-3", "PT-300", "Acme Other", "a-5", "customer-v2-current", "customer", true),
	}
	query := &reportmodel.ReportDatasetRuntimeQuery{Mode: "any", Value: "  acME ", Predicates: []reportmodel.ReportExportQueryPredicate{
		{Field: reportmodel.ReportDatasetField{SourceAlias: "orders", FieldKey: "order_no"}, Operator: "contains"},
		{Field: reportmodel.ReportDatasetField{SourceAlias: "orders", FieldKey: "customer_name"}, Operator: "starts_with"},
	}}
	rows = reportApplyRuntimeQuery(rows, query)
	if len(rows) != 4 {
		t.Fatalf("query rows=%d", len(rows))
	}
	tags := &reportmodel.ReportDatasetRuntimeTags{
		ScopeJoinAliases: []string{"export_tags", "export_tag_definitions"}, TargetField: reportmodel.ReportDatasetField{SourceAlias: "orders", FieldKey: "id"},
		TagField: reportmodel.ReportDatasetField{SourceAlias: "export_tag_definitions", FieldKey: "stable_key"}, Values: []string{"customer", "priority"}, Match: "all",
	}
	rows = reportApplyRuntimeTags(rows, tags)
	if len(rows) != 1 || rows[0]["orders"].ID != "order-1" || rows[0]["export_tags"] != nil || rows[0]["export_tag_definitions"] != nil {
		t.Fatalf("tag rows=%#v", rows)
	}

	tags.Match = "any"
	anyRows := reportApplyRuntimeTags([]reportDatasetRow{
		row("order-1", "PT-188", "Acme Import", "a-1", "priority-v1-historical", "priority", true),
		row("order-1", "PT-188", "Acme Import", "a-2", "customer-v1-historical", "customer", true),
		row("order-2", "PT-200", "Other", "a-3", "priority-v2-current", "priority", true),
	}, tags)
	if len(anyRows) != 2 {
		t.Fatalf("any tag rows=%d", len(anyRows))
	}
}
