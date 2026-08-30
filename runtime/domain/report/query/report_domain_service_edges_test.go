package query

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

type reportEdgeAccess struct {
	objects   map[string]definitionmodel.ObjectSchema
	objectErr error
	deniedID  string
}

type reportExportFieldAccess struct {
	*reportEdgeAccess
	masked bool
	err    error
}

func (a *reportExportFieldAccess) AuthorizeReportExportField(context.Context, principalmodel.Principal, string, string) (bool, error) {
	return a.masked, a.err
}

func (a *reportEdgeAccess) ReportObjectForAction(_ context.Context, _ principalmodel.Principal, objectKey, _ string) (definitionmodel.ObjectSchema, error) {
	if a.objectErr != nil {
		return definitionmodel.ObjectSchema{}, a.objectErr
	}
	return a.objects[objectKey], nil
}

func (*reportEdgeAccess) NormalizeReportListQuery(_ context.Context, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, _ principalmodel.Principal) recordmodel.RecordListQuery {
	query.PageSize = 25
	return query
}

func (a *reportEdgeAccess) CanAccessReportRecord(_ context.Context, _ principalmodel.Principal, _ definitionmodel.ObjectSchema, record recordmodel.Record) bool {
	return record.ID != a.deniedID
}

type reportEdgeRecords struct {
	items []recordmodel.Record
	err   error
	query recordmodel.RecordListQuery
}

func (r *reportEdgeRecords) ListReportRecords(_ context.Context, _ string, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	r.query = query
	return recordmodel.RecordPageResult{Items: r.items}, r.err
}

func TestReportSummaryCoversFailuresAccessFilteringAndMetrics(t *testing.T) {
	report := reportmodel.ReportSchema{
		Key: "sales", Name: "Sales",
		Dataset: reportmodel.ReportDatasetSchema{
			Source:     reportmodel.ReportDatasetSource{ObjectKey: "customer", Alias: "customer"},
			Joins:      []reportmodel.ReportDatasetJoin{{ObjectKey: "order", Alias: "order", LeftAlias: "customer", LeftField: "id", RightField: "id", Cardinality: "one_to_one", Type: "inner"}},
			Dimensions: []reportmodel.ReportDatasetDimension{{Key: "status", Field: *reportField("customer", "status")}},
			Measures: []reportmodel.ReportDatasetMeasure{
				{Key: "revenue", Operation: "sum", Field: reportField("customer", "revenue")},
				{Key: "total", Operation: "sum", Field: reportField("order", "total")},
			},
		},
	}
	access := &reportEdgeAccess{objects: map[string]definitionmodel.ObjectSchema{
		"customer": {Key: "customer", Fields: []definitionmodel.FieldSchema{
			{Key: "revenue", Type: "currency"},
			{Key: "status", Type: "status"},
			{Key: "ignored", Type: "number"},
		}},
		"order": {Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "total", Type: "number"}}},
	}, deniedID: "denied"}
	records := &reportEdgeRecords{items: []recordmodel.Record{
		{ID: "first", Data: map[string]any{"revenue": "12.5", "status": "open", "ignored": 99, "total": float32(4)}},
		{ID: "second", Data: map[string]any{"status": "", "total": int64(3)}},
		{ID: "nil-status", Data: map[string]any{"revenue": "2.50", "status": nil, "total": 2}},
		{ID: "repeated-status", Data: map[string]any{"status": "open", "total": 1}},
		{ID: "denied", Data: map[string]any{"revenue": 1000, "status": "closed", "total": 1000}},
	}}
	service := NewReportDomainService(ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{report}
		},
		Access: access, Records: records,
	})

	if _, err := service.Summary(t.Context(), "missing", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.report.not_found" {
		t.Fatalf("missing report error=%v", err)
	}
	want := errors.New("object unavailable")
	access.objectErr = want
	if _, err := service.Summary(t.Context(), "sales", principalmodel.Principal{}); !errors.Is(err, want) {
		t.Fatalf("object lookup error=%v", err)
	}
	access.objectErr = nil
	records.err = errors.New("records unavailable")
	if _, err := service.Summary(t.Context(), "sales", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.internal_error" || !errors.Is(err, records.err) {
		t.Fatalf("record list error=%v", err)
	}
	records.err = nil

	summary, err := service.Summary(t.Context(), "sales", principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}})
	if err != nil {
		t.Fatal(err)
	}
	if summary.SourceRowCount != 4 || summary.RowCount != 2 || records.query.PageSize != 25 {
		t.Fatalf("summary=%#v query=%#v", summary, records.query)
	}
	rows := map[string]reportmodel.ReportResultRow{}
	for _, row := range summary.Rows {
		rows[row.Dimensions["status"]] = row
	}
	if rows["open"].Measures["revenue"] != "12.50" || rows["open"].Measures["total"] != "5" || rows[""].Measures["revenue"] != "2.50" || rows[""].Measures["total"] != "5" {
		t.Fatalf("rows=%#v", rows)
	}
}

func TestReportExportLookupAndHelperBoundaries(t *testing.T) {
	report := reportmodel.ReportSchema{Key: "report", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "customer", Alias: "customer"}, Joins: []reportmodel.ReportDatasetJoin{{ObjectKey: "order", Alias: "order", LeftAlias: "customer", Cardinality: "one_to_one"}}}}
	service := NewReportDomainService(ReportDependencies{Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
		return []reportmodel.ReportSchema{report}
	}})
	if _, err := service.ReportForExport(t.Context(), "missing", "customer", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.report.not_found" {
		t.Fatalf("missing export report error=%v", err)
	}
	if _, err := service.ReportForExport(t.Context(), "report", "invoice", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.report.object_not_in_report" {
		t.Fatalf("invalid export object error=%v", err)
	}
	if got, err := service.ReportForExport(t.Context(), " report ", " order ", principalmodel.Principal{}); err != nil || got.Key != "report" {
		t.Fatalf("snapshot export report=%#v err=%v", got, err)
	}

	var nilService *ReportDomainService
	if _, ok := nilService.reportForPrincipal(t.Context(), "report", principalmodel.Principal{}); ok {
		t.Fatal("nil service returned report")
	}
	if _, ok := NewReportDomainService(ReportDependencies{}).reportForPrincipal(t.Context(), "report", principalmodel.Principal{}); ok {
		t.Fatal("nil report provider returned report")
	}

	if objects := reportUniqueSourceObjects(reportmodel.ReportSchema{Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "customer", Alias: "customer"}, Joins: []reportmodel.ReportDatasetJoin{{ObjectKey: "order", Alias: "order"}, {ObjectKey: "customer", Alias: "duplicate"}}}}); len(objects) != 2 || objects[0] != "customer" || objects[1] != "order" {
		t.Fatalf("unique objects=%#v", objects)
	}
	if objects := reportUniqueSourceObjects(reportmodel.ReportSchema{Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: " "}}}); len(objects) != 0 {
		t.Fatalf("blank objects=%#v", objects)
	}
	for _, test := range []struct {
		value any
		want  string
		ok    bool
	}{{float64(1.5), "1.5", true}, {float32(2.5), "2.5", true}, {3, "3", true}, {int64(4), "4", true}, {"5.5", "5.5", true}, {"bad", "", false}, {nil, "", false}} {
		got, _, err := reportNumericValue(definitionmodel.FieldSchema{Type: "number"}, test.value)
		if (test.ok && got.String() != test.want) || (err == nil) != test.ok {
			t.Fatalf("numeric(%#v)=(%v,%v) want=(%v,%v)", test.value, got, err, test.want, test.ok)
		}
	}
	if _, _, err := reportNumericValue(definitionmodel.FieldSchema{Type: "currency", Config: map[string]any{"scale": -1}}, "1"); err == nil {
		t.Fatal("invalid currency config was aggregated")
	}
	if _, _, err := reportNumericValue(definitionmodel.FieldSchema{Type: "currency", Config: map[string]any{"precision": 2, "scale": 0}}, "123"); err == nil {
		t.Fatal("overflowing currency was aggregated")
	}
	for _, binaryFloat := range []any{float32(0.1), float64(0.1)} {
		if _, _, err := reportNumericValue(definitionmodel.FieldSchema{Type: "currency"}, binaryFloat); err == nil {
			t.Fatalf("binary floating currency was aggregated: %T", binaryFloat)
		}
	}
}

func TestReportSummaryRejectsInvalidPlanAndExecutionMode(t *testing.T) {
	invalid := reportmodel.ReportSchema{Key: "invalid"}
	valid := reportmodel.ReportSchema{Key: "valid", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "order", Alias: "orders"}}}
	service := NewReportDomainService(ReportDependencies{Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
		return []reportmodel.ReportSchema{invalid, valid}
	}})
	if _, err := service.SummaryMode(t.Context(), "invalid", "realtime", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.report.dataset_source_invalid" {
		t.Fatalf("invalid plan error=%v", err)
	}
	if _, err := service.SummaryMode(t.Context(), "valid", "unsupported", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.report.execution_mode_invalid" {
		t.Fatalf("execution mode error=%v", err)
	}
}

func TestReportExportExecutionAndFieldAuthorizationEdges(t *testing.T) {
	var nilService *ReportDomainService
	if _, err := nilService.AuthorizeExportField(t.Context(), principalmodel.Principal{}, "order", "amount"); apperror.CodeOf(err) != "backend.report.execution_unavailable" {
		t.Fatalf("nil service access error=%v", err)
	}
	service := NewReportDomainService(ReportDependencies{})
	if _, err := service.ExecuteExportReport(t.Context(), reportmodel.ReportSchema{}, nil, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.report.dataset_source_invalid" {
		t.Fatalf("invalid export plan error=%v", err)
	}
	if _, err := service.AuthorizeExportField(t.Context(), principalmodel.Principal{}, "order", "amount"); apperror.CodeOf(err) != "backend.report.execution_unavailable" {
		t.Fatalf("missing access error=%v", err)
	}

	access := &reportEdgeAccess{}
	service = NewReportDomainService(ReportDependencies{Access: access})
	deniedPrincipal := accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "order", FieldKey: "amount", Read: true, Export: false}}})
	if _, err := service.AuthorizeExportField(t.Context(), deniedPrincipal, "order", "amount"); apperror.CodeOf(err) != "backend.report.export_authorizer_unavailable" {
		t.Fatalf("missing owner field authorizer=%v", err)
	}
	principal := accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{
		Permissions:   []string{"order.export"},
		FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "order", FieldKey: "amount", Read: true, Export: true}},
	})
	if _, err := service.AuthorizeExportField(t.Context(), principal, "order", "amount"); apperror.CodeOf(err) != "backend.report.export_authorizer_unavailable" {
		t.Fatalf("missing owner field authorizer with allowed bundle=%v", err)
	}

	want := errors.New("field policy unavailable")
	service = NewReportDomainService(ReportDependencies{Access: &reportExportFieldAccess{reportEdgeAccess: access, masked: true, err: want}})
	if masked, err := service.AuthorizeExportField(t.Context(), principal, "order", "amount"); !masked || !errors.Is(err, want) {
		t.Fatalf("owner authorization masked=%v err=%v", masked, err)
	}
}
