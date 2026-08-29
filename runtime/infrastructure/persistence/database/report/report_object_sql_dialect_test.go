package report

import (
	"fmt"
	"strings"
	"testing"

	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

type objectSQLGoldenDialect struct{ driver string }

func (d objectSQLGoldenDialect) Driver() string { return d.driver }
func (d objectSQLGoldenDialect) Identifier(value string) string {
	if d.driver == "mysql" {
		return "`" + strings.ReplaceAll(value, "`", "``") + "`"
	}
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
func (d objectSQLGoldenDialect) Placeholder(position int) string {
	if d.driver == "postgres" {
		return fmt.Sprintf("$%d", position)
	}
	return "?"
}

func TestReportObjectSQLDialectGoldens(t *testing.T) {
	plan := reportmodel.ReportObjectSQLPlan{
		Sources: []reportmodel.ReportObjectSQLSource{
			{ObjectKey: "sale", Alias: "s", JoinType: "base"},
			{ObjectKey: "payment", Alias: "p", JoinType: "left", On: objectSQLBinary("comparison", "=", objectSQLField("s", "id", "text"), objectSQLField("p", "sale_id", "text"))},
		},
		Projections: []reportmodel.ReportObjectSQLProjection{
			{Alias: "sale_day", Expression: reportmodel.ReportObjectSQLExpression{Kind: "function", Name: "date_bucket", Value: "day", Type: "datetime", Arguments: []reportmodel.ReportObjectSQLExpression{objectSQLField("s", "sold_at", "datetime")}}},
			{Alias: "revenue", Expression: reportmodel.ReportObjectSQLExpression{Kind: "aggregate", Name: "sum", Type: "currency", Scale: 2, Arguments: []reportmodel.ReportObjectSQLExpression{{Kind: "field", Alias: "s", FieldKey: "total", Type: "currency", Precision: 18, Scale: 2}}}},
			{Alias: "average_ticket", Expression: reportmodel.ReportObjectSQLExpression{Kind: "binary", Operator: "/", Type: "currency", Precision: 18, Scale: 2, Arguments: []reportmodel.ReportObjectSQLExpression{
				{Kind: "aggregate", Name: "sum", Type: "currency", Precision: 18, Scale: 2, Arguments: []reportmodel.ReportObjectSQLExpression{{Kind: "field", Alias: "s", FieldKey: "total", Type: "currency", Precision: 18, Scale: 2}}},
				{Kind: "function", Name: "nullif", Type: "integer", Arguments: []reportmodel.ReportObjectSQLExpression{{Kind: "aggregate", Name: "count", Type: "integer"}, {Kind: "literal", Type: "integer", ValueType: "integer", Value: "0"}}},
			}}},
		},
		Where:   &reportmodel.ReportObjectSQLExpression{Kind: "comparison", Operator: ">=", Arguments: []reportmodel.ReportObjectSQLExpression{objectSQLField("s", "sold_at", "datetime"), {Kind: "parameter", Name: "from", Type: "datetime"}}},
		GroupBy: []reportmodel.ReportObjectSQLExpression{{Kind: "function", Name: "date_bucket", Value: "day", Type: "datetime", Arguments: []reportmodel.ReportObjectSQLExpression{objectSQLField("s", "sold_at", "datetime")}}},
		Limit:   20,
	}
	ctes := []string{`"src0" AS (SELECT 1)`, `"src1" AS (SELECT 1)`}
	wants := map[string][]string{
		"sqlite":   {`strftime('%Y-%m-%dT00:00:00Z', "s"."sold_at")`, `runtime_decimal_sum_minor(runtime_decimal_minor("s"."total", 18))`, `runtime_currency_divide_minor(runtime_decimal_sum_minor(runtime_decimal_minor("s"."total", 18)), NULLIF(COUNT(*), 0))`, `>= ?`},
		"postgres": {`DATE_TRUNC('day', CAST("s"."sold_at" AS TIMESTAMPTZ))`, `SUM("s"."total")`, `(SUM("s"."total") / NULLIF(COUNT(*), 0))`, `>= $1`},
		"mysql":    {"DATE_FORMAT(`s`.`sold_at`, '%Y-%m-%d 00:00:00')", "SUM(`s`.`total`)", "(SUM(`s`.`total`) / NULLIF(COUNT(*), 0))", ">= ?"},
	}
	for driver, fragments := range wants {
		t.Run(driver, func(t *testing.T) {
			args := []any{}
			emitter := reportObjectSQLEmitter{dialect: objectSQLGoldenDialect{driver: driver}, profile: reportTestEngineProfile(driver), parameters: map[string]any{"from": "2026-01-01T00:00:00Z"}, args: &args}
			statement, err := emitter.statement(plan, ctes)
			if err != nil {
				t.Fatal(err)
			}
			for _, fragment := range fragments {
				if !strings.Contains(statement, fragment) {
					t.Fatalf("%s SQL missing %q:\n%s", driver, fragment, statement)
				}
			}
			if len(args) != 1 || args[0] != "2026-01-01T00:00:00Z" {
				t.Fatalf("unexpected bound arguments: %#v", args)
			}
		})
	}
}

func TestReportObjectSQLDialectAppliesRuntimeBoundedPage(t *testing.T) {
	plan := reportmodel.ReportObjectSQLPlan{
		Sources:     []reportmodel.ReportObjectSQLSource{{ObjectKey: "ledger", Alias: "l"}},
		Projections: []reportmodel.ReportObjectSQLProjection{{Alias: "id", Expression: objectSQLField("l", "id", "text")}},
		OrderBy:     []reportmodel.ReportObjectSQLOrder{{Expression: objectSQLField("l", "id", "text"), Direction: "asc"}}, Limit: 1000,
	}
	args := []any{}
	emitter := reportObjectSQLEmitter{dialect: objectSQLGoldenDialect{driver: "postgres"}, profile: reportTestEngineProfile("postgres"), args: &args}
	statement, err := emitter.statementPage(plan, []string{`"src" AS (SELECT 1)`}, 400, 200)
	if err != nil || !strings.Contains(statement, `ORDER BY "l"."id" ASC LIMIT 201 OFFSET 400`) {
		t.Fatalf("bounded SQL=%q err=%v", statement, err)
	}
}

func TestReportObjectSQLPostgresDateBucketCastsTextBackedTemporalTypes(t *testing.T) {
	for _, test := range []struct {
		name      string
		fieldType string
		want      string
	}{
		{name: "date", fieldType: "date", want: `DATE_TRUNC('month', CAST("b"."business_date" AS DATE))`},
		{name: "datetime", fieldType: "datetime", want: `DATE_TRUNC('month', CAST("b"."occurred_at" AS TIMESTAMPTZ))`},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := []any{}
			emitter := reportObjectSQLEmitter{dialect: objectSQLGoldenDialect{driver: "postgres"}, profile: reportTestEngineProfile("postgres"), args: &args}
			got, err := emitter.dateBucket(reportmodel.ReportObjectSQLExpression{
				Kind:  "function",
				Name:  "date_bucket",
				Value: "month",
				Type:  "datetime",
				Arguments: []reportmodel.ReportObjectSQLExpression{
					objectSQLField("b", map[string]string{"date": "business_date", "datetime": "occurred_at"}[test.fieldType], test.fieldType),
				},
			})
			if err != nil || got != test.want {
				t.Fatalf("got=%q err=%v, want %q", got, err, test.want)
			}
		})
	}
}

func objectSQLField(alias, fieldKey, fieldType string) reportmodel.ReportObjectSQLExpression {
	return reportmodel.ReportObjectSQLExpression{Kind: "field", Alias: alias, FieldKey: fieldKey, Type: fieldType}
}

func objectSQLBinary(kind, operator string, left, right reportmodel.ReportObjectSQLExpression) *reportmodel.ReportObjectSQLExpression {
	return &reportmodel.ReportObjectSQLExpression{Kind: kind, Operator: operator, Arguments: []reportmodel.ReportObjectSQLExpression{left, right}}
}

func TestReportObjectSQLCurrencyResultSerializationKeepsDeclaredScale(t *testing.T) {
	column := reportmodel.ReportResultColumnSchema{Key: "average_ticket", Type: "currency", Precision: 19, Scale: 2}
	for _, test := range []struct {
		driver string
		raw    any
	}{
		{driver: "sqlite", raw: "505"},
		{driver: "postgres", raw: "5.05"},
		{driver: "mysql", raw: []byte("5.0500")},
	} {
		got, err := reportObjectSQLResultValue(reportTestEngineProfile(test.driver), column, test.raw)
		if err != nil || got != "5.05" {
			t.Fatalf("driver=%s got=%q err=%v", test.driver, got, err)
		}
	}
}

func TestReportObjectSQLNumberAndExactDecimalResultSerialization(t *testing.T) {
	number := reportmodel.ReportResultColumnSchema{Key: "quantity", Type: "number"}
	for _, test := range []struct {
		raw  any
		want string
	}{{float64(1.25), "1.25"}, {float32(2.5), "2.5"}, {int64(3), "3"}} {
		got, err := reportObjectSQLResultValue(reportTestEngineProfile("sqlite"), number, test.raw)
		if err != nil || got != test.want {
			t.Fatalf("number raw=%#v got=%q err=%v", test.raw, got, err)
		}
	}
	exact := reportmodel.ReportResultColumnSchema{Key: "rate", Type: "decimal", Precision: 8, Scale: 2}
	if got, err := reportObjectSQLResultValue(reportTestEngineProfile("sqlite"), exact, int64(750)); err != nil || got != "7.50" {
		t.Fatalf("exact percent got=%q err=%v", got, err)
	}
	if _, err := reportObjectSQLResultValue(reportTestEngineProfile("sqlite"), reportmodel.ReportResultColumnSchema{Key: "unsafe", Type: "decimal"}, float64(1.25)); err == nil {
		t.Fatal("floating value was accepted as exact decimal")
	}
}
