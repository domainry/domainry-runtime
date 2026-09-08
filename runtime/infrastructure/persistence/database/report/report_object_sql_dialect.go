package report

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
)

type reportObjectSQLDialect interface {
	Identifier(string) string
	Placeholder(int) string
}

type reportObjectSQLEmitter struct {
	dialect    reportObjectSQLDialect
	profile    persistencedriver.EngineProfile
	parameters map[string]any
	args       *[]any
}

func (e *reportObjectSQLEmitter) statement(plan reportmodel.ReportObjectSQLPlan, ctes []string) (string, error) {
	if len(plan.Sources) == 0 || len(ctes) != len(plan.Sources) {
		return "", fmt.Errorf("invalid report object SQL sources")
	}
	selectParts := make([]string, 0, len(plan.Projections))
	for _, projection := range plan.Projections {
		expression, err := e.expression(projection.Expression)
		if err != nil {
			return "", err
		}
		selectParts = append(selectParts, expression+" AS "+e.dialect.Identifier(projection.Alias))
	}
	from := e.dialect.Identifier(reportObjectSQLCTE(0)) + " " + e.dialect.Identifier(plan.Sources[0].Alias)
	for index, source := range plan.Sources[1:] {
		if source.On == nil {
			return "", fmt.Errorf("missing report object SQL join condition")
		}
		on, err := e.expression(*source.On)
		if err != nil {
			return "", err
		}
		keyword := " JOIN "
		if source.JoinType == "left" {
			keyword = " LEFT JOIN "
		}
		from += keyword + e.dialect.Identifier(reportObjectSQLCTE(index+1)) + " " + e.dialect.Identifier(source.Alias) + " ON " + on
	}
	statement := "WITH " + strings.Join(ctes, ", ") + " SELECT " + strings.Join(selectParts, ", ") + " FROM " + from
	if plan.Where != nil {
		value, err := e.expression(*plan.Where)
		if err != nil {
			return "", err
		}
		statement += " WHERE " + value
	}
	if len(plan.GroupBy) > 0 {
		values := make([]string, len(plan.GroupBy))
		for index, expression := range plan.GroupBy {
			value, err := e.expression(expression)
			if err != nil {
				return "", err
			}
			values[index] = value
		}
		statement += " GROUP BY " + strings.Join(values, ", ")
	}
	if plan.Having != nil {
		value, err := e.expression(*plan.Having)
		if err != nil {
			return "", err
		}
		statement += " HAVING " + value
	}
	if len(plan.OrderBy) > 0 {
		values := make([]string, len(plan.OrderBy))
		for index, order := range plan.OrderBy {
			value, err := e.expression(order.Expression)
			if err != nil {
				return "", err
			}
			values[index] = value + " " + strings.ToUpper(order.Direction)
		}
		statement += " ORDER BY " + strings.Join(values, ", ")
	}
	statement += " LIMIT " + strconv.Itoa(plan.Limit)
	return statement, nil
}

func (e *reportObjectSQLEmitter) expression(expression reportmodel.ReportObjectSQLExpression) (string, error) {
	switch expression.Kind {
	case "field":
		reference := e.dialect.Identifier(expression.Alias) + "." + e.dialect.Identifier(expression.FieldKey)
		if e.profile.OrderedDecimalTextStorage() && (expression.Type == "currency" || expression.Type == "decimal" && expression.Precision > 0) {
			return "runtime_decimal_minor(" + reference + ", " + strconv.Itoa(expression.Precision) + ")", nil
		}
		return reference, nil
	case "result":
		return e.dialect.Identifier(expression.Alias), nil
	case "parameter":
		value, exists := e.parameters[expression.Name]
		if !exists {
			return "", fmt.Errorf("missing bound report parameter %s", expression.Name)
		}
		if e.profile.OrderedDecimalTextStorage() && expression.Type == "currency" && value != nil {
			parsed, err := decimal.NewFromString(strings.TrimSpace(fmt.Sprint(value)))
			if err != nil {
				return "", err
			}
			minor := parsed.Shift(int32(expression.Scale)).BigInt()
			if !minor.IsInt64() {
				return "", fmt.Errorf("currency parameter exceeds SQLite exact range")
			}
			value = minor.Int64()
		}
		*e.args = append(*e.args, value)
		return e.dialect.Placeholder(len(*e.args)), nil
	case "literal":
		if expression.Type == "currency" && e.profile.OrderedDecimalTextStorage() {
			parsed, err := decimal.NewFromString(expression.Value)
			if err != nil {
				return "", err
			}
			return parsed.Shift(int32(expression.Scale)).BigInt().String(), nil
		}
		if expression.ValueType == "boolean" {
			if expression.Value == "true" {
				return "TRUE", nil
			}
			return "FALSE", nil
		}
		return expression.Value, nil
	case "null":
		return "NULL", nil
	case "binary", "comparison", "logical":
		left, right, err := e.binaryArguments(expression)
		if err != nil {
			return "", err
		}
		if e.profile.OrderedDecimalTextStorage() && expression.Kind == "binary" && expression.Operator == "/" && expression.Type == "currency" {
			return "runtime_currency_divide_minor(" + left + ", " + right + ")", nil
		}
		if e.profile.OrderedDecimalTextStorage() && expression.Kind == "binary" && expression.Operator == "*" && expression.Type == "decimal" && expression.Precision > 0 {
			return "runtime_decimal_multiply_minor(" + left + ", " + right + ")", nil
		}
		return "(" + left + " " + strings.ToUpper(expression.Operator) + " " + right + ")", nil
	case "unary":
		value, err := e.expression(expression.Arguments[0])
		if err != nil {
			return "", err
		}
		return "(" + expression.Operator + value + ")", nil
	case "between":
		if len(expression.Arguments) != 3 {
			return "", fmt.Errorf("invalid between expression")
		}
		left, err := e.expression(expression.Arguments[0])
		if err != nil {
			return "", err
		}
		from, err := e.expression(expression.Arguments[1])
		if err != nil {
			return "", err
		}
		to, err := e.expression(expression.Arguments[2])
		if err != nil {
			return "", err
		}
		return "(" + left + " " + strings.ToUpper(expression.Operator) + " " + from + " AND " + to + ")", nil
	case "is":
		value, err := e.expression(expression.Arguments[0])
		if err != nil {
			return "", err
		}
		return "(" + value + " " + strings.ToUpper(expression.Operator) + ")", nil
	case "not":
		value, err := e.expression(expression.Arguments[0])
		if err != nil {
			return "", err
		}
		return "(NOT " + value + ")", nil
	case "case":
		return e.caseExpression(expression)
	case "function":
		return e.functionExpression(expression)
	case "aggregate":
		return e.aggregateExpression(expression)
	default:
		return "", fmt.Errorf("unsupported bound report expression %q", expression.Kind)
	}
}

func (e *reportObjectSQLEmitter) binaryArguments(expression reportmodel.ReportObjectSQLExpression) (string, string, error) {
	if len(expression.Arguments) != 2 {
		return "", "", fmt.Errorf("invalid binary expression")
	}
	left, err := e.expression(expression.Arguments[0])
	if err != nil {
		return "", "", err
	}
	right, err := e.expression(expression.Arguments[1])
	if err != nil {
		return "", "", err
	}
	return left, right, nil
}

func (e *reportObjectSQLEmitter) caseExpression(expression reportmodel.ReportObjectSQLExpression) (string, error) {
	var result strings.Builder
	result.WriteString("CASE")
	for _, when := range expression.Whens {
		condition, err := e.expression(when.Condition)
		if err != nil {
			return "", err
		}
		value, err := e.expression(when.Value)
		if err != nil {
			return "", err
		}
		result.WriteString(" WHEN ")
		result.WriteString(condition)
		result.WriteString(" THEN ")
		result.WriteString(value)
	}
	if expression.Else != nil {
		value, err := e.expression(*expression.Else)
		if err != nil {
			return "", err
		}
		result.WriteString(" ELSE ")
		result.WriteString(value)
	}
	result.WriteString(" END")
	return result.String(), nil
}

func (e *reportObjectSQLEmitter) functionExpression(expression reportmodel.ReportObjectSQLExpression) (string, error) {
	if expression.Name == "date_bucket" {
		return e.dateBucket(expression)
	}
	if expression.Name == "contains" {
		return e.containsExpression(expression)
	}
	arguments := make([]string, len(expression.Arguments))
	for index, argument := range expression.Arguments {
		value, err := e.expression(argument)
		if err != nil {
			return "", err
		}
		arguments[index] = value
	}
	if e.profile.OrderedDecimalTextStorage() && expression.Name == "floor" && len(expression.Arguments) == 1 {
		argument := expression.Arguments[0]
		if argument.Type == "decimal" && argument.Precision > 0 && expression.Type == "integer" {
			return "runtime_decimal_floor_units(" + arguments[0] + ", " + strconv.Itoa(argument.Scale) + ")", nil
		}
	}
	if e.profile.OrderedDecimalTextStorage() && expression.Type == "currency" {
		switch expression.Name {
		case "round":
			if len(expression.Arguments) == 1 {
				return arguments[0], nil
			}
			scale, err := strconv.Atoi(expression.Arguments[1].Value)
			if err != nil || scale != expression.Scale {
				return "", fmt.Errorf("backend.report.object_sql_sqlite_currency_round_unsupported")
			}
			return arguments[0], nil
		case "floor":
			factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(expression.Scale)), nil).String()
			value := arguments[0]
			return "(CASE WHEN " + value + " >= 0 THEN (" + value + " / " + factor + ") * " + factor + " ELSE -((((-" + value + ") + " + factor + " - 1) / " + factor + ") * " + factor + ") END)", nil
		}
	}
	return strings.ToUpper(expression.Name) + "(" + strings.Join(arguments, ", ") + ")", nil
}

func (e *reportObjectSQLEmitter) aggregateExpression(expression reportmodel.ReportObjectSQLExpression) (string, error) {
	arguments := make([]string, len(expression.Arguments))
	for index, argument := range expression.Arguments {
		value, err := e.expression(argument)
		if err != nil {
			return "", err
		}
		arguments[index] = value
	}
	if expression.Name == "count" && len(arguments) == 0 {
		return "COUNT(*)", nil
	}
	prefix := ""
	if expression.Distinct {
		prefix = "DISTINCT "
	}
	if e.profile.OrderedDecimalTextStorage() && expression.Type == "currency" {
		name := map[string]string{"sum": "runtime_decimal_sum_minor", "avg": "runtime_decimal_avg_minor"}[expression.Name]
		if name != "" {
			return name + "(" + prefix + arguments[0] + ")", nil
		}
	}
	if e.profile.OrderedDecimalTextStorage() && expression.Type == "decimal" && expression.Precision > 0 {
		name := map[string]string{"sum": "runtime_decimal_sum_minor", "avg": "runtime_decimal_avg_minor"}[expression.Name]
		if name != "" {
			return name + "(" + prefix + arguments[0] + ")", nil
		}
	}
	return strings.ToUpper(expression.Name) + "(" + prefix + strings.Join(arguments, ", ") + ")", nil
}

func (e *reportObjectSQLEmitter) dateBucket(expression reportmodel.ReportObjectSQLExpression) (string, error) {
	value, err := e.expression(expression.Arguments[0])
	if err != nil {
		return "", err
	}
	timeZone := expression.TimeZone
	if timeZone == "" {
		timeZone = "UTC"
	}
	return e.profile.ReportDateBucket(value, expression.Value, timeZone, expression.Arguments[0].Type == "date")
}
