package report

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/go-sql-driver/mysql"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
)

func TestReportObjectSQLKeysetGroupedDateBucketRunsOnMySQL(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("RUNTIME_MYSQL_TEST_DSN"))
	if dsn == "" {
		t.Skip("RUNTIME_MYSQL_TEST_DSN is not configured")
	}
	database, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	bucket := reportmodel.ReportObjectSQLExpression{
		Kind: "function", Name: "date_bucket", Value: "day", Type: "datetime",
		Arguments: []reportmodel.ReportObjectSQLExpression{{Kind: "field", Alias: "touch", FieldKey: "occurred_at", Type: "datetime"}},
	}
	kind := reportmodel.ReportObjectSQLExpression{Kind: "field", Alias: "touch", FieldKey: "kind", Type: "text"}
	plan := reportmodel.ReportObjectSQLPlan{
		Sources: []reportmodel.ReportObjectSQLSource{{Alias: "touch"}},
		Projections: []reportmodel.ReportObjectSQLProjection{
			{Alias: "day_bucket", Expression: bucket},
			{Alias: "kind", Expression: kind},
			{Alias: "activity_count", Expression: reportmodel.ReportObjectSQLExpression{Kind: "aggregate", Name: "count", Type: "integer", Arguments: []reportmodel.ReportObjectSQLExpression{{Kind: "field", Alias: "touch", FieldKey: "id", Type: "text"}}}},
		},
		GroupBy: []reportmodel.ReportObjectSQLExpression{bucket, kind},
		OrderBy: []reportmodel.ReportObjectSQLOrder{
			{Expression: reportmodel.ReportObjectSQLExpression{Kind: "result", Alias: "day_bucket"}, Direction: "asc"},
			{Expression: reportmodel.ReportObjectSQLExpression{Kind: "result", Alias: "kind"}, Direction: "asc"},
			{Expression: bucket, Direction: "asc"},
			{Expression: kind, Direction: "asc"},
		},
		Limit: 100,
	}
	args := []any{}
	emitter := reportObjectSQLEmitter{dialect: objectSQLGoldenDialect{driver: "mysql"}, profile: reportTestEngineProfile("mysql"), args: &args}
	statement, err := emitter.statementKeysetPage(plan, []string{
		"`report_object_sql_source_0` AS (SELECT 'touch-1' AS `id`, 1792530000000 AS `occurred_at`, 'meeting' AS `kind` UNION ALL SELECT 'touch-2', 1792533600000, 'meeting')",
	}, nil, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := database.QueryContext(t.Context(), statement, args...)
	if err != nil {
		t.Fatalf("execute grouped MySQL keyset page: %v\n%s", err, statement)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		t.Fatal("grouped MySQL keyset page returned no rows")
	}
	values := make([]any, 3+len(plan.OrderBy)*2)
	destinations := make([]any, len(values))
	for index := range values {
		destinations[index] = &values[index]
	}
	if err := rows.Scan(destinations...); err != nil {
		t.Fatal(err)
	}
}
