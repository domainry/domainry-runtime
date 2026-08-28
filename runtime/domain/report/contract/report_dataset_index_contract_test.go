package contract

import (
	"reflect"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

func TestReportDatasetIndexDiagnosticsCoverJoinFilterTimeAndAnalysis(t *testing.T) {
	report := reportmodel.ReportSchema{Key: "operations", Dataset: reportmodel.ReportDatasetSchema{
		Source:     reportmodel.ReportDatasetSource{ObjectKey: "account", Alias: "accounts"},
		Joins:      []reportmodel.ReportDatasetJoin{{ObjectKey: "event", Alias: "events", LeftAlias: "accounts", LeftField: "external_key", RightField: "account_id"}},
		Filters:    []reportmodel.ReportDatasetFilter{{Field: reportmodel.ReportDatasetField{SourceAlias: "events", FieldKey: "status"}}},
		Dimensions: []reportmodel.ReportDatasetDimension{{Key: "day", Field: reportmodel.ReportDatasetField{SourceAlias: "events", FieldKey: "occurred_at"}, TimeGrain: "day"}},
		Analyses:   []reportmodel.ReportDatasetAnalysis{{Key: "flow", Type: "funnel", EntityField: reportmodel.ReportDatasetField{SourceAlias: "events", FieldKey: "actor_id"}, EventField: &reportmodel.ReportDatasetField{SourceAlias: "events", FieldKey: "event_kind"}, TimeField: reportmodel.ReportDatasetField{SourceAlias: "events", FieldKey: "occurred_at"}}},
	}}
	objects := map[string]definitionmodel.ObjectSchema{
		"account": {Key: "account", Fields: []definitionmodel.FieldSchema{{Key: "external_key", Type: "text"}}},
		"event": {Key: "event", Fields: []definitionmodel.FieldSchema{
			{Key: "account_id", Type: "relation"},
			{Key: "status", Type: "select"}, {Key: "occurred_at", Type: "datetime"},
			{Key: "actor_id", Type: "relation", Config: map[string]any{"indexed": false}},
			{Key: "event_kind", Type: "select", Config: map[string]any{"indexed": true}},
		}},
	}
	diagnostics := ReportDatasetIndexDiagnostics(report, objects)
	actual := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		if diagnostic.Code != ReportRequiredIndexMissingCode || !reflect.DeepEqual(diagnostic.Fields, []string{"workspace_id", diagnostic.FieldKey}) {
			t.Fatalf("diagnostic=%#v", diagnostic)
		}
		actual = append(actual, diagnostic.ObjectKey+"."+diagnostic.FieldKey+":"+diagnostic.Usage)
	}
	want := []string{"event.actor_id:analysis_entity", "event.occurred_at:analysis_time", "event.occurred_at:time_bucket", "event.status:filter", "account.external_key:join"}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("diagnostics=%#v want=%#v", actual, want)
	}
}

func TestReportDatasetIndexDiagnosticsAcceptUniqueAndExplicitIndexes(t *testing.T) {
	report := reportmodel.ReportSchema{Dataset: reportmodel.ReportDatasetSchema{
		Source:     reportmodel.ReportDatasetSource{ObjectKey: "event", Alias: "events"},
		Filters:    []reportmodel.ReportDatasetFilter{{Field: reportmodel.ReportDatasetField{SourceAlias: "events", FieldKey: "status"}}},
		Dimensions: []reportmodel.ReportDatasetDimension{{Key: "day", Field: reportmodel.ReportDatasetField{SourceAlias: "events", FieldKey: "occurred_at"}, TimeGrain: "day"}},
	}}
	objects := map[string]definitionmodel.ObjectSchema{"event": {Key: "event", Fields: []definitionmodel.FieldSchema{
		{Key: "status", Type: "select", Unique: true}, {Key: "occurred_at", Type: "datetime", Config: map[string]any{"indexed": "true"}},
	}}}
	if diagnostics := ReportDatasetIndexDiagnostics(report, objects); len(diagnostics) != 0 {
		t.Fatalf("diagnostics=%#v", diagnostics)
	}
	event := objects["event"]
	event.Fields = append(event.Fields, definitionmodel.FieldSchema{Key: "unsupported_index", Type: "text", Config: map[string]any{"indexed": 1}})
	objects["event"] = event
	report.Dataset.Filters = append(report.Dataset.Filters, reportmodel.ReportDatasetFilter{Field: reportmodel.ReportDatasetField{SourceAlias: "events", FieldKey: "unsupported_index"}})
	if diagnostics := ReportDatasetIndexDiagnostics(report, objects); len(diagnostics) != 1 || diagnostics[0].FieldKey != "unsupported_index" {
		t.Fatalf("unsupported index diagnostics=%#v", diagnostics)
	}
}

func TestReportDatasetIndexDiagnosticsCoversIgnoredAndDeduplicatedReferences(t *testing.T) {
	report := reportmodel.ReportSchema{Dataset: reportmodel.ReportDatasetSchema{
		Source: reportmodel.ReportDatasetSource{ObjectKey: "event", Alias: "events"},
		Joins:  []reportmodel.ReportDatasetJoin{{ObjectKey: "missing", Alias: "missing"}},
		Filters: []reportmodel.ReportDatasetFilter{
			{Field: reportmodel.ReportDatasetField{SourceAlias: "unknown", FieldKey: "status"}},
			{Field: reportmodel.ReportDatasetField{SourceAlias: "events", FieldKey: ""}},
			{Field: reportmodel.ReportDatasetField{SourceAlias: "events", FieldKey: "id"}},
			{Field: reportmodel.ReportDatasetField{SourceAlias: "events", FieldKey: "created_at"}},
			{Field: reportmodel.ReportDatasetField{SourceAlias: "events", FieldKey: "updated_at"}},
			{Field: reportmodel.ReportDatasetField{SourceAlias: "missing", FieldKey: "status"}},
			{Field: reportmodel.ReportDatasetField{SourceAlias: "events", FieldKey: "absent"}},
			{Field: reportmodel.ReportDatasetField{SourceAlias: "events", FieldKey: "status"}},
			{Field: reportmodel.ReportDatasetField{SourceAlias: "events", FieldKey: "status"}},
		},
		Dimensions: []reportmodel.ReportDatasetDimension{
			{Key: "plain", Field: reportmodel.ReportDatasetField{SourceAlias: "events", FieldKey: "status"}},
			{Key: "day", Field: reportmodel.ReportDatasetField{SourceAlias: "events", FieldKey: "status"}, TimeGrain: "day"},
		},
		Analyses: []reportmodel.ReportDatasetAnalysis{{Key: "analysis", EntityField: reportmodel.ReportDatasetField{SourceAlias: "events", FieldKey: "status"}, TimeField: reportmodel.ReportDatasetField{SourceAlias: "events", FieldKey: "status"}}},
	}}
	objects := map[string]definitionmodel.ObjectSchema{"event": {Key: "event", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}}}}
	diagnostics := ReportDatasetIndexDiagnostics(report, objects)
	if len(diagnostics) != 4 {
		t.Fatalf("diagnostics=%#v", diagnostics)
	}
}
