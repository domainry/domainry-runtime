package service

import (
	"context"
	"reflect"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type reportDatasetAccess struct {
	objects       map[string]definitionmodel.ObjectSchema
	deniedID      string
	requiredOwner string
	pushdown      bool
}

func (a reportDatasetAccess) ReportObjectForAction(_ context.Context, _ principalmodel.Principal, objectKey, _ string) (definitionmodel.ObjectSchema, error) {
	return a.objects[objectKey], nil
}

func (a reportDatasetAccess) NormalizeReportListQuery(_ context.Context, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, _ principalmodel.Principal) recordmodel.RecordListQuery {
	if a.requiredOwner != "" {
		query.OwnerField = "owner"
	}
	return query
}
func (a reportDatasetAccess) CanAccessReportRecord(_ context.Context, _ principalmodel.Principal, _ definitionmodel.ObjectSchema, record recordmodel.Record) bool {
	return record.ID != a.deniedID && (a.requiredOwner == "" || record.Data["owner"] == a.requiredOwner)
}
func (a reportDatasetAccess) ProjectReportRecordFields(_ context.Context, _ principalmodel.Principal, _ definitionmodel.ObjectSchema, records []recordmodel.Record) ([]recordmodel.Record, error) {
	result := make([]recordmodel.Record, len(records))
	for index, record := range records {
		data := make(map[string]any, len(record.Data))
		for key, value := range record.Data {
			if key != "secret_amount" && (a.requiredOwner == "" || key != "owner") {
				data[key] = value
			}
		}
		result[index] = record
		result[index].Data = data
	}
	return result, nil
}

func TestReportDatasetAuthorizesWithInternalScopeFieldsBeforeCLSProjection(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "entry", Fields: []definitionmodel.FieldSchema{{Key: "owner", Type: "user"}, {Key: "amount", Type: "currency"}}}
	report := reportmodel.ReportSchema{Key: "owned", Dataset: reportmodel.ReportDatasetSchema{
		Source:   reportmodel.ReportDatasetSource{ObjectKey: "entry", Alias: "entries"},
		Measures: []reportmodel.ReportDatasetMeasure{{Key: "amount", Operation: "sum", Field: reportField("entries", "amount")}},
	}}
	service := NewReportDomainService(ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{report}
		},
		Access: reportDatasetAccess{objects: map[string]definitionmodel.ObjectSchema{"entry": object}, requiredOwner: "user-a"},
		Records: reportDatasetRecords{byObject: map[string][]recordmodel.Record{"entry": {
			{ID: "allowed", Data: map[string]any{"owner": "user-a", "amount": "10.00"}},
			{ID: "denied", Data: map[string]any{"owner": "user-b", "amount": "999.00"}},
		}}},
	})
	summary, err := service.Summary(t.Context(), "owned", principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a", UserID: "user-a"}})
	if err != nil || summary.SourceRowCount != 1 || summary.Rows[0].Measures["amount"] != "10.00" {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}
}
func (a reportDatasetAccess) CanPushdownReportDataset(_ context.Context, _ principalmodel.Principal, _ []definitionmodel.ObjectSchema) bool {
	return a.pushdown
}

type reportDatasetRecords struct {
	byObject map[string][]recordmodel.Record
}

type reportDatasetRowReader struct {
	calls int
	rows  []reportcontract.ReportDatasetRecordRow
}

func (r *reportDatasetRowReader) ReadReportDatasetRows(context.Context, reportcontract.ReportDatasetRowReadRequest) ([]reportcontract.ReportDatasetRecordRow, error) {
	r.calls++
	return r.rows, nil
}

func TestReportDatasetUsesSQLRowsOnlyWhenRecordOwnerAuthorizesPushdown(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "entry", Fields: []definitionmodel.FieldSchema{{Key: "amount", Type: "currency"}}}
	objects := map[string]definitionmodel.ObjectSchema{"entry": object}
	report := reportmodel.ReportSchema{Key: "totals", Dataset: reportmodel.ReportDatasetSchema{
		Source:   reportmodel.ReportDatasetSource{ObjectKey: "entry", Alias: "entries"},
		Measures: []reportmodel.ReportDatasetMeasure{{Key: "amount", Operation: "sum", Field: reportField("entries", "amount")}},
	}}
	newService := func(allow bool, reader *reportDatasetRowReader) *ReportDomainService {
		return NewReportDomainService(ReportDependencies{
			Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
				return []reportmodel.ReportSchema{report}
			},
			Access: reportDatasetAccess{objects: objects, pushdown: allow},
			Records: reportDatasetRecords{byObject: map[string][]recordmodel.Record{
				"entry": {{ID: "fallback", Data: map[string]any{"amount": "10.00"}}},
			}},
			DatasetRows: reader,
		})
	}
	reader := &reportDatasetRowReader{rows: []reportcontract.ReportDatasetRecordRow{{Records: map[string]recordmodel.Record{
		"entries": {ID: "sql", Data: map[string]any{"amount": "20.00"}},
	}}}}
	summary, err := newService(true, reader).Summary(t.Context(), "totals", principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}})
	if err != nil || reader.calls != 1 || summary.Rows[0].Measures["amount"] != "20.00" {
		t.Fatalf("authorized summary=%#v calls=%d err=%v", summary, reader.calls, err)
	}
	reader.calls = 0
	summary, err = newService(false, reader).Summary(t.Context(), "totals", principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}})
	if err != nil || reader.calls != 0 || summary.Rows[0].Measures["amount"] != "10.00" {
		t.Fatalf("fallback summary=%#v calls=%d err=%v", summary, reader.calls, err)
	}
}

func (r reportDatasetRecords) ListReportRecords(_ context.Context, _ string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	all := r.byObject[object.Key]
	const pageSize = 2
	start := (query.Page - 1) * pageSize
	if start >= len(all) {
		return recordmodel.RecordPageResult{Page: query.Page, PageSize: pageSize, Total: len(all)}, nil
	}
	end := start + pageSize
	if end > len(all) {
		end = len(all)
	}
	return recordmodel.RecordPageResult{Items: append([]recordmodel.Record(nil), all[start:end]...), Page: query.Page, PageSize: pageSize, Total: len(all), HasNext: end < len(all)}, nil
}

func TestReportDatasetExecutesJoinFiltersDimensionsAndEveryBaseMeasure(t *testing.T) {
	objects := map[string]definitionmodel.ObjectSchema{
		"account": {Key: "account", Fields: []definitionmodel.FieldSchema{{Key: "segment", Type: "status"}}},
		"transaction": {Key: "transaction", Fields: []definitionmodel.FieldSchema{
			{Key: "account_id", Type: "relation"}, {Key: "status", Type: "status"},
			{Key: "amount", Type: "currency"}, {Key: "secret_amount", Type: "currency"},
			{Key: "started_at", Type: "datetime"}, {Key: "ended_at", Type: "datetime"},
		}},
	}
	records := reportDatasetRecords{byObject: map[string][]recordmodel.Record{
		"account": {
			{ID: "account-a", Data: map[string]any{"segment": "A"}},
			{ID: "account-b", Data: map[string]any{"segment": "B"}},
		},
		"transaction": {
			{ID: "tx-1", Data: map[string]any{"account_id": "account-a", "status": "settled", "amount": "10.00", "secret_amount": "99.00", "started_at": "2026-07-01T10:00:00Z", "ended_at": "2026-07-01T11:00:00Z"}},
			{ID: "tx-2", Data: map[string]any{"account_id": "account-a", "status": "settled", "amount": "20.00", "secret_amount": "99.00", "started_at": "2026-07-02T10:00:00Z", "ended_at": "2026-07-02T10:30:00Z"}},
			{ID: "tx-3", Data: map[string]any{"account_id": "account-b", "status": "settled", "amount": "5.00", "secret_amount": "99.00", "started_at": "2026-07-03T10:00:00Z", "ended_at": "2026-07-03T10:15:00Z"}},
			{ID: "denied", Data: map[string]any{"account_id": "account-a", "status": "settled", "amount": "1000.00", "started_at": "2026-07-03T10:00:00Z", "ended_at": "2026-07-03T11:00:00Z"}},
		},
	}}
	report := reportmodel.ReportSchema{Key: "performance", Dataset: reportmodel.ReportDatasetSchema{
		Source:     reportmodel.ReportDatasetSource{ObjectKey: "account", Alias: "accounts"},
		Joins:      []reportmodel.ReportDatasetJoin{{ObjectKey: "transaction", Alias: "transactions", Type: "inner", LeftAlias: "accounts", LeftField: "id", RightField: "account_id", Cardinality: "one_to_many"}},
		Filters:    []reportmodel.ReportDatasetFilter{{Field: *reportField("transactions", "status"), Operator: "in", Values: []any{"settled"}}},
		Dimensions: []reportmodel.ReportDatasetDimension{{Key: "segment", Field: *reportField("accounts", "segment")}},
		Measures: []reportmodel.ReportDatasetMeasure{
			{Key: "average_per_transaction", Operation: "ratio", NumeratorKey: "amount_sum", DenominatorKey: "transactions"},
			{Key: "transactions", Operation: "count", SourceAlias: "transactions"},
			{Key: "accounts", Operation: "distinct_count", Field: reportField("transactions", "account_id")},
			{Key: "amount_sum", Operation: "sum", Field: reportField("transactions", "amount")},
			{Key: "amount_avg", Operation: "avg", Field: reportField("transactions", "amount")},
			{Key: "amount_min", Operation: "min", Field: reportField("transactions", "amount")},
			{Key: "amount_max", Operation: "max", Field: reportField("transactions", "amount")},
			{Key: "elapsed_seconds", Operation: "duration", StartField: reportField("transactions", "started_at"), EndField: reportField("transactions", "ended_at")},
			{Key: "amount_p50", Operation: "percentile", Field: reportField("transactions", "amount"), Percentile: "50"},
			{Key: "hidden_sum", Operation: "sum", Field: reportField("transactions", "secret_amount")},
		},
		Sort: []reportmodel.ReportDatasetSort{{Key: "amount_sum", Direction: "desc"}}, Limit: 2,
	}}
	service := NewReportDomainService(ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{report}
		},
		Access: reportDatasetAccess{objects: objects, deniedID: "denied"}, Records: records,
	})

	summary, err := service.Summary(t.Context(), "performance", principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}})
	if err != nil {
		t.Fatal(err)
	}
	if summary.SourceRowCount != 3 || summary.RowCount != 2 || summary.Rows[0].Dimensions["segment"] != "A" {
		t.Fatalf("summary=%#v", summary)
	}
	want := map[string]string{
		"average_per_transaction": "15.000000", "transactions": "2", "accounts": "1",
		"amount_sum": "30.00", "amount_avg": "15.000000", "amount_min": "10.00", "amount_max": "20.00",
		"elapsed_seconds": "5400", "amount_p50": "10.00", "hidden_sum": "0",
	}
	for key, expected := range want {
		if actual := summary.Rows[0].Measures[key]; actual != expected {
			t.Fatalf("measure %s=%q want=%q rows=%#v", key, actual, expected, summary.Rows)
		}
	}
}

func TestReportDatasetCompoundJoinsSelectCurrentOrderAndItemVersionsWithoutDuplicates(t *testing.T) {
	objects := map[string]definitionmodel.ObjectSchema{
		"order":         {Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "version_counter", Type: "integer"}}},
		"order_version": {Key: "order_version", Fields: []definitionmodel.FieldSchema{{Key: "order_id", Type: "relation"}, {Key: "version_no", Type: "integer"}}},
		"item":          {Key: "item", Fields: []definitionmodel.FieldSchema{{Key: "order_version_id", Type: "relation"}, {Key: "version_counter", Type: "integer"}}},
		"item_version":  {Key: "item_version", Fields: []definitionmodel.FieldSchema{{Key: "item_id", Type: "relation"}, {Key: "version_no", Type: "integer"}, {Key: "classification", Type: "text"}}},
	}
	equalities := func(leftID, rightID, leftVersion, rightVersion string) []reportmodel.ReportDatasetJoinFieldEquality {
		return []reportmodel.ReportDatasetJoinFieldEquality{{LeftField: leftID, RightField: rightID}, {LeftField: leftVersion, RightField: rightVersion}}
	}
	report := reportmodel.ReportSchema{Key: "current_order_items", Dataset: reportmodel.ReportDatasetSchema{
		Source: reportmodel.ReportDatasetSource{ObjectKey: "order", Alias: "orders"},
		Joins: []reportmodel.ReportDatasetJoin{
			{ObjectKey: "order_version", Alias: "current_order_versions", Type: "inner", LeftAlias: "orders", FieldEqualities: equalities("id", "order_id", "version_counter", "version_no"), Cardinality: "one_to_one"},
			{ObjectKey: "item", Alias: "items", Type: "inner", LeftAlias: "current_order_versions", LeftField: "id", RightField: "order_version_id", Cardinality: "one_to_many"},
			{ObjectKey: "item_version", Alias: "current_item_versions", Type: "inner", LeftAlias: "items", FieldEqualities: equalities("id", "item_id", "version_counter", "version_no"), Cardinality: "one_to_one"},
		},
		Dimensions: []reportmodel.ReportDatasetDimension{
			{Key: "item_id", Field: *reportField("items", "id")},
			{Key: "classification", Field: *reportField("current_item_versions", "classification")},
		},
		Measures: []reportmodel.ReportDatasetMeasure{{Key: "current_versions", Operation: "count", SourceAlias: "current_item_versions"}},
	}}
	records := reportDatasetRecords{byObject: map[string][]recordmodel.Record{
		"order": {{ID: "order-a", Data: map[string]any{"version_counter": 2}}, {ID: "order-b", Data: map[string]any{"version_counter": 1}}},
		"order_version": {
			{ID: "ov-a1", Data: map[string]any{"order_id": "order-a", "version_no": 1}},
			{ID: "ov-a2", Data: map[string]any{"order_id": "order-a", "version_no": 2}},
			{ID: "ov-b1", Data: map[string]any{"order_id": "order-b", "version_no": 1}},
		},
		"item": {
			{ID: "item-a", Data: map[string]any{"order_version_id": "ov-a2", "version_counter": 2}},
			{ID: "item-b", Data: map[string]any{"order_version_id": "ov-b1", "version_counter": 1}},
		},
		"item_version": {
			{ID: "iv-a1", Data: map[string]any{"item_id": "item-a", "version_no": 1, "classification": "legacy"}},
			{ID: "iv-a2", Data: map[string]any{"item_id": "item-a", "version_no": 2, "classification": "current-a"}},
			{ID: "iv-b1", Data: map[string]any{"item_id": "item-b", "version_no": 1, "classification": "current-b"}},
		},
	}}
	summary, err := serviceWithReport(report, objects, records).Summary(t.Context(), report.Key, principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}})
	if err != nil || summary.SourceRowCount != 2 || summary.RowCount != 2 {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}
	classifications := map[string]bool{}
	for _, row := range summary.Rows {
		if row.Measures["current_versions"] != "1" {
			t.Fatalf("duplicate current version row=%#v", row)
		}
		classifications[row.Dimensions["classification"]] = true
	}
	if !classifications["current-a"] || !classifications["current-b"] || classifications["legacy"] {
		t.Fatalf("classifications=%v rows=%#v", classifications, summary.Rows)
	}

	violating := records
	violating.byObject = make(map[string][]recordmodel.Record, len(records.byObject))
	for key, values := range records.byObject {
		violating.byObject[key] = append([]recordmodel.Record(nil), values...)
	}
	violating.byObject["order_version"] = append(violating.byObject["order_version"], recordmodel.Record{ID: "ov-a2-duplicate", Data: map[string]any{"order_id": "order-a", "version_no": 2}})
	if _, err := serviceWithReport(report, objects, violating).Summary(t.Context(), report.Key, principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}}); apperror.CodeOf(err) != "backend.report.join_cardinality_violation" {
		t.Fatalf("cardinality err=%v", err)
	}
}

func TestReportDatasetAppliesRightFilterAfterLeftJoinAndBucketsTime(t *testing.T) {
	objects := map[string]definitionmodel.ObjectSchema{
		"resource": {Key: "resource", Fields: []definitionmodel.FieldSchema{{Key: "opened_at", Type: "datetime"}}},
		"incident": {Key: "incident", Fields: []definitionmodel.FieldSchema{{Key: "resource_id", Type: "relation"}, {Key: "status", Type: "status"}}},
	}
	report := reportmodel.ReportSchema{Key: "quiet", Dataset: reportmodel.ReportDatasetSchema{
		Source:     reportmodel.ReportDatasetSource{ObjectKey: "resource", Alias: "resources"},
		Joins:      []reportmodel.ReportDatasetJoin{{ObjectKey: "incident", Alias: "incidents", Type: "left", LeftAlias: "resources", LeftField: "id", RightField: "resource_id", Cardinality: "one_to_one"}},
		Filters:    []reportmodel.ReportDatasetFilter{{Field: *reportField("incidents", "status"), Operator: "is_null"}},
		Dimensions: []reportmodel.ReportDatasetDimension{{Key: "opened_day", Field: *reportField("resources", "opened_at"), TimeGrain: "day"}},
		Measures:   []reportmodel.ReportDatasetMeasure{{Key: "resources", Operation: "count", SourceAlias: "resources"}},
		TimeZone:   "Asia/Shanghai",
	}}
	service := NewReportDomainService(ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{report}
		},
		Access: reportDatasetAccess{objects: objects},
		Records: reportDatasetRecords{byObject: map[string][]recordmodel.Record{
			"resource": {{ID: "r-1", Data: map[string]any{"opened_at": "2026-07-01T18:00:00Z"}}, {ID: "r-2", Data: map[string]any{"opened_at": "2026-07-02T18:00:00Z"}}},
			"incident": {{ID: "i-1", Data: map[string]any{"resource_id": "r-1", "status": "open"}}},
		}},
	})
	summary, err := service.Summary(t.Context(), "quiet", principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}})
	if err != nil || summary.SourceRowCount != 1 || summary.Rows[0].Dimensions["opened_day"] != "2026-07-03" || summary.Rows[0].Measures["resources"] != "1" {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}

	report.Dataset.TimeZone = "Mars/Olympus"
	if _, err := serviceWithReport(report, objects, reportDatasetRecords{}).Summary(t.Context(), "quiet", principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}}); apperror.CodeOf(err) != "backend.report.execution_invalid" {
		t.Fatalf("invalid timezone error=%v", err)
	}
}

func serviceWithReport(report reportmodel.ReportSchema, objects map[string]definitionmodel.ObjectSchema, records reportDatasetRecords) *ReportDomainService {
	return NewReportDomainService(ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{report}
		},
		Access: reportDatasetAccess{objects: objects}, Records: records,
	})
}

func TestReportAliasReadQueryPushesSafeFiltersAndProjectionBeforeAggregation(t *testing.T) {
	dataset := reportmodel.ReportDatasetSchema{
		Source: reportmodel.ReportDatasetSource{ObjectKey: "order", Alias: "orders"},
		Joins: []reportmodel.ReportDatasetJoin{
			{ObjectKey: "line", Alias: "lines", Type: "inner", LeftAlias: "orders", LeftField: "id", RightField: "order_id", Cardinality: "one_to_many"},
			{ObjectKey: "note", Alias: "notes", Type: "left", LeftAlias: "lines", LeftField: "id", RightField: "line_id", Cardinality: "one_to_one"},
		},
		Filters: []reportmodel.ReportDatasetFilter{
			{Field: *reportField("lines", "status"), Operator: "eq", Value: "ready"},
			{Field: *reportField("lines", "amount"), Operator: "between", Values: []any{"1.00", "9.00"}},
			{Field: *reportField("notes", "status"), Operator: "is_null"},
		},
		Measures: []reportmodel.ReportDatasetMeasure{{Key: "total", Operation: "sum", Field: reportField("lines", "amount")}},
	}
	query := reportAliasReadQuery(dataset, "lines")
	if query.FilterExpression == nil || query.FilterExpression.Operator != "and" || len(query.FilterExpression.Children) != 2 {
		t.Fatalf("filter=%#v", query.FilterExpression)
	}
	if !reflect.DeepEqual(query.SelectFields, []string{"amount", "order_id", "status"}) {
		t.Fatalf("select_fields=%#v", query.SelectFields)
	}
	leftQuery := reportAliasReadQuery(dataset, "notes")
	if leftQuery.FilterExpression != nil {
		t.Fatalf("left-join filter must stay post-join: %#v", leftQuery.FilterExpression)
	}
}

func TestReportDatasetPrivacySuppressesSmallGroupsBeforeResponse(t *testing.T) {
	objects := map[string]definitionmodel.ObjectSchema{"event": {Key: "event", Fields: []definitionmodel.FieldSchema{{Key: "kind", Type: "status"}}}}
	report := reportmodel.ReportSchema{Key: "private", Dataset: reportmodel.ReportDatasetSchema{
		Source:     reportmodel.ReportDatasetSource{ObjectKey: "event", Alias: "events"},
		Dimensions: []reportmodel.ReportDatasetDimension{{Key: "kind", Field: *reportField("events", "kind")}},
		Measures:   []reportmodel.ReportDatasetMeasure{{Key: "events", Operation: "count", SourceAlias: "events"}},
		Privacy:    &reportmodel.ReportDatasetPrivacy{MinimumGroupSize: 2, EntityField: *reportField("events", "id")},
	}}
	service := serviceWithReport(report, objects, reportDatasetRecords{byObject: map[string][]recordmodel.Record{"event": {
		{ID: "e-1", Data: map[string]any{"kind": "large"}}, {ID: "e-2", Data: map[string]any{"kind": "large"}}, {ID: "e-3", Data: map[string]any{"kind": "small"}},
	}}})
	summary, err := service.Summary(t.Context(), "private", principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}})
	if err != nil || summary.RowCount != 1 || summary.SourceRowCount != 2 || summary.Rows[0].Dimensions["kind"] != "large" || summary.Rows[0].Measures["events"] != "2" {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}
}

func TestReportDatasetComputesExactPeriodOverPeriodComparisons(t *testing.T) {
	objects := map[string]definitionmodel.ObjectSchema{"sale": {Key: "sale", Fields: []definitionmodel.FieldSchema{{Key: "occurred_at", Type: "datetime"}, {Key: "amount", Type: "currency"}}}}
	report := reportmodel.ReportSchema{Key: "monthly", Dataset: reportmodel.ReportDatasetSchema{
		Source:     reportmodel.ReportDatasetSource{ObjectKey: "sale", Alias: "sales"},
		Dimensions: []reportmodel.ReportDatasetDimension{{Key: "month", Field: *reportField("sales", "occurred_at"), TimeGrain: "month"}},
		Measures:   []reportmodel.ReportDatasetMeasure{{Key: "revenue", Operation: "sum", Field: reportField("sales", "amount")}},
		Comparisons: []reportmodel.ReportDatasetComparison{
			{Key: "revenue_change", Type: "period_over_period", TimeDimensionKey: "month", MeasureKey: "revenue", Operation: "difference", OffsetPeriods: 1},
			{Key: "revenue_growth", Type: "period_over_period", TimeDimensionKey: "month", MeasureKey: "revenue", Operation: "percent_change", OffsetPeriods: 1},
		},
		Sort: []reportmodel.ReportDatasetSort{{Key: "month", Direction: "asc"}},
	}}
	service := serviceWithReport(report, objects, reportDatasetRecords{byObject: map[string][]recordmodel.Record{"sale": {
		{ID: "jan", Data: map[string]any{"occurred_at": "2026-01-10T00:00:00Z", "amount": "10.00"}},
		{ID: "feb", Data: map[string]any{"occurred_at": "2026-02-10T00:00:00Z", "amount": "15.00"}},
		{ID: "apr", Data: map[string]any{"occurred_at": "2026-04-10T00:00:00Z", "amount": "30.00"}},
	}}})
	summary, err := service.Summary(t.Context(), "monthly", principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}})
	if err != nil || summary.RowCount != 3 {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}
	if summary.Rows[0].Measures["revenue_change"] != "" || summary.Rows[1].Measures["revenue_change"] != "5.00" || summary.Rows[1].Measures["revenue_growth"] != "50.000000" || summary.Rows[2].Measures["revenue_change"] != "" {
		t.Fatalf("rows=%#v", summary.Rows)
	}
}

func TestReportDatasetExecutesOrderedFunnelAndCohortRetention(t *testing.T) {
	objects := map[string]definitionmodel.ObjectSchema{"activity": {Key: "activity", Fields: []definitionmodel.FieldSchema{{Key: "person_id", Type: "relation"}, {Key: "event", Type: "status"}, {Key: "occurred_at", Type: "datetime"}}}}
	report := reportmodel.ReportSchema{Key: "journey", Dataset: reportmodel.ReportDatasetSchema{
		Source: reportmodel.ReportDatasetSource{ObjectKey: "activity", Alias: "activities"},
		Analyses: []reportmodel.ReportDatasetAnalysis{
			{Key: "acquisition", Type: "funnel", EntityField: *reportField("activities", "person_id"), EventField: reportField("activities", "event"), TimeField: *reportField("activities", "occurred_at"), Stages: []reportmodel.ReportDatasetFunnelStage{{Key: "view", Values: []any{"view"}}, {Key: "signup", Values: []any{"signup"}}, {Key: "purchase", Values: []any{"purchase"}}}, WindowSeconds: 864000},
			{Key: "retention", Type: "cohort_retention", EntityField: *reportField("activities", "person_id"), TimeField: *reportField("activities", "occurred_at"), CohortGrain: "month", PeriodGrain: "month", MaximumPeriods: 6},
		},
	}}
	service := serviceWithReport(report, objects, reportDatasetRecords{byObject: map[string][]recordmodel.Record{"activity": {
		{ID: "1", Data: map[string]any{"person_id": "u1", "event": "view", "occurred_at": "2026-01-01T00:00:00Z"}},
		{ID: "2", Data: map[string]any{"person_id": "u1", "event": "signup", "occurred_at": "2026-01-02T00:00:00Z"}},
		{ID: "3", Data: map[string]any{"person_id": "u1", "event": "purchase", "occurred_at": "2026-01-03T00:00:00Z"}},
		{ID: "4", Data: map[string]any{"person_id": "u1", "event": "return", "occurred_at": "2026-02-03T00:00:00Z"}},
		{ID: "5", Data: map[string]any{"person_id": "u2", "event": "view", "occurred_at": "2026-01-01T00:00:00Z"}},
		{ID: "6", Data: map[string]any{"person_id": "u2", "event": "signup", "occurred_at": "2026-01-05T00:00:00Z"}},
		{ID: "7", Data: map[string]any{"person_id": "u3", "event": "signup", "occurred_at": "2026-02-01T00:00:00Z"}},
		{ID: "8", Data: map[string]any{"person_id": "u3", "event": "return", "occurred_at": "2026-03-01T00:00:00Z"}},
	}}})
	summary, err := service.Summary(t.Context(), "journey", principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}})
	if err != nil || len(summary.Analyses) != 2 {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}
	funnel := summary.Analyses[0].Rows
	if len(funnel) != 3 || funnel[0].Measures["entities"] != "2" || funnel[1].Measures["entities"] != "2" || funnel[2].Measures["entities"] != "1" || funnel[2].Measures["conversion_rate"] != "0.500000" {
		t.Fatalf("funnel=%#v", funnel)
	}
	retention := summary.Analyses[1].Rows
	if len(retention) != 4 || retention[0].Dimensions["cohort"] != "2026-01" || retention[0].Measures["cohort_size"] != "2" || retention[1].Dimensions["period_index"] != "1" || retention[1].Measures["retained_entities"] != "1" || retention[1].Measures["retention_rate"] != "0.500000" {
		t.Fatalf("retention=%#v", retention)
	}
}
