package report

import (
	"reflect"
	"strings"
	"testing"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
)

func TestReportContainsUsesORMEscapingAndExistingParameterOffsets(t *testing.T) {
	expression := reportmodel.ReportObjectSQLExpression{Kind: "function", Name: "contains", Type: "boolean", Arguments: []reportmodel.ReportObjectSQLExpression{
		{Kind: "field", Alias: "v", FieldKey: "customer", Type: "text"}, {Kind: "parameter", Name: "keyword", Type: "text"},
	}}
	for _, driver := range []string{"sqlite", "postgres", "mysql"} {
		t.Run(driver, func(t *testing.T) {
			dialect := objectSQLGoldenDialect{driver: driver}
			args := []any{"workspace-a", "store-a"}
			emitter := reportObjectSQLEmitter{dialect: dialect, profile: reportTestEngineProfile(driver), args: &args, parameters: map[string]any{"keyword": " 50%_off~猫 ' "}}
			sql, err := emitter.expression(expression)
			want := "(" + dialect.Identifier("v") + "." + dialect.Identifier("customer") + " LIKE " + dialect.Placeholder(3) + " ESCAPE '~')"
			if err != nil || sql != want || !reflect.DeepEqual(args, []any{"workspace-a", "store-a", "% 50~%~_off~~猫 ' %"}) {
				t.Fatalf("sql=%q args=%#v error=%v", sql, args, err)
			}
			emitter.parameters["keyword"] = nil
			if sql, err := emitter.expression(expression); err != nil || sql != "NULL" || len(args) != 3 {
				t.Fatalf("null sql=%q error=%v", sql, err)
			}
			emitter.parameters["keyword"] = 1
			if _, err := emitter.expression(expression); err == nil {
				t.Fatal("non-text value reached SQL")
			}
			delete(emitter.parameters, "keyword")
			if _, err := emitter.expression(expression); err == nil || !strings.Contains(err.Error(), "missing bound report parameter") {
				t.Fatalf("missing parameter: %v", err)
			}
		})
	}
	for _, arguments := range [][]reportmodel.ReportObjectSQLExpression{
		nil, expression.Arguments[:1],
		{{Kind: "literal", Type: "text"}, expression.Arguments[1]},
		{expression.Arguments[0], {Kind: "field", Type: "text"}},
		{{Kind: "field", Alias: "v", FieldKey: "amount", Type: "integer"}, expression.Arguments[1]},
	} {
		invalid := expression
		invalid.Arguments = arguments
		emitter := reportObjectSQLEmitter{}
		if _, err := emitter.expression(invalid); err == nil {
			t.Fatalf("invalid bound expression accepted: %+v", invalid)
		}
	}
}
