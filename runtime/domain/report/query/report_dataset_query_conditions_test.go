package query

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

type reportAccessWithoutPushdown struct{}

func (reportAccessWithoutPushdown) ReportObjectForAction(context.Context, principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
	return definitionmodel.ObjectSchema{}, nil
}
func (reportAccessWithoutPushdown) NormalizeReportListQuery(_ context.Context, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, _ principalmodel.Principal) recordmodel.RecordListQuery {
	return query
}
func (reportAccessWithoutPushdown) CanAccessReportRecord(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool {
	return true
}

type reportFaultAccess struct {
	objectErr  error
	projectErr error
	withScope  bool
}

func (a reportFaultAccess) ReportObjectForAction(context.Context, principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
	return definitionmodel.ObjectSchema{Key: "entry"}, a.objectErr
}
func (a reportFaultAccess) NormalizeReportListQuery(_ context.Context, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, _ principalmodel.Principal) recordmodel.RecordListQuery {
	if a.withScope {
		query.ScopeExpression = &recordmodel.RecordScopeExpression{}
	}
	return query
}
func (a reportFaultAccess) CanAccessReportRecord(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool {
	return true
}
func (a reportFaultAccess) ProjectReportRecordFields(_ context.Context, _ principalmodel.Principal, _ definitionmodel.ObjectSchema, records []recordmodel.Record) ([]recordmodel.Record, error) {
	return records, a.projectErr
}

type reportFaultRecords struct{ err error }

func (r reportFaultRecords) ListReportRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	return recordmodel.RecordPageResult{}, r.err
}

type reportFaultDatasetRows struct{ err error }

func (r reportFaultDatasetRows) ReadReportDatasetRows(context.Context, reportcontract.ReportDatasetRowReadRequest) ([]reportcontract.ReportDatasetRecordRow, error) {
	return nil, r.err
}

func TestExecuteReportDatasetCoversUnavailableObjectReadProjectionAndPushdownFailures(t *testing.T) {
	report := reportmodel.ReportSchema{Key: "report", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "entry", Alias: "entries"}, Measures: []reportmodel.ReportDatasetMeasure{{Key: "count", Operation: "count"}}}}
	plan, err := reportcontract.BuildReportDatasetPlan(report)
	if err != nil {
		t.Fatal(err)
	}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}
	for name, service := range map[string]*ReportDomainService{
		"nil service": nil,
		"nil access":  NewReportDomainService(ReportDependencies{Records: reportFaultRecords{}}),
		"nil records": NewReportDomainService(ReportDependencies{Access: reportFaultAccess{}}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.executeReportDataset(t.Context(), report, plan, principal); apperror.CodeOf(err) != "backend.report.execution_unavailable" {
				t.Fatalf("err=%v", err)
			}
		})
	}
	want := errors.New("failure")
	service := NewReportDomainService(ReportDependencies{Access: reportFaultAccess{objectErr: want}, Records: reportFaultRecords{}})
	if _, err := service.executeReportDataset(t.Context(), report, plan, principal); !errors.Is(err, want) {
		t.Fatalf("object err=%v", err)
	}
	service = NewReportDomainService(ReportDependencies{Access: reportFaultAccess{}, Records: reportFaultRecords{err: want}})
	if _, err := service.executeReportDataset(t.Context(), report, plan, principal); !errors.Is(err, want) || apperror.CodeOf(err) != "backend.internal_error" {
		t.Fatalf("record err=%v", err)
	}
	service = NewReportDomainService(ReportDependencies{Access: reportFaultAccess{projectErr: want}, Records: reportFaultRecords{}})
	if _, err := service.executeReportDataset(t.Context(), report, plan, principal); !errors.Is(err, want) || apperror.CodeOf(err) != "backend.internal_error" {
		t.Fatalf("projection err=%v", err)
	}
	service = NewReportDomainService(ReportDependencies{Access: reportFaultAccess{withScope: true}, Records: reportFaultRecords{}})
	if _, err := service.executeReportDataset(t.Context(), report, plan, principal); err != nil {
		t.Fatalf("scoped query err=%v", err)
	}
	service = NewReportDomainService(ReportDependencies{Access: reportDatasetAccess{objects: map[string]definitionmodel.ObjectSchema{"entry": {Key: "entry"}}, pushdown: true}, Records: reportFaultRecords{}, DatasetRows: reportFaultDatasetRows{err: want}})
	if _, err := service.executeReportDataset(t.Context(), report, plan, principal); !errors.Is(err, want) || apperror.CodeOf(err) != "backend.report.query_failed" {
		t.Fatalf("pushdown err=%v", err)
	}
}

func TestReportDatasetQueryProjectionFilterJoinAndComparisonHelpersCoverEdges(t *testing.T) {
	if reportDatasetPushdownAllowed(t.Context(), reportAccessWithoutPushdown{}, principalmodel.Principal{}, map[string]definitionmodel.ObjectSchema{"b": {Key: "b"}, "a": {Key: "a"}}) {
		t.Fatal("non-authorizer allowed pushdown")
	}
	query := reportIncludeAuthorizationProjection(recordmodel.RecordListQuery{SelectFields: []string{" visible ", " "}, OwnerField: "owner", DepartmentPathField: " ", TeamField: "team", StoreField: "store", TerritoryField: "territory", WarehouseField: "warehouse"})
	if len(query.SelectFields) != 6 || query.SelectFields[0] != "owner" {
		t.Fatalf("projection=%v", query.SelectFields)
	}

	alias := "orders"
	dataset := reportmodel.ReportDatasetSchema{
		Source:  reportmodel.ReportDatasetSource{Alias: alias},
		Filters: []reportmodel.ReportDatasetFilter{{Field: reportmodel.ReportDatasetField{SourceAlias: alias, FieldKey: "status"}, Operator: "eq", Value: "open"}},
		Dimensions: []reportmodel.ReportDatasetDimension{
			{Key: "ignored", Field: reportmodel.ReportDatasetField{SourceAlias: "other", FieldKey: "ignored"}},
			{Key: "blank", Field: reportmodel.ReportDatasetField{SourceAlias: alias, FieldKey: " "}},
		},
		Measures: []reportmodel.ReportDatasetMeasure{{Field: nil, StartField: &reportmodel.ReportDatasetField{SourceAlias: alias, FieldKey: "start"}, EndField: &reportmodel.ReportDatasetField{SourceAlias: "other", FieldKey: "end"}}},
		Privacy:  &reportmodel.ReportDatasetPrivacy{EntityField: reportmodel.ReportDatasetField{SourceAlias: alias, FieldKey: "person"}},
		Analyses: []reportmodel.ReportDatasetAnalysis{{EntityField: reportmodel.ReportDatasetField{SourceAlias: alias, FieldKey: "entity"}, TimeField: reportmodel.ReportDatasetField{SourceAlias: alias, FieldKey: "time"}}, {EventField: &reportmodel.ReportDatasetField{SourceAlias: alias, FieldKey: "event"}}},
		Joins:    []reportmodel.ReportDatasetJoin{{LeftAlias: alias, LeftField: "id", Alias: "lines", RightField: "order_id"}, {LeftAlias: "other", LeftField: "x", Alias: alias, RightField: "joined_id"}},
	}
	query = reportAliasReadQuery(dataset, alias)
	if query.FilterExpression == nil || query.FilterExpression.Operator != "eq" {
		t.Fatalf("query=%#v", query)
	}
	dataset.Filters = append(dataset.Filters, reportmodel.ReportDatasetFilter{Field: reportmodel.ReportDatasetField{SourceAlias: alias, FieldKey: "kind"}, Operator: "ne", Value: "x"})
	if query := reportAliasReadQuery(dataset, alias); query.FilterExpression == nil || query.FilterExpression.Operator != "and" {
		t.Fatalf("multi query=%#v", query)
	}
	_ = reportAliasReadQuery(reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{Alias: alias}, Filters: []reportmodel.ReportDatasetFilter{{Field: reportmodel.ReportDatasetField{SourceAlias: alias, FieldKey: "amount"}, Operator: "between", Values: []any{1}}}}, alias)

	for _, tc := range []struct {
		filter reportmodel.ReportDatasetFilter
		ok     bool
	}{
		{filter: reportmodel.ReportDatasetFilter{Operator: "eq"}, ok: true},
		{filter: reportmodel.ReportDatasetFilter{Operator: "not_null"}, ok: true},
		{filter: reportmodel.ReportDatasetFilter{Operator: "between", Values: []any{1}}, ok: false},
		{filter: reportmodel.ReportDatasetFilter{Operator: "between", Values: []any{1, 2}}, ok: true},
		{filter: reportmodel.ReportDatasetFilter{Operator: "contains"}, ok: false},
	} {
		_, ok := reportFilterExpression(tc.filter)
		if ok != tc.ok {
			t.Fatalf("filter=%#v ok=%v", tc.filter, ok)
		}
	}

	inner := reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{Alias: "root"}, Joins: []reportmodel.ReportDatasetJoin{{Alias: "child", LeftAlias: "root", Type: "inner"}, {Alias: "grandchild", LeftAlias: "child", Type: "inner"}}}
	left := inner
	left.Joins = append([]reportmodel.ReportDatasetJoin(nil), inner.Joins...)
	left.Joins[0].Type = "left"
	if !reportAliasFilterPushdownSafe(inner, "root") || !reportAliasFilterPushdownSafe(inner, "child") || reportAliasFilterPushdownSafe(left, "grandchild") || reportAliasFilterPushdownSafe(inner, "missing") {
		t.Fatal("join pushdown safety mismatch")
	}

	right := []recordmodel.Record{{ID: "right-1", Data: map[string]any{"foreign": "a"}}, {ID: "right-2", Data: map[string]any{}}}
	rows := []reportDatasetRow{{"left": &recordmodel.Record{ID: "a"}}, {"left": &recordmodel.Record{ID: "missing"}}, {"left": nil}}
	join := reportmodel.ReportDatasetJoin{Alias: "right", LeftAlias: "left", LeftField: "id", RightField: "foreign", Type: "inner"}
	if got := reportJoinDatasetRows(rows, right, join); len(got) != 1 {
		t.Fatalf("inner rows=%#v", got)
	}
	join.Type = "left"
	if got := reportJoinDatasetRows(rows, right, join); len(got) != 3 || got[1]["right"] != nil {
		t.Fatalf("left rows=%#v", got)
	}

	filters := []struct {
		actual any
		exists bool
		filter reportmodel.ReportDatasetFilter
		want   bool
	}{
		{actual: nil, exists: false, filter: reportmodel.ReportDatasetFilter{Operator: "is_null"}, want: true},
		{actual: nil, exists: true, filter: reportmodel.ReportDatasetFilter{Operator: "is_null"}, want: true},
		{actual: "x", exists: true, filter: reportmodel.ReportDatasetFilter{Operator: "not_null"}, want: true},
		{actual: nil, exists: true, filter: reportmodel.ReportDatasetFilter{Operator: "not_null"}, want: false},
		{actual: "x", exists: false, filter: reportmodel.ReportDatasetFilter{Operator: "eq", Value: "x"}, want: false},
		{actual: nil, exists: true, filter: reportmodel.ReportDatasetFilter{Operator: "eq", Value: "x"}, want: false},
	}
	for _, tc := range filters {
		if got := reportFilterMatches(tc.actual, tc.exists, tc.filter); got != tc.want {
			t.Fatalf("filter=%#v got=%v", tc, got)
		}
	}
	for _, filter := range []reportmodel.ReportDatasetFilter{
		{Operator: "eq", Value: 2}, {Operator: "ne", Value: 1}, {Operator: "gt", Value: 0}, {Operator: "gte", Value: 1},
		{Operator: "lt", Value: 2}, {Operator: "lte", Value: 1}, {Operator: "in", Values: []any{0, 1}}, {Operator: "not_in", Values: []any{2}},
		{Operator: "between", Values: []any{0, 2}}, {Operator: "contains", Value: "1"}, {Operator: "starts_with", Value: "1"}, {Operator: "ends_with", Value: "1"}, {Operator: "unknown"},
	} {
		_ = reportFilterMatches(1, true, filter)
	}
	if reportCompareValues("1", "bad") >= 0 || reportCompareValues("bad", "1") <= 0 {
		t.Fatal("mixed numeric/string comparison mismatch")
	}
	timeFilter := reportmodel.ReportDatasetFilter{Operator: "between", Values: []any{"2026-08-09T16:00:00Z", "2026-08-10T15:59:59.999999999Z"}}
	for _, value := range []string{"2026-08-10T00:00:00+08:00", "2026-08-10T23:59:59+08:00"} {
		if !reportFilterMatches(value, true, timeFilter) {
			t.Fatalf("in-range instant %q rejected", value)
		}
	}
	for _, value := range []string{"2026-08-09T23:59:59+08:00", "2026-08-11T00:00:00+08:00"} {
		if reportFilterMatches(value, true, timeFilter) {
			t.Fatalf("out-of-range instant %q accepted", value)
		}
	}
}

func TestExecuteReportDatasetCoversAnalysisAggregateComparisonAndLimitErrors(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}
	object := definitionmodel.ObjectSchema{Key: "entry", Fields: []definitionmodel.FieldSchema{{Key: "kind", Type: "text"}}}
	access := reportDatasetAccess{objects: map[string]definitionmodel.ObjectSchema{"entry": object}}
	records := reportDatasetRecords{byObject: map[string][]recordmodel.Record{"entry": {{ID: "one", Data: map[string]any{"kind": "a"}}, {ID: "two", Data: map[string]any{"kind": "b"}}}}}
	base := reportmodel.ReportSchema{Key: "report", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "entry", Alias: "entries"}, Dimensions: []reportmodel.ReportDatasetDimension{{Key: "kind", Field: reportmodel.ReportDatasetField{SourceAlias: "entries", FieldKey: "kind"}}}, Measures: []reportmodel.ReportDatasetMeasure{{Key: "count", Operation: "count", SourceAlias: "entries"}}}}
	plan, err := reportcontract.BuildReportDatasetPlan(base)
	if err != nil {
		t.Fatal(err)
	}
	service := NewReportDomainService(ReportDependencies{Access: access, Records: records})

	invalidAnalysis := base
	invalidAnalysis.Dataset.Analyses = []reportmodel.ReportDatasetAnalysis{{Key: "bad", Type: "unknown"}}
	if _, err := service.executeReportDataset(t.Context(), invalidAnalysis, plan, principal); apperror.CodeOf(err) != "backend.report.analysis_invalid" {
		t.Fatalf("analysis err=%v", err)
	}
	invalidAggregate := base
	invalidAggregate.Dataset.TimeZone = "invalid/time-zone"
	if _, err := service.executeReportDataset(t.Context(), invalidAggregate, plan, principal); apperror.CodeOf(err) != "backend.report.execution_invalid" {
		t.Fatalf("aggregate err=%v", err)
	}
	invalidComparison := base
	invalidComparison.Dataset.Comparisons = []reportmodel.ReportDatasetComparison{{Key: "change", TimeDimensionKey: "kind", MeasureKey: "count", Operation: "difference"}}
	if _, err := service.executeReportDataset(t.Context(), invalidComparison, plan, principal); apperror.CodeOf(err) != "backend.report.comparison_invalid" {
		t.Fatalf("comparison err=%v", err)
	}
	limited := base
	limited.Dataset.Limit = 1
	summary, err := service.executeReportDataset(t.Context(), limited, plan, principal)
	if err != nil || len(summary.Rows) != 1 {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}
}
