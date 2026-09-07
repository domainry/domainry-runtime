package report

import (
	"fmt"

	"github.com/domainry/domainry-orm/query"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	querypersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/query"
)

// Only qualified columns are rendered through this adapter; table resolution
// remains in the existing Runtime-owned, authorization-scoped report sources.
type reportPredicateRenderer struct{ reportObjectSQLDialect }

func (r reportPredicateRenderer) Table(name string) string { return r.Identifier(name) }

func (e *reportObjectSQLEmitter) containsExpression(expression reportmodel.ReportObjectSQLExpression) (string, error) {
	if len(expression.Arguments) != 2 {
		return "", fmt.Errorf("invalid bound report contains expression")
	}
	field, parameter := expression.Arguments[0], expression.Arguments[1]
	if field.Kind != "field" || field.Type != "text" || field.Alias == "" || field.FieldKey == "" || parameter.Kind != "parameter" || parameter.Type != "text" {
		return "", fmt.Errorf("report contains requires a text field and declared text parameter")
	}
	value, exists := e.parameters[parameter.Name]
	if !exists {
		return "", fmt.Errorf("missing bound report parameter %s", parameter.Name)
	}
	// Preserve SQL three-valued logic, including NOT CONTAINS with NULL input.
	if value == nil {
		return "NULL", nil
	}
	literal, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("report contains parameter %s must be text", parameter.Name)
	}
	predicate := querypersistence.LiteralContainsPredicate(query.QualifiedColumn(field.Alias, field.FieldKey), literal)
	prepared, bound, err := query.PreparePredicate(reportPredicateRenderer{e.dialect}, predicate, len(*e.args))
	if err != nil {
		return "", err
	}
	*e.args = append(*e.args, bound...)
	return "(" + prepared + ")", nil
}
