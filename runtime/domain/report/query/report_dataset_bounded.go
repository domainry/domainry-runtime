package query

import (
	"context"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportobjectsql "github.com/domainry/domainry-report/query/objectsql"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
)

// executeReportDatasetPage lowers the common aggregate Dataset subset into the
// same metadata-bound SQL executor used by object_sql_v1. Complex analyses,
// comparisons, tag-set semantics, contextual masking, and non-UTC bucketing
// fail closed instead of silently falling back to full-result materialization.
func (s *ReportDomainService) executeReportDatasetPage(ctx context.Context, report reportmodel.ReportSchema, plan reportmodel.ReportDatasetPlan, pageCursor string, pagePosition, pageSize int, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	if s == nil || s.dependencies.Access == nil || s.dependencies.ObjectSQL == nil || pagePosition < 0 || pageSize < 1 || pageSize > reportmodel.ReportPageMaximumSize {
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
		if pagePosition == 0 {
			rows = append(rows, reportmodel.ReportResultRow{})
		}
		return reportmodel.ReportSummary{Key: report.Key, Name: report.Name, Rows: rows, RowCount: len(rows), SourceRowCount: -1, ExecutionMode: "dataset_sql_v1", PageSize: pageSize, Total: 1, TotalSemantics: reportmodel.ReportTotalExact}, nil
	}

	selectFields := make(map[string][]string, len(aliases))
	for _, alias := range aliases {
		selectFields[alias] = append([]string(nil), queries[alias].SelectFields...)
	}
	builtPlan, err := reportobjectsql.BuildDatasetPlan(dataset, plan.AliasObjects, aliases, selectFields, reportSQLEngineObjects(objects))
	if err != nil {
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindBadRequest, "backend.report.bounded_page_unavailable", err)
	}
	sqlPlan, parameters := builtPlan.Plan, builtPlan.Parameters

	result, err := s.dependencies.ObjectSQL.ExecuteReportObjectSQL(ctx, reportcontract.ReportObjectSQLExecutionRequest{WorkspaceID: principal.WorkspaceID, Plan: sqlPlan, Objects: objects, Queries: queries, Parameters: parameters, PageCursor: pageCursor, PagePosition: pagePosition, PageSize: pageSize})
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
	summary := reportmodel.ReportSummary{Key: report.Key, Name: report.Name, Rows: rows, RowCount: len(rows), SourceRowCount: -1, ExecutionMode: "dataset_sql_v1", PageSize: pageSize, Truncated: result.HasMore, Total: pagePosition + len(rows), TotalSemantics: reportmodel.ReportTotalExact, ExecutionCursor: result.NextCursor}
	if result.TotalKnown {
		summary.Total = result.Total
	} else if result.HasMore {
		summary.Total++
		summary.TotalSemantics = reportmodel.ReportTotalAtLeast
	}
	return summary, nil
}

// A measure-only aggregate has exactly one result row and therefore no domain
// identity column that can act as a cursor tie-breaker. Ordering by its first
// projected measure gives the keyset executor a deterministic first-page term
// without inventing OFFSET pagination. Grouped datasets are already ordered by
// every dimension above.
func ensureReportDatasetSQLStableOrder(plan *reportmodel.ReportObjectSQLPlan) {
	reportobjectsql.EnsureDatasetStableOrder(plan)
}

func reportDatasetSQLFieldExpression(objects map[string]definitionmodel.ObjectSchema, reference reportmodel.ReportDatasetField) reportmodel.ReportObjectSQLExpression {
	return reportobjectsql.DatasetFieldExpression(reportSQLEngineObjects(objects), reference)
}

func reportSQLResultType(value string) string {
	return reportobjectsql.ResultType(value)
}

func reportDatasetSQLFilter(objects map[string]definitionmodel.ObjectSchema, filter reportmodel.ReportDatasetFilter, prefix string, declared map[string]reportmodel.ReportObjectSQLParameter, parameters map[string]any) (reportmodel.ReportObjectSQLExpression, error) {
	return reportobjectsql.DatasetFilter(reportSQLEngineObjects(objects), filter, prefix, declared, parameters)
}

func reportSQLBinary(kind, operator string, left, right reportmodel.ReportObjectSQLExpression) reportmodel.ReportObjectSQLExpression {
	return reportobjectsql.Binary(kind, operator, left, right)
}

func reportSQLConjunction(values []reportmodel.ReportObjectSQLExpression, operator string) reportmodel.ReportObjectSQLExpression {
	return reportobjectsql.Conjunction(values, operator)
}

func reportDatasetSQLOrderContains(orders []reportmodel.ReportObjectSQLOrder, alias string) bool {
	return reportobjectsql.OrderContains(orders, alias)
}
