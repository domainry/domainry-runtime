package report

import (
	"fmt"
	"strconv"
	"strings"

	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

const reportObjectSQLPageCTE = "domainry_report_page"

// statementKeysetPage emits a bounded report page without OFFSET. Every
// published report plan already carries deterministic tie terms. The emitter
// projects those terms into an internal CTE and resumes with a lexicographic
// predicate over their last values.
func (e *reportObjectSQLEmitter) statementKeysetPage(plan reportmodel.ReportObjectSQLPlan, ctes []string, after []any, position, pageSize int) (string, error) {
	if len(plan.Sources) == 0 || len(ctes) != len(plan.Sources) {
		return "", fmt.Errorf("invalid report object SQL sources")
	}
	if position < 0 || pageSize < 1 || position > plan.Limit {
		return "", fmt.Errorf("invalid report object SQL keyset page")
	}
	if len(plan.OrderBy) == 0 || len(after) != 0 && len(after) != len(plan.OrderBy) {
		return "", fmt.Errorf("invalid report object SQL keyset cursor")
	}

	visible := make([]string, 0, len(plan.Projections))
	inner := make([]string, 0, len(plan.Projections)+len(plan.OrderBy)*2)
	for _, projection := range plan.Projections {
		expression, err := e.expression(projection.Expression)
		if err != nil {
			return "", err
		}
		alias := e.dialect.Identifier(projection.Alias)
		visible = append(visible, alias)
		inner = append(inner, expression+" AS "+alias)
	}
	for index, order := range plan.OrderBy {
		expression, err := e.reportKeysetOrderExpression(plan, order.Expression)
		if err != nil {
			return "", err
		}
		inner = append(inner,
			"CASE WHEN "+expression+" IS NULL THEN 1 ELSE 0 END AS "+e.dialect.Identifier(reportKeysetNullAlias(index)),
			expression+" AS "+e.dialect.Identifier(reportKeysetValueAlias(index)),
		)
	}

	from, err := e.reportKeysetFrom(plan)
	if err != nil {
		return "", err
	}
	pageBody := "SELECT " + strings.Join(inner, ", ") + " FROM " + from
	if plan.Where != nil {
		value, expressionErr := e.expression(*plan.Where)
		if expressionErr != nil {
			return "", expressionErr
		}
		pageBody += " WHERE " + value
	}
	if len(plan.GroupBy) > 0 {
		values := make([]string, len(plan.GroupBy))
		for index, expression := range plan.GroupBy {
			value, expressionErr := e.expression(expression)
			if expressionErr != nil {
				return "", expressionErr
			}
			values[index] = value
		}
		pageBody += " GROUP BY " + strings.Join(values, ", ")
	}
	if plan.Having != nil {
		value, expressionErr := e.expression(*plan.Having)
		if expressionErr != nil {
			return "", expressionErr
		}
		pageBody += " HAVING " + value
	}

	allCTEs := append(append([]string(nil), ctes...), e.dialect.Identifier(reportObjectSQLPageCTE)+" AS ("+pageBody+")")
	outerColumns := append([]string(nil), visible...)
	for index := range plan.OrderBy {
		outerColumns = append(outerColumns, e.dialect.Identifier(reportKeysetNullAlias(index)), e.dialect.Identifier(reportKeysetValueAlias(index)))
	}
	// CTE names are statement-local identifiers, not physical relations and
	// therefore must not pass through the schema/table-prefix renderer.
	pageRelation := e.dialect.Identifier(reportObjectSQLPageCTE)
	statement := "WITH " + strings.Join(allCTEs, ", ") + " SELECT " + strings.Join(outerColumns, ", ") + " FROM " + pageRelation
	if len(after) > 0 {
		predicate, predicateErr := e.reportKeysetPredicate(plan.OrderBy, after)
		if predicateErr != nil {
			return "", predicateErr
		}
		statement += " WHERE " + predicate
	}
	orders := make([]string, 0, len(plan.OrderBy)*2)
	for index, order := range plan.OrderBy {
		direction := strings.ToUpper(order.Direction)
		orders = append(orders, e.dialect.Identifier(reportKeysetNullAlias(index))+" ASC", e.dialect.Identifier(reportKeysetValueAlias(index))+" "+direction)
	}
	statement += " ORDER BY " + strings.Join(orders, ", ")
	remaining := plan.Limit - position
	limit := pageSize + 1
	if limit > remaining {
		limit = remaining
	}
	statement += " LIMIT " + strconv.Itoa(limit)
	return statement, nil
}

func (e *reportObjectSQLEmitter) reportKeysetFrom(plan reportmodel.ReportObjectSQLPlan) (string, error) {
	firstRelation := e.dialect.Identifier(reportObjectSQLCTE(0))
	from := firstRelation + " " + e.dialect.Identifier(plan.Sources[0].Alias)
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
		relation := e.dialect.Identifier(reportObjectSQLCTE(index + 1))
		from += keyword + relation + " " + e.dialect.Identifier(source.Alias) + " ON " + on
	}
	return from, nil
}

func (e *reportObjectSQLEmitter) reportKeysetOrderExpression(plan reportmodel.ReportObjectSQLPlan, expression reportmodel.ReportObjectSQLExpression) (string, error) {
	if expression.Kind == "result" {
		for _, projection := range plan.Projections {
			if projection.Alias == expression.Alias {
				return e.expression(projection.Expression)
			}
		}
		return "", fmt.Errorf("unknown report result order %s", expression.Alias)
	}
	return e.expression(expression)
}

func (e *reportObjectSQLEmitter) reportKeysetPredicate(orders []reportmodel.ReportObjectSQLOrder, after []any) (string, error) {
	branches := make([]string, 0, len(orders)*2)
	for index, order := range orders {
		nullColumn := e.dialect.Identifier(reportKeysetNullAlias(index))
		valueColumn := e.dialect.Identifier(reportKeysetValueAlias(index))
		nullRank := 0
		if after[index] == nil {
			nullRank = 1
		}
		nullPrefix := e.reportKeysetEqualityPrefix(after, index)
		branches = append(branches, reportKeysetBranch(nullPrefix, nullColumn+" > "+e.reportKeysetArgument(nullRank)))
		if after[index] == nil {
			continue
		}
		valuePrefix := e.reportKeysetEqualityPrefix(after, index)
		valuePrefix = append(valuePrefix, nullColumn+" = "+e.reportKeysetArgument(nullRank))
		operator := ">"
		if strings.EqualFold(order.Direction, "desc") {
			operator = "<"
		}
		branches = append(branches, reportKeysetBranch(valuePrefix, valueColumn+" "+operator+" "+e.reportKeysetArgument(after[index])))
	}
	if len(branches) == 0 {
		return "", fmt.Errorf("empty report object SQL keyset cursor")
	}
	return "(" + strings.Join(branches, " OR ") + ")", nil
}

func (e *reportObjectSQLEmitter) reportKeysetEqualityPrefix(after []any, count int) []string {
	prefix := make([]string, 0, count*2)
	for index := 0; index < count; index++ {
		nullRank := 0
		if after[index] == nil {
			nullRank = 1
		}
		prefix = append(prefix, e.dialect.Identifier(reportKeysetNullAlias(index))+" = "+e.reportKeysetArgument(nullRank))
		if after[index] == nil {
			prefix = append(prefix, e.dialect.Identifier(reportKeysetValueAlias(index))+" IS NULL")
		} else {
			prefix = append(prefix, e.dialect.Identifier(reportKeysetValueAlias(index))+" = "+e.reportKeysetArgument(after[index]))
		}
	}
	return prefix
}

func (e *reportObjectSQLEmitter) reportKeysetArgument(value any) string {
	*e.args = append(*e.args, value)
	return e.dialect.Placeholder(len(*e.args))
}

func reportKeysetBranch(prefix []string, comparison string) string {
	conditions := append(append([]string(nil), prefix...), comparison)
	return "(" + strings.Join(conditions, " AND ") + ")"
}

func reportKeysetNullAlias(index int) string  { return fmt.Sprintf("__cursor_null_%d", index) }
func reportKeysetValueAlias(index int) string { return fmt.Sprintf("__cursor_value_%d", index) }
