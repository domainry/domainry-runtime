package export

import (
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
)

func predicateReport() reportmodel.ReportSchema {
	report := exportReport()
	report.Dataset.QueryPredicates = []reportmodel.ReportDatasetPredicate{{Key: "current", Filters: []reportmodel.ReportDatasetFilter{{Field: reportmodel.ReportDatasetField{SourceAlias: "order", FieldKey: "status"}, Operator: "eq", Value: "current"}}}}
	report.Dataset.TagPredicates = []reportmodel.ReportDatasetPredicate{
		{Key: "high_value", Filters: []reportmodel.ReportDatasetFilter{{Field: reportmodel.ReportDatasetField{SourceAlias: "order", FieldKey: "amount"}, Operator: "gte", Value: "100"}}},
		{Key: "reviewed", Filters: []reportmodel.ReportDatasetFilter{{Field: reportmodel.ReportDatasetField{SourceAlias: "order", FieldKey: "reviewed_at"}, Operator: "not_null"}}},
	}
	return report
}

func TestApplyDeclaredPredicatesCanonicalizesAndIntersectsExportControl(t *testing.T) {
	report, query, tags, err := ApplyDeclaredPredicates(predicateReport(), " current ", []string{" reviewed ", "high_value", "reviewed"}, []string{"current"}, []string{"high_value", "reviewed"}, true)
	if err != nil || query != "current" || strings.Join(tags, ",") != "high_value,reviewed" || len(report.Dataset.Filters) != 3 {
		t.Fatalf("query=%q tags=%v filters=%#v err=%v", query, tags, report.Dataset.Filters, err)
	}
	for _, test := range []struct {
		name           string
		queryKey       string
		tags           []string
		allowedQueries []string
		allowedTags    []string
		code           string
	}{
		{"unknown query", "missing", nil, []string{"missing"}, nil, "backend.report.query_not_allowed"},
		{"query outside control", "current", nil, nil, nil, "backend.report.query_not_allowed"},
		{"unknown tag", "", []string{"missing"}, nil, []string{"missing"}, "backend.report.tag_not_allowed"},
		{"tag outside control", "", []string{"reviewed"}, nil, nil, "backend.report.tag_not_allowed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, _, err := ApplyDeclaredPredicates(predicateReport(), test.queryKey, test.tags, test.allowedQueries, test.allowedTags, true)
			if apperror.CodeOf(err) != test.code {
				t.Fatalf("code=%q err=%v", apperror.CodeOf(err), err)
			}
		})
	}
}

func TestApplyDeclaredPredicatesRejectsInvalidDeclarationAndUnboundedInput(t *testing.T) {
	invalid := predicateReport()
	invalid.Dataset.QueryPredicates[0].Filters[0].Operator = "sql"
	if _, _, _, err := ApplyDeclaredPredicates(invalid, "current", nil, nil, nil, false); apperror.CodeOf(err) != "backend.report.predicate_invalid" {
		t.Fatalf("operator err=%v", err)
	}
	duplicate := predicateReport()
	duplicate.Dataset.QueryPredicates = append(duplicate.Dataset.QueryPredicates, duplicate.Dataset.QueryPredicates[0])
	if _, _, _, err := ApplyDeclaredPredicates(duplicate, "current", nil, nil, nil, false); apperror.CodeOf(err) != "backend.report.predicate_invalid" {
		t.Fatalf("duplicate err=%v", err)
	}
	if _, _, _, err := ApplyDeclaredPredicates(predicateReport(), strings.Repeat("q", 129), nil, nil, nil, false); apperror.CodeOf(err) != "backend.report.export_scope_invalid" {
		t.Fatalf("query length err=%v", err)
	}
	if _, _, _, err := ApplyDeclaredPredicates(predicateReport(), "", []string{""}, nil, nil, false); apperror.CodeOf(err) != "backend.report.export_scope_invalid" {
		t.Fatalf("tag shape err=%v", err)
	}
}

func TestNormalizeScopeAppliesOnlyControlAllowlistedPredicates(t *testing.T) {
	request := exportRequest()
	request.QueryKey = "current"
	request.Tags = []string{"reviewed", "high_value"}
	control := reportmodel.ReportExportControlSchema{AllowedQueryKeys: []string{"current"}, AllowedTags: []string{"high_value", "reviewed"}}
	scope, report, _, err := NormalizeScope(predicateReport(), "order", control, request, exportPrincipal())
	if err != nil || scope.QueryKey != "current" || strings.Join(scope.Tags, ",") != "high_value,reviewed" || len(report.Dataset.Filters) != 3 {
		t.Fatalf("scope=%#v filters=%#v err=%v", scope, report.Dataset.Filters, err)
	}
	control.AllowedTags = []string{"reviewed"}
	if _, _, _, err := NormalizeScope(predicateReport(), "order", control, request, exportPrincipal()); apperror.CodeOf(err) != "backend.report.tag_not_allowed" {
		t.Fatalf("control err=%v", err)
	}
	request.Tags = nil
	request.Freshness.Mode = "snapshot"
	if _, _, _, err := NormalizeScope(predicateReport(), "order", control, request, exportPrincipal()); apperror.CodeOf(err) != "backend.report.export_snapshot_scope_unsupported" {
		t.Fatalf("snapshot predicate err=%v", err)
	}
}
