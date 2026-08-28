package service

import (
	"context"
	"fmt"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

// executeReportDatasetPage lowers the common aggregate Dataset subset into the
// same metadata-bound SQL executor used by object_sql_v1. Complex analyses,
// comparisons, tag-set semantics, contextual masking, and non-UTC bucketing
// fail closed instead of silently falling back to full-result materialization.
func (s *ReportDomainService) executeReportDatasetPage(ctx context.Context, report reportmodel.ReportSchema, plan reportmodel.ReportDatasetPlan, pageOffset, pageSize int, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	if s == nil || s.dependencies.Access == nil || s.dependencies.ObjectSQL == nil || pageOffset < 0 || pageSize < 1 || pageSize > reportmodel.ReportPageMaximumSize {
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindBadRequest, "backend.report.bounded_page_unavailable", nil)
	}
	dataset := report.Dataset
	if len(dataset.Analyses) > 0 || len(dataset.Comparisons) > 0 || dataset.RuntimeQuery != nil || dataset.RuntimeTags != nil || strings.TrimSpace(dataset.TimeZone) != "" && !strings.EqualFold(strings.TrimSpace(dataset.TimeZone), "UTC") {
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindBadRequest, "backend.report.bounded_page_unavailable", nil)
	}
	for _, measure := range dataset.Measures {
		switch strings.TrimSpace(measure.Operation) {
		case "count", "distinct_count", "sum", "avg", "min", "max":
		default:
			return reportmodel.ReportSummary{}, reportAppError(apperror.KindBadRequest, "backend.report.bounded_page_unavailable", nil)
		}
	}

	objects := make(map[string]definitionmodel.ObjectSchema, len(plan.AliasObjects))
	queries := make(map[string]recordmodel.RecordListQuery, len(plan.AliasObjects))
	aliases := reportDatasetAliasOrder(dataset)
	for _, alias := range aliases {
		object, err := s.dependencies.Access.ReportObjectForAction(ctx, principal, plan.AliasObjects[alias], "read")
		if err != nil {
			return reportmodel.ReportSummary{}, err
		}
		objects[alias] = object
	}
	if !reportDatasetPushdownAllowed(ctx, s.dependencies.Access, principal, objects) {
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindBadRequest, "backend.report.bounded_page_unavailable", nil)
	}
	for _, alias := range aliases {
		query := reportAliasReadQuery(dataset, alias)
		query.Page, query.PageSize = 1, reportDatasetPageSize
		query = s.dependencies.Access.NormalizeReportListQuery(ctx, objects[alias], query, principal)
		queries[alias] = reportIncludeAuthorizationProjection(query)
	}
	// The legacy Dataset contract permits a source-only report. Its aggregate
	// result is one empty group even when the source has no rows. Preserve that
	// result without emitting an invalid SQL SELECT with no projections.
	if len(dataset.Dimensions) == 0 && len(dataset.Measures) == 0 {
		rows := []reportmodel.ReportResultRow{}
		if pageOffset == 0 {
			rows = append(rows, reportmodel.ReportResultRow{})
		}
		return reportmodel.ReportSummary{Key: report.Key, Name: report.Name, Rows: rows, RowCount: len(rows), SourceRowCount: -1, ExecutionMode: "dataset_sql_v1", PageSize: pageSize, Total: 1, TotalSemantics: reportmodel.ReportTotalExact}, nil
	}

	sqlPlan := reportmodel.ReportObjectSQLPlan{
		Sources: make([]reportmodel.ReportObjectSQLSource, 0, len(aliases)), Parameters: map[string]reportmodel.ReportObjectSQLParameter{},
		ResultSchema: []reportmodel.ReportResultColumnSchema{}, Limit: dataset.Limit,
	}
	if sqlPlan.Limit <= 0 {
		sqlPlan.Limit = 10000
	}
	for index, alias := range aliases {
		source := reportmodel.ReportObjectSQLSource{ObjectKey: plan.AliasObjects[alias], Alias: alias, Fields: append([]string(nil), queries[alias].SelectFields...)}
		if index > 0 {
			join := dataset.Joins[index-1]
			source.JoinType, source.Cardinality = strings.TrimSpace(join.Type), strings.TrimSpace(join.Cardinality)
			conditions := make([]reportmodel.ReportObjectSQLExpression, 0, len(join.Equalities()))
			for _, equality := range join.Equalities() {
				left := reportDatasetSQLFieldExpression(objects, reportmodel.ReportDatasetField{SourceAlias: join.LeftAlias, FieldKey: equality.LeftField})
				right := reportDatasetSQLFieldExpression(objects, reportmodel.ReportDatasetField{SourceAlias: join.Alias, FieldKey: equality.RightField})
				conditions = append(conditions, reportSQLBinary("comparison", "=", left, right))
			}
			condition := reportSQLConjunction(conditions, "and")
			source.On = &condition
		}
		sqlPlan.Sources = append(sqlPlan.Sources, source)
	}

	for _, dimension := range dataset.Dimensions {
		expression := reportDatasetSQLFieldExpression(objects, dimension.Field)
		grain := strings.TrimSpace(dimension.TimeGrain)
		if grain == "" {
			grain = strings.TrimSpace(dataset.DefaultTimeGrain)
		}
		if grain != "" {
			expression = reportmodel.ReportObjectSQLExpression{Kind: "function", Name: "date_bucket", Value: grain, Type: expression.Type, Arguments: []reportmodel.ReportObjectSQLExpression{expression}}
		}
		sqlPlan.Projections = append(sqlPlan.Projections, reportmodel.ReportObjectSQLProjection{Alias: dimension.Key, Expression: expression})
		sqlPlan.GroupBy = append(sqlPlan.GroupBy, expression)
		sqlPlan.ResultSchema = append(sqlPlan.ResultSchema, reportmodel.ReportResultColumnSchema{Key: dimension.Key, Type: reportSQLResultType(expression.Type), Kind: "dimension", Precision: expression.Precision, Scale: expression.Scale})
	}
	measureExpressions := map[string]reportmodel.ReportObjectSQLExpression{}
	measureByKey := map[string]reportmodel.ReportDatasetMeasure{}
	for _, measure := range dataset.Measures {
		measureByKey[measure.Key] = measure
	}
	var buildMeasure func(string, map[string]bool) (reportmodel.ReportObjectSQLExpression, error)
	buildMeasure = func(key string, visiting map[string]bool) (reportmodel.ReportObjectSQLExpression, error) {
		if expression := measureExpressions[key]; expression.Kind != "" {
			return expression, nil
		}
		if visiting[key] {
			return reportmodel.ReportObjectSQLExpression{}, fmt.Errorf("cyclic ratio")
		}
		visiting[key] = true
		defer delete(visiting, key)
		measure, ok := measureByKey[key]
		if !ok {
			return reportmodel.ReportObjectSQLExpression{}, fmt.Errorf("unknown measure %s", key)
		}
		operation := strings.TrimSpace(measure.Operation)
		var expression reportmodel.ReportObjectSQLExpression
		switch operation {
		case "count":
			alias := strings.TrimSpace(measure.SourceAlias)
			if alias == "" {
				alias = strings.TrimSpace(dataset.Source.Alias)
			}
			expression = reportmodel.ReportObjectSQLExpression{Kind: "aggregate", Name: "count", Type: "integer", Arguments: []reportmodel.ReportObjectSQLExpression{{Kind: "field", Alias: alias, FieldKey: "id", Type: "text"}}}
		case "distinct_count", "sum", "avg", "min", "max":
			if measure.Field == nil {
				return reportmodel.ReportObjectSQLExpression{}, fmt.Errorf("measure field missing")
			}
			field := reportDatasetSQLFieldExpression(objects, *measure.Field)
			name := operation
			if operation == "distinct_count" {
				name = "count"
			}
			expression = reportmodel.ReportObjectSQLExpression{Kind: "aggregate", Name: name, Type: field.Type, Precision: field.Precision, Scale: field.Scale, Distinct: operation == "distinct_count", Arguments: []reportmodel.ReportObjectSQLExpression{field}}
			if operation == "distinct_count" {
				expression.Type, expression.Precision, expression.Scale = "integer", 0, 0
			}
		case "ratio":
			numerator, err := buildMeasure(measure.NumeratorKey, visiting)
			if err != nil {
				return reportmodel.ReportObjectSQLExpression{}, err
			}
			denominator, err := buildMeasure(measure.DenominatorKey, visiting)
			if err != nil {
				return reportmodel.ReportObjectSQLExpression{}, err
			}
			expression = reportmodel.ReportObjectSQLExpression{Kind: "binary", Operator: "/", Type: "decimal", Precision: 38, Scale: 6, Arguments: []reportmodel.ReportObjectSQLExpression{numerator, {Kind: "function", Name: "nullif", Type: denominator.Type, Arguments: []reportmodel.ReportObjectSQLExpression{denominator, {Kind: "literal", Type: "integer", ValueType: "integer", Value: "0"}}}}}
		}
		measureExpressions[key] = expression
		return expression, nil
	}
	for _, measure := range dataset.Measures {
		expression, err := buildMeasure(measure.Key, map[string]bool{})
		if err != nil {
			return reportmodel.ReportSummary{}, reportAppError(apperror.KindBadRequest, "backend.report.bounded_page_unavailable", err)
		}
		sqlPlan.Projections = append(sqlPlan.Projections, reportmodel.ReportObjectSQLProjection{Alias: measure.Key, Expression: expression})
		sqlPlan.ResultSchema = append(sqlPlan.ResultSchema, reportmodel.ReportResultColumnSchema{Key: measure.Key, Type: reportSQLResultType(expression.Type), Kind: "measure", Precision: expression.Precision, Scale: expression.Scale})
	}

	filterExpressions := make([]reportmodel.ReportObjectSQLExpression, 0, len(dataset.Filters))
	parameters := map[string]any{}
	for index, filter := range dataset.Filters {
		expression, err := reportDatasetSQLFilter(objects, filter, fmt.Sprintf("dataset_filter_%d", index), sqlPlan.Parameters, parameters)
		if err != nil {
			return reportmodel.ReportSummary{}, reportAppError(apperror.KindBadRequest, "backend.report.bounded_page_unavailable", err)
		}
		filterExpressions = append(filterExpressions, expression)
	}
	if len(filterExpressions) > 0 {
		where := reportSQLConjunction(filterExpressions, "and")
		sqlPlan.Where = &where
	}
	if dataset.Privacy != nil {
		entity := reportDatasetSQLFieldExpression(objects, dataset.Privacy.EntityField)
		count := reportmodel.ReportObjectSQLExpression{Kind: "aggregate", Name: "count", Type: "integer", Distinct: true, Arguments: []reportmodel.ReportObjectSQLExpression{entity}}
		minimum := reportmodel.ReportObjectSQLExpression{Kind: "literal", Type: "integer", ValueType: "integer", Value: fmt.Sprint(dataset.Privacy.MinimumGroupSize)}
		having := reportSQLBinary("comparison", ">=", count, minimum)
		sqlPlan.Having = &having
	}
	for _, sortRule := range dataset.Sort {
		sqlPlan.OrderBy = append(sqlPlan.OrderBy, reportmodel.ReportObjectSQLOrder{Expression: reportmodel.ReportObjectSQLExpression{Kind: "result", Alias: sortRule.Key}, Direction: sortRule.Direction})
	}
	for _, dimension := range dataset.Dimensions {
		candidate := reportmodel.ReportObjectSQLOrder{Expression: reportmodel.ReportObjectSQLExpression{Kind: "result", Alias: dimension.Key}, Direction: "asc"}
		if !reportDatasetSQLOrderContains(sqlPlan.OrderBy, dimension.Key) {
			sqlPlan.OrderBy = append(sqlPlan.OrderBy, candidate)
		}
	}

	result, err := s.dependencies.ObjectSQL.ExecuteReportObjectSQL(ctx, reportcontract.ReportObjectSQLExecutionRequest{WorkspaceID: principal.WorkspaceID, Plan: sqlPlan, Objects: objects, Queries: queries, Parameters: parameters, PageOffset: pageOffset, PageSize: pageSize})
	if err != nil {
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindInternal, "backend.report.query_failed", err)
	}
	rows := make([]reportmodel.ReportResultRow, 0, len(result.Rows))
	for _, values := range result.Rows {
		row := reportmodel.ReportResultRow{Dimensions: map[string]string{}, Measures: map[string]string{}}
		for _, column := range sqlPlan.ResultSchema {
			if column.Kind == "measure" {
				row.Measures[column.Key] = values[column.Key]
			} else {
				row.Dimensions[column.Key] = values[column.Key]
			}
		}
		if len(row.Dimensions) == 0 {
			row.Dimensions = nil
		}
		if len(row.Measures) == 0 {
			row.Measures = nil
		}
		rows = append(rows, row)
	}
	summary := reportmodel.ReportSummary{Key: report.Key, Name: report.Name, Rows: rows, RowCount: len(rows), SourceRowCount: -1, ExecutionMode: "dataset_sql_v1", PageSize: pageSize, Truncated: result.HasMore, Total: pageOffset + len(rows), TotalSemantics: reportmodel.ReportTotalExact}
	if result.TotalKnown {
		summary.Total = result.Total
	} else if result.HasMore {
		summary.Total++
		summary.TotalSemantics = reportmodel.ReportTotalAtLeast
	}
	return summary, nil
}

func reportDatasetSQLFieldExpression(objects map[string]definitionmodel.ObjectSchema, reference reportmodel.ReportDatasetField) reportmodel.ReportObjectSQLExpression {
	field := reportDatasetFieldSchema(objects[strings.TrimSpace(reference.SourceAlias)], reference.FieldKey)
	expression := reportmodel.ReportObjectSQLExpression{Kind: "field", Alias: strings.TrimSpace(reference.SourceAlias), FieldKey: strings.TrimSpace(reference.FieldKey), Type: field.Type}
	if field.Type == "currency" || field.Type == "decimal" {
		if config, err := recordmodel.RecordNormalizeDecimalConfig(field.Config); err == nil {
			expression.Precision, expression.Scale = config.Precision, int(config.Scale)
		}
	}
	return expression
}

func reportSQLResultType(value string) string {
	switch value {
	case "text", "integer", "number", "decimal", "boolean", "date", "datetime", "currency":
		return value
	default:
		return "text"
	}
}

func reportDatasetSQLFilter(objects map[string]definitionmodel.ObjectSchema, filter reportmodel.ReportDatasetFilter, prefix string, declared map[string]reportmodel.ReportObjectSQLParameter, parameters map[string]any) (reportmodel.ReportObjectSQLExpression, error) {
	field := reportDatasetSQLFieldExpression(objects, filter.Field)
	parameter := func(suffix string, value any) reportmodel.ReportObjectSQLExpression {
		key := prefix + suffix
		parameterType := reportSQLResultType(field.Type)
		if parameterType == "currency" {
			parameterType = "decimal"
		}
		declared[key] = reportmodel.ReportObjectSQLParameter{Key: key, Type: parameterType, Required: true}
		parameters[key] = value
		return reportmodel.ReportObjectSQLExpression{Kind: "parameter", Name: key, Type: field.Type, Precision: field.Precision, Scale: field.Scale}
	}
	operator := strings.TrimSpace(filter.Operator)
	switch operator {
	case "eq", "ne", "gt", "gte", "lt", "lte":
		sqlOperator := map[string]string{"eq": "=", "ne": "!=", "gt": ">", "gte": ">=", "lt": "<", "lte": "<="}[operator]
		return reportSQLBinary("comparison", sqlOperator, field, parameter("_value", filter.Value)), nil
	case "between":
		if len(filter.Values) != 2 {
			return reportmodel.ReportObjectSQLExpression{}, fmt.Errorf("invalid between filter")
		}
		return reportmodel.ReportObjectSQLExpression{Kind: "between", Operator: "between", Arguments: []reportmodel.ReportObjectSQLExpression{field, parameter("_from", filter.Values[0]), parameter("_to", filter.Values[1])}}, nil
	case "in", "not_in":
		if len(filter.Values) == 0 {
			return reportmodel.ReportObjectSQLExpression{}, fmt.Errorf("empty set filter")
		}
		items := make([]reportmodel.ReportObjectSQLExpression, 0, len(filter.Values))
		for index, value := range filter.Values {
			comparison := "="
			if operator == "not_in" {
				comparison = "!="
			}
			items = append(items, reportSQLBinary("comparison", comparison, field, parameter(fmt.Sprintf("_%d", index), value)))
		}
		join := "or"
		if operator == "not_in" {
			join = "and"
		}
		return reportSQLConjunction(items, join), nil
	case "is_null", "not_null":
		sqlOperator := "IS NULL"
		if operator == "not_null" {
			sqlOperator = "IS NOT NULL"
		}
		return reportmodel.ReportObjectSQLExpression{Kind: "is", Operator: sqlOperator, Arguments: []reportmodel.ReportObjectSQLExpression{field}}, nil
	default:
		return reportmodel.ReportObjectSQLExpression{}, fmt.Errorf("unsupported filter %s", operator)
	}
}

func reportSQLBinary(kind, operator string, left, right reportmodel.ReportObjectSQLExpression) reportmodel.ReportObjectSQLExpression {
	return reportmodel.ReportObjectSQLExpression{Kind: kind, Operator: operator, Type: left.Type, Precision: left.Precision, Scale: left.Scale, Arguments: []reportmodel.ReportObjectSQLExpression{left, right}}
}

func reportSQLConjunction(values []reportmodel.ReportObjectSQLExpression, operator string) reportmodel.ReportObjectSQLExpression {
	if len(values) == 1 {
		return values[0]
	}
	result := reportSQLBinary("logical", operator, values[0], values[1])
	for _, value := range values[2:] {
		result = reportSQLBinary("logical", operator, result, value)
	}
	return result
}

func reportDatasetSQLOrderContains(orders []reportmodel.ReportObjectSQLOrder, alias string) bool {
	for _, order := range orders {
		if order.Expression.Kind == "result" && order.Expression.Alias == alias {
			return true
		}
	}
	return false
}
