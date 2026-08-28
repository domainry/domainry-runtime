// Report domain service tests.
package service

import (
	"errors"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"

	"context"
	"testing"
)

type accessStub struct {
	object         definitionmodel.ObjectSchema
	err            error
	deniedRecordID string
}

func (s accessStub) ReportObjectForAction(context.Context, principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
	return s.object, s.err
}
func (accessStub) NormalizeReportListQuery(_ context.Context, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, _ principalmodel.Principal) recordmodel.RecordListQuery {
	return query
}
func (s accessStub) CanAccessReportRecord(_ context.Context, _ principalmodel.Principal, _ definitionmodel.ObjectSchema, record recordmodel.Record) bool {
	return record.ID != s.deniedRecordID
}

type recordRepositoryStub struct {
	records []recordmodel.Record
	err     error
}

func (s recordRepositoryStub) ListReportRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	return recordmodel.RecordPageResult{Items: s.records}, s.err
}

func TestServiceSummaryDoesNotRequireRuntimeServices(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "revenue", Type: "currency"}}}
	repository := recordRepositoryStub{records: []recordmodel.Record{{ID: "customer-1", Data: map[string]any{"revenue": 42}}}}
	service := NewReportDomainService(ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{{Key: "revenue", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "customer", Alias: "customer"}, Measures: []reportmodel.ReportDatasetMeasure{{Key: "revenue", Operation: "sum", Field: reportField("customer", "revenue")}}}}}
		},
		Access: accessStub{object: object}, Records: repository,
	})

	summary, err := service.Summary(t.Context(), "revenue", principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}})
	if err != nil {
		t.Fatal(err)
	}
	if summary.SourceRowCount != 1 || summary.RowCount != 1 || summary.Rows[0].Measures["revenue"] != "42.00" {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}

func TestReportServiceSummaryAndExportFailurePaths(t *testing.T) {
	report := reportmodel.ReportSchema{Key: "revenue", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "customer", Alias: "customer"}, Joins: []reportmodel.ReportDatasetJoin{{ObjectKey: "order", Alias: "order", Type: "inner", LeftAlias: "customer", LeftField: "id", RightField: "id", Cardinality: "many_to_one"}}}}
	reports := func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
		return []reportmodel.ReportSchema{report}
	}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}}

	var nilService *ReportDomainService
	if _, err := nilService.Summary(t.Context(), "revenue", principal); reportErrorCode(err) != "backend.report.not_found" {
		t.Fatalf("nil service summary error=%v", err)
	}
	withoutReports := NewReportDomainService(ReportDependencies{})
	if _, err := withoutReports.ReportForExport(t.Context(), "revenue", "customer", principal); reportErrorCode(err) != "backend.report.not_found" {
		t.Fatalf("nil reports export error=%v", err)
	}

	accessErr := errors.New("object denied")
	service := NewReportDomainService(ReportDependencies{Reports: reports, Access: accessStub{err: accessErr}, Records: recordRepositoryStub{}})
	if _, err := service.Summary(t.Context(), "revenue", principal); !errors.Is(err, accessErr) {
		t.Fatalf("object access error=%v", err)
	}

	recordErr := errors.New("records unavailable")
	service = NewReportDomainService(ReportDependencies{Reports: reports, Access: accessStub{object: definitionmodel.ObjectSchema{Key: "customer"}}, Records: recordRepositoryStub{err: recordErr}})
	if _, err := service.Summary(t.Context(), "revenue", principal); reportErrorCode(err) != "backend.internal_error" || !errors.Is(err, recordErr) {
		t.Fatalf("record read error=%v", err)
	}

	if _, err := service.ReportForExport(t.Context(), "missing", "customer", principal); reportErrorCode(err) != "backend.report.not_found" {
		t.Fatalf("missing export report error=%v", err)
	}
	if _, err := service.ReportForExport(t.Context(), "revenue", "missing", principal); reportErrorCode(err) != "backend.report.object_not_in_report" {
		t.Fatalf("foreign export object error=%v", err)
	}
	if exported, err := service.ReportForExport(t.Context(), " revenue ", " order ", principal); err != nil || exported.Key != "revenue" {
		t.Fatalf("exported report=%#v err=%v", exported, err)
	}
	if objects := reportUniqueSourceObjects(report); len(objects) != 2 || objects[0] != "customer" || objects[1] != "order" {
		t.Fatalf("unique source objects=%#v", objects)
	}
}

func TestReportServiceFiltersInaccessibleRecordsAndAggregatesSelectedMetrics(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{
		{Key: "float64", Type: "number"}, {Key: "float32", Type: "currency"}, {Key: "int", Type: "number"},
		{Key: "int64", Type: "number"}, {Key: "numeric_text", Type: "number"}, {Key: "invalid_number", Type: "number"},
		{Key: "status", Type: "status"}, {Key: "empty_status", Type: "select"}, {Key: "nil_status", Type: "select"},
		{Key: "excluded", Type: "number"}, {Key: "ignored", Type: "text"},
	}}
	report := reportmodel.ReportSchema{Key: "metrics", Dataset: reportmodel.ReportDatasetSchema{
		Source:     reportmodel.ReportDatasetSource{ObjectKey: "customer", Alias: "customer"},
		Dimensions: []reportmodel.ReportDatasetDimension{{Key: "status", Field: *reportField("customer", "status")}, {Key: "empty_status", Field: *reportField("customer", "empty_status")}, {Key: "nil_status", Field: *reportField("customer", "nil_status")}},
		Measures: []reportmodel.ReportDatasetMeasure{
			{Key: "float64", Operation: "sum", Field: reportField("customer", "float64")}, {Key: "float32", Operation: "sum", Field: reportField("customer", "float32")},
			{Key: "int", Operation: "sum", Field: reportField("customer", "int")}, {Key: "int64", Operation: "sum", Field: reportField("customer", "int64")},
			{Key: "numeric_text", Operation: "sum", Field: reportField("customer", "numeric_text")},
		},
	}}
	records := []recordmodel.Record{
		{ID: "visible", Data: map[string]any{"float64": float64(1.5), "float32": "2.50", "int": 3, "int64": int64(4), "numeric_text": "5.5", "invalid_number": "bad", "status": " active ", "empty_status": " ", "nil_status": nil, "excluded": 99}},
		{ID: "visible-again", Data: map[string]any{"status": "active"}},
		{ID: "denied", Data: map[string]any{"float64": 100.0, "status": "hidden"}},
	}
	service := NewReportDomainService(ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{report}
		},
		Access: accessStub{object: object, deniedRecordID: "denied"}, Records: recordRepositoryStub{records: records},
	})
	summary, err := service.Summary(t.Context(), "metrics", principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}})
	if err != nil {
		t.Fatal(err)
	}
	if summary.SourceRowCount != 2 || summary.RowCount != 1 || summary.Rows[0].Dimensions["status"] != "active" || summary.Rows[0].Measures["float64"] != "1.5" || summary.Rows[0].Measures["numeric_text"] != "5.5" {
		t.Fatalf("summary=%+v", summary)
	}
	if _, found := summary.Rows[0].Measures["excluded"]; found {
		t.Fatalf("excluded metric was aggregated: %+v", summary.Rows[0].Measures)
	}
}

func TestReportMetricHelpersCoverAllNumericAndFieldModes(t *testing.T) {
	for name, test := range map[string]struct {
		value any
		want  string
		ok    bool
	}{
		"float64": {value: float64(1.5), want: "1.5", ok: true},
		"float32": {value: float32(2.5), want: "2.5", ok: true},
		"int":     {value: 3, want: "3", ok: true},
		"int64":   {value: int64(4), want: "4", ok: true},
		"string":  {value: "5.5", want: "5.5", ok: true},
		"invalid": {value: "bad"},
		"other":   {value: true},
	} {
		t.Run(name, func(t *testing.T) {
			got, _, err := reportNumericValue(definitionmodel.FieldSchema{Type: "number"}, test.value)
			if (test.ok && got.String() != test.want) || (err == nil) != test.ok {
				t.Fatalf("numeric value=(%v,%v) want=(%v,%v)", got, err, test.want, test.ok)
			}
		})
	}
	if got := reportStableValue(nil); got != "" {
		t.Fatalf("nil report text=%q", got)
	}
}

func reportField(alias, fieldKey string) *reportmodel.ReportDatasetField {
	return &reportmodel.ReportDatasetField{SourceAlias: alias, FieldKey: fieldKey}
}

func reportErrorCode(err error) string {
	if coded, ok := err.(interface{ ErrorCode() string }); ok {
		return coded.ErrorCode()
	}
	return ""
}
