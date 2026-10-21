package report

import (
	"strings"
	"testing"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
)

func TestReportObjectSQLKeysetPageUsesStableCompositeCursorWithoutOffset(t *testing.T) {
	args := []any{}
	emitter := reportObjectSQLEmitter{dialect: objectSQLGoldenDialect{driver: "sqlite"}, profile: reportTestEngineProfile("sqlite"), args: &args}
	plan := reportmodel.ReportObjectSQLPlan{
		Sources: []reportmodel.ReportObjectSQLSource{{Alias: "l"}},
		Projections: []reportmodel.ReportObjectSQLProjection{
			{Alias: "created_at", Expression: reportmodel.ReportObjectSQLExpression{Kind: "field", Alias: "l", FieldKey: "created_at"}},
			{Alias: "id", Expression: reportmodel.ReportObjectSQLExpression{Kind: "field", Alias: "l", FieldKey: "id"}},
		},
		OrderBy: []reportmodel.ReportObjectSQLOrder{
			{Expression: reportmodel.ReportObjectSQLExpression{Kind: "result", Alias: "created_at"}, Direction: "desc"},
			{Expression: reportmodel.ReportObjectSQLExpression{Kind: "field", Alias: "l", FieldKey: "id"}, Direction: "asc"},
		},
		Limit: 100,
	}
	statement, err := emitter.statementKeysetPage(plan, []string{`"source" AS (SELECT 1)`}, []any{"2026-08-30T00:00:00Z", "id-20"}, 20, 10)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToUpper(statement), " OFFSET ") {
		t.Fatalf("keyset statement contains OFFSET: %s", statement)
	}
	for _, required := range []string{`"__cursor_null_0" ASC`, `"__cursor_value_0" DESC`, `"__cursor_value_1" ASC`, `"__cursor_value_0" < ?`, `"__cursor_value_1" > ?`, "LIMIT 11"} {
		if !strings.Contains(statement, required) {
			t.Fatalf("keyset statement missing %q: %s", required, statement)
		}
	}
	if len(args) != 10 {
		t.Fatalf("keyset args=%#v", args)
	}
}

func TestReportObjectSQLKeysetPageHandlesNullOrderValue(t *testing.T) {
	args := []any{}
	emitter := reportObjectSQLEmitter{dialect: objectSQLGoldenDialect{driver: "sqlite"}, profile: reportTestEngineProfile("sqlite"), args: &args}
	plan := reportmodel.ReportObjectSQLPlan{
		Sources:     []reportmodel.ReportObjectSQLSource{{Alias: "l"}},
		Projections: []reportmodel.ReportObjectSQLProjection{{Alias: "value", Expression: reportmodel.ReportObjectSQLExpression{Kind: "field", Alias: "l", FieldKey: "value"}}},
		OrderBy: []reportmodel.ReportObjectSQLOrder{
			{Expression: reportmodel.ReportObjectSQLExpression{Kind: "result", Alias: "value"}, Direction: "asc"},
			{Expression: reportmodel.ReportObjectSQLExpression{Kind: "field", Alias: "l", FieldKey: "id"}, Direction: "asc"},
		},
		Limit: 20,
	}
	statement, err := emitter.statementKeysetPage(plan, []string{`"source" AS (SELECT 1)`}, []any{nil, "id-2"}, 2, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(statement, `"__cursor_value_0" IS NULL`) || strings.Contains(statement, `"__cursor_value_0" >`) {
		t.Fatalf("NULL keyset predicate is not stable: %s", statement)
	}
}

func TestReportObjectSQLKeysetPageRanksGroupedValuesAfterAggregation(t *testing.T) {
	args := []any{}
	emitter := reportObjectSQLEmitter{dialect: objectSQLGoldenDialect{driver: "mysql"}, profile: reportTestEngineProfile("mysql"), args: &args}
	bucket := reportmodel.ReportObjectSQLExpression{
		Kind: "function", Name: "date_bucket", Value: "day", Type: "datetime",
		Arguments: []reportmodel.ReportObjectSQLExpression{{Kind: "field", Alias: "touch", FieldKey: "occurred_at", Type: "datetime"}},
	}
	plan := reportmodel.ReportObjectSQLPlan{
		Sources: []reportmodel.ReportObjectSQLSource{{Alias: "touch"}},
		Projections: []reportmodel.ReportObjectSQLProjection{
			{Alias: "day_bucket", Expression: bucket},
			{Alias: "activity_count", Expression: reportmodel.ReportObjectSQLExpression{Kind: "aggregate", Name: "count", Type: "integer", Arguments: []reportmodel.ReportObjectSQLExpression{{Kind: "field", Alias: "touch", FieldKey: "id", Type: "text"}}}},
		},
		GroupBy: []reportmodel.ReportObjectSQLExpression{bucket},
		OrderBy: []reportmodel.ReportObjectSQLOrder{{Expression: reportmodel.ReportObjectSQLExpression{Kind: "result", Alias: "day_bucket"}, Direction: "asc"}},
		Limit:   100,
	}
	statement, err := emitter.statementKeysetPage(plan, []string{"`source` AS (SELECT 1)"}, nil, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	wantRank := "CASE WHEN `__cursor_value_0` IS NULL THEN 1 ELSE 0 END AS `__cursor_null_0`"
	if !strings.Contains(statement, "`domainry_report_base` AS (") || !strings.Contains(statement, "`domainry_report_page` AS (SELECT") || !strings.Contains(statement, wantRank) {
		t.Fatalf("grouped keyset statement does not rank the aggregated alias in an outer layer: %s", statement)
	}
	rankStart := strings.Index(statement, wantRank)
	if rankStart < 0 || strings.Contains(statement[rankStart:rankStart+len(wantRank)], "occurred_at") {
		t.Fatalf("cursor NULL rank still references the raw grouped field: %s", statement)
	}
}
