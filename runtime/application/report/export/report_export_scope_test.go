package export

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	reportservice "github.com/domainry/domainry-runtime/runtime/domain/report/query"
)

type exportAccessStub struct {
	masked string
	denied string
	err    error
}

func (s exportAccessStub) ReportObjectForAction(_ context.Context, _ principalmodel.Principal, objectKey, _ string) (definitionmodel.ObjectSchema, error) {
	return definitionmodel.ObjectSchema{Key: objectKey}, nil
}
func (exportAccessStub) NormalizeReportListQuery(_ context.Context, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, _ principalmodel.Principal) recordmodel.RecordListQuery {
	return query
}
func (exportAccessStub) CanAccessReportRecord(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool {
	return true
}
func (s exportAccessStub) AuthorizeReportExportField(_ context.Context, _ principalmodel.Principal, _, fieldKey string) (bool, error) {
	if fieldKey == s.denied {
		if s.err != nil {
			return false, s.err
		}
		return false, &apperror.AppError{Kind: apperror.KindForbidden, Code: "denied"}
	}
	return fieldKey == s.masked, nil
}

func exportPrincipal() principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-a", AuthorizationRevision: "rev-1"}}, accessfixture.Bundle{
		Key: "report-exporter", Permissions: []string{"order.export", "tag_assignment.export", "tag_definition.export"}, RecordScope: "all_records",
		FieldPolicies: []accessfixture.FieldPolicyFixture{
			{ObjectKey: "order", FieldKey: "status", Read: true, Export: true},
			{ObjectKey: "order", FieldKey: "occurred_at", Read: true, Export: true},
			{ObjectKey: "order", FieldKey: "amount", Read: true, Export: true},
		},
	})
}

func exportReport() reportmodel.ReportSchema {
	amount := reportmodel.ReportDatasetField{SourceAlias: "order", FieldKey: "amount"}
	return reportmodel.ReportSchema{Key: "orders", Dataset: reportmodel.ReportDatasetSchema{
		Source: reportmodel.ReportDatasetSource{ObjectKey: "order", Alias: "order"}, TimeZone: "UTC",
		Dimensions: []reportmodel.ReportDatasetDimension{
			{Key: "status", Field: reportmodel.ReportDatasetField{SourceAlias: "order", FieldKey: "status"}},
			{Key: "day", Field: reportmodel.ReportDatasetField{SourceAlias: "order", FieldKey: "occurred_at"}, TimeGrain: "day"},
		},
		Measures: []reportmodel.ReportDatasetMeasure{{Key: "orders", Operation: "count", SourceAlias: "order"}, {Key: "amount", Operation: "sum", Field: &amount}},
		Analyses: []reportmodel.ReportDatasetAnalysis{{Key: "funnel", EntityField: reportmodel.ReportDatasetField{SourceAlias: "order", FieldKey: "customer_id"}, TimeField: reportmodel.ReportDatasetField{SourceAlias: "order", FieldKey: "occurred_at"}}},
	}}
}

func exportRequest() reportmodel.ReportExportScopeRequest {
	return reportmodel.ReportExportScopeRequest{Purpose: "audit evidence", FieldProjection: []string{"status", "orders"}, Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}}
}

func TestExportHelpersCoverClosedInputs(t *testing.T) {
	for _, test := range []struct {
		op     string
		values int
		want   bool
	}{{"is_null", 0, true}, {"not_null", 1, false}, {"eq", 1, true}, {"ne", 0, false}, {"between", 2, true}, {"between", 1, false}, {"in", 1, true}, {"not_in", 64, true}, {"in", 65, false}, {"unknown", 1, false}} {
		if got := validReportExportFilter(test.op, test.values); got != test.want {
			t.Fatalf("filter %s/%d=%v", test.op, test.values, got)
		}
	}
	field := reportmodel.ReportDatasetField{SourceAlias: "o", FieldKey: "value"}
	if got := reportMeasureFields(reportmodel.ReportDatasetMeasure{Field: &field, StartField: &field, EndField: &field}); len(got) != 3 {
		t.Fatalf("measure fields=%v", got)
	}
	if normalizeScopeStrings(nil, 1, 2) != nil || normalizeScopeStrings([]string{"a", "b"}, 1, 2) != nil || normalizeScopeStrings([]string{""}, 1, 2) != nil || normalizeScopeStrings([]string{"abc"}, 1, 2) != nil {
		t.Fatal("invalid scope strings accepted")
	}
	if got := normalizeScopeStrings([]string{" b ", "a", "a"}, 3, 2); strings.Join(got, ",") != "a,b" {
		t.Fatalf("scope strings=%v", got)
	}
	if got := normalizeScopeProjection([]string{" a ", "", "a", "b"}); strings.Join(got, ",") != "a,b" {
		t.Fatalf("projection=%v", got)
	}
	if got := stringValuesAsAny([]string{"a", "b"}); len(got) != 2 || got[1] != "b" {
		t.Fatalf("any values=%v", got)
	}
	if _, err := reportExportDate("2026-08-10T01:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := reportExportDate("2026-08-10"); err != nil {
		t.Fatal(err)
	}
	if _, err := reportExportDate("invalid"); err == nil {
		t.Fatal("invalid date accepted")
	}
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	from, err := reportExportDateBoundary("2026-08-10", shanghai, false)
	if err != nil || from.UTC().Format(time.RFC3339Nano) != "2026-08-09T16:00:00Z" {
		t.Fatalf("localized from=%s err=%v", from, err)
	}
	to, err := reportExportDateBoundary("2026-08-10", shanghai, true)
	if err != nil || to.UTC().Format(time.RFC3339Nano) != "2026-08-10T15:59:59.999999999Z" {
		t.Fatalf("localized to=%s err=%v", to, err)
	}
	if utc, err := reportExportDateBoundary("2026-08-10", nil, false); err != nil || utc.Location() != time.UTC {
		t.Fatalf("nil location boundary=%s err=%v", utc, err)
	}
	if _, err := CanonicalJSONSHA256(make(chan int)); err == nil {
		t.Fatal("unsupported JSON accepted")
	}
	if canonicalJSONEqual(make(chan int), map[string]string{}) {
		t.Fatal("unsupported JSON compared equal")
	}
	expected := []reportmodel.ReportMetricDefinitionRef{{Key: "a", Version: "v1"}, {Key: "b", Version: "v2"}}
	for _, requested := range [][]reportmodel.ReportMetricDefinitionRef{
		{{Key: "a", Version: "v1"}},
		{{Key: "a", Version: "wrong"}, {Key: "b", Version: "v2"}},
		{{Key: "", Version: "v1"}, {Key: "b", Version: "v2"}},
		{{Key: "a", Version: ""}, {Key: "b", Version: "v2"}},
		{{Key: "a", Version: "v1"}, {Key: "a", Version: "v1"}},
	} {
		if reportMetricDefinitionsEqual(requested, expected) {
			t.Fatalf("metric definitions accepted: %v", requested)
		}
	}
	if !reportMetricDefinitionsEqual([]reportmodel.ReportMetricDefinitionRef{{Key: " b ", Version: " v2 "}, {Key: "a", Version: "v1"}}, expected) {
		t.Fatal("ordered metric definition set rejected")
	}
	if SafeFilename(" revenue / report ", "order:item") != "revenue___report-order_item.csv" || len(SHA256Hex([]byte("x"))) != 64 {
		t.Fatal("safe filename or hash mismatch")
	}
	if SafeFilename("azAZ09-_!{", "") != "azAZ09--.csv" {
		t.Fatal("safe filename character allowlist mismatch")
	}
	if apperror.CodeOf(exportScopeError("scope-error")) != "scope-error" {
		t.Fatal("scope error code mismatch")
	}
}

func TestExportRowsAndDataExchangePreflightCSV(t *testing.T) {
	row := reportmodel.ReportResultRow{Dimensions: map[string]string{"status": "paid", "empty": ""}, Measures: map[string]string{"orders": "2"}}
	summary := reportmodel.ReportSummary{Rows: []reportmodel.ReportResultRow{row}, Analyses: []reportmodel.ReportAnalysisResult{{Key: "funnel", Rows: []reportmodel.ReportResultRow{row}}}}
	if rows, err := Rows(summary, ""); err != nil || len(rows) != 1 {
		t.Fatalf("default rows=%v err=%v", rows, err)
	}
	if rows, err := Rows(summary, "funnel"); err != nil || len(rows) != 1 {
		t.Fatalf("analysis rows=%v err=%v", rows, err)
	}
	if _, err := Rows(summary, "missing"); apperror.CodeOf(err) != "backend.report.export_analysis_not_allowed" {
		t.Fatalf("missing analysis err=%v", err)
	}
	content, err := EncodePreflightCSV([]reportmodel.ReportResultRow{row}, []string{"status", "orders", "empty"}, map[string]bool{"status": true, "empty": true})
	if err != nil || string(content) != "status,orders,empty\n******,2,\n" {
		t.Fatalf("csv=%q err=%v", content, err)
	}
}

func TestNormalizeScopeRejectsEveryUntrustedSelector(t *testing.T) {
	principal := exportPrincipal()
	base := func() (reportmodel.ReportSchema, reportmodel.ReportExportScopeRequest) {
		return exportReport(), exportRequest()
	}
	tests := []struct {
		name string
		code string
		edit func(*reportmodel.ReportSchema, *reportmodel.ReportExportScopeRequest, *principalmodel.Principal)
	}{
		{"purpose", "backend.report.export_scope_invalid", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.Purpose = ""
		}},
		{"purpose max", "backend.report.export_scope_invalid", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.Purpose = strings.Repeat("p", 513)
		}},
		{"long query", "backend.report.export_scope_invalid", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.QueryKey = strings.Repeat("q", 129)
		}},
		{"long analysis", "backend.report.export_scope_invalid", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.AnalysisKey = strings.Repeat("a", 129)
		}},
		{"query unsupported", "backend.report.query_not_allowed", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.QueryKey = "saved-search"
		}},
		{"role", "backend.report.export_scope_role_mismatch", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.RoleKey = "other"
		}},
		{"permission", "backend.permission.denied", func(_ *reportmodel.ReportSchema, _ *reportmodel.ReportExportScopeRequest, p *principalmodel.Principal) {
			accessfixture.Set(p, accessfixture.Bundle{})
		}},
		{"data permission read", "backend.permission.denied", func(_ *reportmodel.ReportSchema, _ *reportmodel.ReportExportScopeRequest, p *principalmodel.Principal) {
			accessfixture.Set(p, accessfixture.Bundle{
				Permissions:  []string{"order.export"},
				DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: "order", Scope: "all_records", Read: false}},
			})
		}},
		{"data scope", "backend.report.export_scope_data_mismatch", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.DataScopes = map[string]string{"order": "own"}
		}},
		{"analysis", "backend.report.export_analysis_not_allowed", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.AnalysisKey = "missing"
		}},
		{"filters max", "backend.report.export_scope_invalid", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.Filters = make([]reportmodel.ReportExportFilter, 33)
		}},
		{"filter dimension", "backend.report.export_filter_not_allowed", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.Filters = []reportmodel.ReportExportFilter{{DimensionKey: "missing", Operator: "eq", Values: []string{"x"}}}
		}},
		{"filter operator", "backend.report.export_filter_not_allowed", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.Filters = []reportmodel.ReportExportFilter{{DimensionKey: "status", Operator: "sql", Values: []string{"x"}}}
		}},
		{"filter value", "backend.report.export_scope_invalid", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.Filters = []reportmodel.ReportExportFilter{{DimensionKey: "status", Operator: "eq", Values: []string{strings.Repeat("x", 1025)}}}
		}},
		{"date dimension", "backend.report.export_date_range_invalid", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.DateRange = &reportmodel.ReportExportDateRange{DimensionKey: "status", From: "2026-01-01", To: "2026-01-02"}
		}},
		{"date dimension missing", "backend.report.export_date_range_invalid", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.DateRange = &reportmodel.ReportExportDateRange{DimensionKey: "missing", From: "2026-01-01", To: "2026-01-02"}
		}},
		{"date parse", "backend.report.export_date_range_invalid", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.DateRange = &reportmodel.ReportExportDateRange{DimensionKey: "day", From: "invalid", To: "2026-01-02"}
		}},
		{"date to parse", "backend.report.export_date_range_invalid", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.DateRange = &reportmodel.ReportExportDateRange{DimensionKey: "day", From: "2026-01-01", To: "invalid"}
		}},
		{"date order", "backend.report.export_date_range_invalid", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.DateRange = &reportmodel.ReportExportDateRange{DimensionKey: "day", From: "2026-01-03", To: "2026-01-02"}
		}},
		{"timezone", "backend.report.export_timezone_invalid", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.TimeZone = "Not/AZone"
		}},
		{"tags unsupported", "backend.report.export_scope_invalid", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.Tags = []string{""}
		}},
		{"projection empty", "backend.report.export_projection_not_allowed", func(s *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			s.Dataset.Dimensions = nil
			s.Dataset.Measures = nil
			r.FieldProjection = nil
		}},
		{"projection unknown", "backend.report.export_projection_not_allowed", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.FieldProjection = []string{"unknown"}
		}},
		{"projection max", "backend.report.export_projection_not_allowed", func(s *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			s.Dataset.Dimensions = make([]reportmodel.ReportDatasetDimension, 129)
			r.FieldProjection = make([]string, 129)
			for i := range s.Dataset.Dimensions {
				key := fmt.Sprintf("dimension_%03d", i)
				s.Dataset.Dimensions[i] = reportmodel.ReportDatasetDimension{Key: key, Field: reportmodel.ReportDatasetField{SourceAlias: "order", FieldKey: key}}
				r.FieldProjection[i] = key
			}
		}},
		{"metric", "backend.report.export_metric_definition_mismatch", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.MetricDefinitions = []reportmodel.ReportMetricDefinitionRef{{Key: "orders", Version: "wrong"}}
		}},
		{"metric version", "backend.report.export_metric_definition_mismatch", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.MetricDefinitions = []reportmodel.ReportMetricDefinitionRef{{Key: "orders", Version: ""}}
		}},
		{"freshness", "backend.report.export_freshness_invalid", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.Freshness.Mode = "stale"
		}},
		{"freshness lag", "backend.report.export_freshness_invalid", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.Freshness.MaximumLagSeconds = -1
		}},
		{"freshness lag max", "backend.report.export_freshness_invalid", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.Freshness.MaximumLagSeconds = 86401
		}},
		{"snapshot filter", "backend.report.export_snapshot_scope_unsupported", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.Freshness.Mode = "snapshot"
			r.Filters = []reportmodel.ReportExportFilter{{DimensionKey: "status", Operator: "eq", Values: []string{"paid"}}}
		}},
		{"snapshot date", "backend.report.export_snapshot_scope_unsupported", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.Freshness.Mode = "snapshot"
			r.DateRange = &reportmodel.ReportExportDateRange{DimensionKey: "day", From: "2026-01-01", To: "2026-01-02"}
		}},
		{"snapshot timezone", "backend.report.export_snapshot_scope_unsupported", func(_ *reportmodel.ReportSchema, r *reportmodel.ReportExportScopeRequest, _ *principalmodel.Principal) {
			r.Freshness.Mode = "snapshot"
			r.TimeZone = "Asia/Shanghai"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, request := base()
			currentPrincipal := principal
			test.edit(&report, &request, &currentPrincipal)
			_, _, _, err := NormalizeScope(report, "order", reportmodel.ReportExportControlSchema{}, request, currentPrincipal)
			if apperror.CodeOf(err) != test.code {
				t.Fatalf("code=%s err=%v", apperror.CodeOf(err), err)
			}
		})
	}
}

func TestNormalizeScopeAppliesOnlyReportOwnedQueryAndTagContracts(t *testing.T) {
	report := exportReport()
	report.ExportScope = &reportmodel.ReportExportScopeSchema{
		Query: &reportmodel.ReportExportQueryScopeSchema{Mode: "any", Predicates: []reportmodel.ReportExportQueryPredicate{
			{Field: reportmodel.ReportDatasetField{SourceAlias: "order", FieldKey: "order_no"}, Operator: "contains"},
			{Field: reportmodel.ReportDatasetField{SourceAlias: "order", FieldKey: "customer_name"}, Operator: "starts_with"},
		}},
		Tags: &reportmodel.ReportExportTagScopeSchema{
			Join:        reportmodel.ReportDatasetJoin{Alias: "export_tags", ObjectKey: "tag_assignment", Type: "inner", LeftAlias: "order", LeftField: "id", RightField: "target_id", Cardinality: "one_to_many"},
			FamilyJoin:  &reportmodel.ReportDatasetJoin{Alias: "export_tag_definitions", ObjectKey: "tag_definition", Type: "inner", LeftAlias: "export_tags", LeftField: "tag_definition_id", RightField: "id", Cardinality: "many_to_one"},
			TargetField: reportmodel.ReportDatasetField{SourceAlias: "order", FieldKey: "id"}, TagField: reportmodel.ReportDatasetField{SourceAlias: "export_tag_definitions", FieldKey: "stable_key"},
			AllowedMatchModes: []string{"any", "all"}, DefaultMatchMode: "all",
			FixedFilters: []reportmodel.ReportDatasetFilter{{Field: reportmodel.ReportDatasetField{SourceAlias: "export_tags", FieldKey: "active"}, Operator: "eq", Value: true}},
		},
	}
	request := exportRequest()
	request.QueryKey = "  ACME   188  "
	request.Tags = []string{"priority", " cold ", "priority"}
	request.TagMatch = " ANY "
	control := reportmodel.ReportExportControlSchema{SourceObjects: []string{"order", "tag_assignment", "tag_definition"}}
	scope, scoped, _, err := NormalizeScope(report, "order", control, request, exportPrincipal())
	if err != nil {
		t.Fatal(err)
	}
	if scope.QueryKey != "acme 188" || strings.Join(scope.Tags, ",") != "cold,priority" || scope.TagMatch != "any" || scope.DataScopes["tag_assignment"] != "identity_policy" || scope.DataScopes["tag_definition"] != "identity_policy" {
		t.Fatalf("scope=%#v", scope)
	}
	if scoped.Dataset.RuntimeQuery == nil || scoped.Dataset.RuntimeTags == nil || len(scoped.Dataset.Joins) != 2 || !scoped.Dataset.Joins[0].RuntimeScopeOnly || !scoped.Dataset.Joins[1].RuntimeScopeOnly || len(scoped.Dataset.Filters) != 1 {
		t.Fatalf("scoped dataset=%#v", scoped.Dataset)
	}
	equivalent := request
	equivalent.QueryKey = "ACME 188"
	equivalent.Tags = []string{"cold", "priority"}
	equivalent.TagMatch = "any"
	equivalentScope, _, _, err := NormalizeScope(report, "order", control, equivalent, exportPrincipal())
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := CanonicalJSONSHA256(scope)
	equivalentHash, _ := CanonicalJSONSHA256(equivalentScope)
	if hash != equivalentHash {
		t.Fatalf("canonical scope hashes differ: %s != %s", hash, equivalentHash)
	}
	if _, _, _, err := NormalizeScope(report, "order", reportmodel.ReportExportControlSchema{SourceObjects: []string{"order"}}, request, exportPrincipal()); apperror.CodeOf(err) != "backend.report.export_control_invalid" {
		t.Fatalf("missing governed tag source error=%v", err)
	}

	unsafe := report
	unsafe.ExportScope.Query.Predicates[0].Operator = "sql"
	if _, _, _, err := NormalizeScope(unsafe, "order", control, request, exportPrincipal()); apperror.CodeOf(err) != "backend.report.export_query_definition_invalid" {
		t.Fatalf("unsafe query error=%v", err)
	}
	report.ExportScope.Query.Predicates[0].Operator = "contains"
	unsafe = report
	unsafe.ExportScope.Tags.Join.Type = "cross"
	if _, _, _, err := NormalizeScope(unsafe, "order", control, request, exportPrincipal()); apperror.CodeOf(err) != "backend.report.export_tag_definition_invalid" {
		t.Fatalf("unsafe tag error=%v", err)
	}
	request.TagMatch = "xor"
	if _, _, _, err := NormalizeScope(report, "order", control, request, exportPrincipal()); apperror.CodeOf(err) != "backend.report.export_tag_match_not_allowed" {
		t.Fatalf("unknown tag match error=%v", err)
	}
}

func TestNormalizeScopeCanonicalizesAllAllowedScope(t *testing.T) {
	report, request, principal := exportReport(), exportRequest(), exportPrincipal()
	request.AnalysisKey = "funnel"
	request.Filters = []reportmodel.ReportExportFilter{{DimensionKey: "status", Operator: "in", Values: []string{"paid", "pending"}}, {DimensionKey: "status", Operator: "is_null"}}
	request.DateRange = &reportmodel.ReportExportDateRange{DimensionKey: "day", From: "2026-08-01", To: "2026-08-10"}
	request.TimeZone = "Asia/Shanghai"
	request.Freshness.Mode = ""
	scope, scoped, masked, err := NormalizeScope(report, "order", reportmodel.ReportExportControlSchema{}, request, principal)
	if err != nil || scope.QueryKey != "" || scope.Freshness.Mode != "realtime" || len(scoped.Dataset.Filters) != 3 || len(scoped.Dataset.Dimensions) != 1 || scoped.Dataset.Dimensions[0].Key != "status" || len(scoped.Dataset.Measures) != 1 || scoped.Dataset.Measures[0].Key != "orders" || len(masked) != 0 || len(scope.MetricDefinitions) != 1 {
		t.Fatalf("scope=%#v filters=%#v masked=%v err=%v", scope, scoped.Dataset.Filters, masked, err)
	}
	dateFilter := scoped.Dataset.Filters[2]
	if scoped.Dataset.TimeZone != "Asia/Shanghai" || len(dateFilter.Values) != 2 || dateFilter.Values[0] != "2026-07-31T16:00:00Z" || dateFilter.Values[1] != "2026-08-10T15:59:59.999999999Z" {
		t.Fatalf("timezone=%q date filter=%#v", scoped.Dataset.TimeZone, dateFilter)
	}
	request = exportRequest()
	request.FieldProjection = nil
	request.TimeZone = ""
	report.Dataset.TimeZone = ""
	if scope, _, _, err = NormalizeScope(report, "order", reportmodel.ReportExportControlSchema{}, request, principal); err != nil || scope.TimeZone != "UTC" || len(scope.FieldProjection) != 4 {
		t.Fatalf("default scope=%#v err=%v", scope, err)
	}
}

func TestNormalizeScopeEnforcesFieldMaskingPolicy(t *testing.T) {
	principal := exportPrincipal()
	setFieldPermissions := func(permissions ...accessfixture.FieldPolicyFixture) {
		accessfixture.Set(&principal, accessfixture.Bundle{
			Key: "report-exporter", Permissions: []string{"order.export"}, RecordScope: "all_records", FieldPolicies: permissions,
		})
	}
	setFieldPermissions(accessfixture.FieldPolicyFixture{ObjectKey: "order", FieldKey: "status", Read: true, Export: true, Masked: true})
	report := reportmodel.ReportSchema{Key: "orders", Dataset: reportmodel.ReportDatasetSchema{
		Source:     reportmodel.ReportDatasetSource{ObjectKey: "order", Alias: "order"},
		Dimensions: []reportmodel.ReportDatasetDimension{{Key: "status", Field: reportmodel.ReportDatasetField{SourceAlias: "order", FieldKey: "status"}}},
		Measures:   []reportmodel.ReportDatasetMeasure{{Key: "orders", Operation: "count", SourceAlias: "order"}},
	}}
	request := reportmodel.ReportExportScopeRequest{Purpose: "mask", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}}
	if _, _, masked, err := NormalizeScope(report, "order", reportmodel.ReportExportControlSchema{MaskingRequired: true}, request, principal); err != nil || !masked["status"] {
		t.Fatalf("masked=%v err=%v", masked, err)
	}
	if _, _, _, err := NormalizeScope(report, "order", reportmodel.ReportExportControlSchema{}, request, principal); apperror.CodeOf(err) != "backend.report.export_field_denied" {
		t.Fatalf("masking control err=%v", err)
	}
	setFieldPermissions(accessfixture.FieldPolicyFixture{ObjectKey: "order", FieldKey: "status", Read: true, Export: false})
	if _, _, _, err := NormalizeScope(report, "order", reportmodel.ReportExportControlSchema{}, request, principal); apperror.CodeOf(err) != "backend.report.export_field_denied" {
		t.Fatalf("field permission err=%v", err)
	}
	amount := reportmodel.ReportDatasetField{SourceAlias: "order", FieldKey: "amount"}
	report.Dataset.Dimensions = nil
	report.Dataset.Measures = []reportmodel.ReportDatasetMeasure{{Key: "amount", Operation: "sum", Field: &amount}}
	setFieldPermissions(accessfixture.FieldPolicyFixture{ObjectKey: "order", FieldKey: "amount", Read: true, Export: false})
	if _, _, _, err := NormalizeScope(report, "order", reportmodel.ReportExportControlSchema{}, request, principal); apperror.CodeOf(err) != "backend.report.export_sensitive_measure_denied" {
		t.Fatalf("measure export err=%v", err)
	}
	setFieldPermissions(accessfixture.FieldPolicyFixture{ObjectKey: "order", FieldKey: "amount", Read: true, Export: true, Masked: true})
	if _, _, _, err := NormalizeScope(report, "order", reportmodel.ReportExportControlSchema{MaskingRequired: true}, request, principal); apperror.CodeOf(err) != "backend.report.export_sensitive_measure_denied" {
		t.Fatalf("measure mask err=%v", err)
	}
}

func TestValidateFieldAccessAndArtifactIntegrity(t *testing.T) {
	principal := exportPrincipal()
	field := func(key string) reportmodel.ReportDatasetField {
		return reportmodel.ReportDatasetField{SourceAlias: "order", FieldKey: key}
	}
	event := field("event")
	report := exportReport()
	report.Dataset.Filters = []reportmodel.ReportDatasetFilter{{Field: field("filter")}}
	report.Dataset.Measures[1].StartField, report.Dataset.Measures[1].EndField = ptrField(field("start")), ptrField(field("end"))
	report.Dataset.Privacy = &reportmodel.ReportDatasetPrivacy{EntityField: field("privacy")}
	report.Dataset.Analyses[0].EventField = &event
	report.Dataset.Joins = []reportmodel.ReportDatasetJoin{{Alias: "joined", ObjectKey: "joined", LeftAlias: "order", FieldEqualities: []reportmodel.ReportDatasetJoinFieldEquality{
		{LeftField: "left", RightField: "right"},
		{LeftField: "left_version", RightField: "right_version"},
	}}}
	domain := reportservice.NewReportDomainService(reportservice.ReportDependencies{Access: exportAccessStub{}})
	if masked, err := ValidateFieldAccess(t.Context(), domain, report, reportmodel.ReportExportControlSchema{}, principal); err != nil || len(masked) != 2 {
		t.Fatalf("masked=%v err=%v", masked, err)
	}
	maskedDomain := reportservice.NewReportDomainService(reportservice.ReportDependencies{Access: exportAccessStub{masked: "status"}})
	if masked, err := ValidateFieldAccess(t.Context(), maskedDomain, exportReport(), reportmodel.ReportExportControlSchema{MaskingRequired: true}, principal); err != nil || !masked["status"] {
		t.Fatalf("masked=%v err=%v", masked, err)
	}
	if _, err := ValidateFieldAccess(t.Context(), maskedDomain, exportReport(), reportmodel.ReportExportControlSchema{}, principal); apperror.CodeOf(err) != "backend.report.export_sensitive_measure_denied" {
		t.Fatalf("masked denial=%v", err)
	}
	bad := exportReport()
	bad.Dataset.Dimensions[0].Field.SourceAlias = "missing"
	if _, err := ValidateFieldAccess(t.Context(), domain, bad, reportmodel.ReportExportControlSchema{}, principal); apperror.CodeOf(err) != "backend.report.export_field_not_found" {
		t.Fatalf("bad field=%v", err)
	}
	bad = exportReport()
	bad.Dataset.Dimensions[0].Field.FieldKey = ""
	if _, err := ValidateFieldAccess(t.Context(), domain, bad, reportmodel.ReportExportControlSchema{}, principal); apperror.CodeOf(err) != "backend.report.export_field_not_found" {
		t.Fatalf("blank field=%v", err)
	}
	maskedFilter := exportReport()
	maskedFilter.Dataset.Filters = []reportmodel.ReportDatasetFilter{{Field: field("status")}}
	if _, err := ValidateFieldAccess(t.Context(), maskedDomain, maskedFilter, reportmodel.ReportExportControlSchema{MaskingRequired: true}, principal); apperror.CodeOf(err) != "backend.report.export_sensitive_measure_denied" {
		t.Fatalf("masked filter=%v", err)
	}
	want := errors.New("authorizer failed")
	failing := reportservice.NewReportDomainService(reportservice.ReportDependencies{Access: exportAccessStub{denied: "status", err: want}})
	if _, err := ValidateFieldAccess(t.Context(), failing, exportReport(), reportmodel.ReportExportControlSchema{}, principal); !errors.Is(err, want) {
		t.Fatalf("authorizer err=%v", err)
	}
	for _, denied := range []string{"filter", "amount", "privacy", "customer_id", "occurred_at", "event", "left", "right", "left_version", "right_version"} {
		t.Run("denied "+denied, func(t *testing.T) {
			deniedDomain := reportservice.NewReportDomainService(reportservice.ReportDependencies{Access: exportAccessStub{denied: denied}})
			if _, err := ValidateFieldAccess(t.Context(), deniedDomain, report, reportmodel.ReportExportControlSchema{}, principal); err == nil {
				t.Fatalf("field %s was accepted", denied)
			}
		})
	}

	scope, _, _, err := NormalizeScope(exportReport(), "order", reportmodel.ReportExportControlSchema{}, exportRequest(), principal)
	if err != nil {
		t.Fatal(err)
	}
	scopeHash, _ := CanonicalJSONSHA256(scope)
	authHash, _ := reportservice.ReportAccessScopeHash(principal)
	reportHash, _ := CanonicalJSONSHA256(exportReport())
	controlHash, _ := CanonicalJSONSHA256(reportmodel.ReportExportControlSchema{})
	snapshot := reportmodel.ReportExportAuthorizationSnapshot{ObjectKey: "order", Scope: scope, ScopeSHA256: scopeHash, AuthorizationScopeSHA256: authHash, ReportDefinitionSHA256: reportHash, ControlDefinitionSHA256: controlHash}
	if err := ValidateCurrentExportAuthorization(t.Context(), domain, exportReport(), reportmodel.ReportExportControlSchema{}, snapshot, principal); err != nil {
		t.Fatal(err)
	}
	snapshot.ScopeSHA256 = "changed"
	if err := ValidateCurrentExportAuthorization(t.Context(), domain, exportReport(), reportmodel.ReportExportControlSchema{}, snapshot, principal); apperror.CodeOf(err) != "backend.report.export_scope_changed" {
		t.Fatalf("integrity err=%v", err)
	}
	snapshot.Scope.Purpose = ""
	if err := ValidateCurrentExportAuthorization(t.Context(), domain, exportReport(), reportmodel.ReportExportControlSchema{}, snapshot, principal); apperror.CodeOf(err) != "backend.report.export_scope_invalid" {
		t.Fatalf("invalid scope err=%v", err)
	}
	snapshot.Scope = scope
	snapshot.ScopeSHA256 = scopeHash
	snapshot.ReportDefinitionSHA256 = "changed"
	if err := ValidateCurrentExportAuthorization(t.Context(), domain, exportReport(), reportmodel.ReportExportControlSchema{}, snapshot, principal); apperror.CodeOf(err) != "backend.report.export_scope_changed" {
		t.Fatalf("report definition integrity err=%v", err)
	}
	snapshot.ReportDefinitionSHA256 = reportHash
	snapshot.ControlDefinitionSHA256 = "changed"
	if err := ValidateCurrentExportAuthorization(t.Context(), domain, exportReport(), reportmodel.ReportExportControlSchema{}, snapshot, principal); apperror.CodeOf(err) != "backend.report.export_scope_changed" {
		t.Fatalf("control definition integrity err=%v", err)
	}
	snapshot.ControlDefinitionSHA256 = controlHash
	if err := ValidateCurrentExportAuthorization(t.Context(), failing, exportReport(), reportmodel.ReportExportControlSchema{}, snapshot, principal); !errors.Is(err, want) {
		t.Fatalf("current field access err=%v", err)
	}
}

func ptrField(field reportmodel.ReportDatasetField) *reportmodel.ReportDatasetField { return &field }
