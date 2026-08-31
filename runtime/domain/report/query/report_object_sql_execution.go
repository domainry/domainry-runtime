package query

import (
	"context"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportobjectsql "github.com/domainry/domainry-report/query/objectsql"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
)

func (s *ReportDomainService) executeReportObjectSQL(ctx context.Context, report reportmodel.ReportSchema, rawParameters map[string]any, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	return s.executeReportObjectSQLPage(ctx, report, rawParameters, "", 0, 0, principal)
}

func (s *ReportDomainService) executeReportObjectSQLPage(ctx context.Context, report reportmodel.ReportSchema, rawParameters map[string]any, pageCursor string, pagePosition, pageSize int, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	if s == nil || s.dependencies.Access == nil || s.dependencies.ObjectSQL == nil || report.ObjectSQLV1 == nil {
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindInternal, "backend.report.object_sql_execution_unavailable", nil)
	}
	plan, objects, queries, err := s.reportObjectSQLExecutionInputs(ctx, report, principal)
	if err != nil {
		return reportmodel.ReportSummary{}, err
	}
	if reportmodel.ReportCrossWorkspaceAggregate(report) && !reportobjectsql.SafeCrossWorkspaceAggregatePlan(plan) {
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindForbidden, "backend.report.cross_workspace_raw_projection_forbidden", nil)
	}
	parameters, err := reportobjectsql.NormalizeDeclaredParameters(plan.Parameters, rawParameters)
	if err != nil {
		return reportmodel.ReportSummary{}, err
	}
	timeout := time.Duration(report.ObjectSQLV1.TimeoutMilliseconds) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	result, err := s.dependencies.ObjectSQL.ExecuteReportObjectSQL(ctx, reportcontract.ReportObjectSQLExecutionRequest{WorkspaceID: principal.WorkspaceID, CrossWorkspaceAggregate: reportmodel.ReportCrossWorkspaceAggregate(report), Plan: plan, Objects: objects, Queries: queries, Parameters: parameters, Timeout: timeout, PageCursor: pageCursor, PagePosition: pagePosition, PageSize: pageSize})
	if err != nil {
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindInternal, "backend.report.object_sql_query_failed", err)
	}
	rows := make([]reportmodel.ReportResultRow, 0, len(result.Rows))
	for _, values := range result.Rows {
		row := reportmodel.ReportResultRow{Dimensions: map[string]string{}, Measures: map[string]string{}}
		for _, column := range plan.ResultSchema {
			value, exists := values[column.Key]
			if !exists {
				continue
			}
			if column.Kind == "measure" {
				row.Measures[column.Key] = value
			} else {
				row.Dimensions[column.Key] = value
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
	// The aggregate query does not expose a trustworthy pre-aggregation source
	// row count. -1 is explicit unknown; returning result row count would be a
	// false business fact.
	summary := reportmodel.ReportSummary{Key: report.Key, Name: report.Name, Rows: rows, RowCount: len(rows), SourceRowCount: -1, ExecutionMode: "object_sql_v1", ResultSchema: append([]reportmodel.ReportResultColumnSchema(nil), plan.ResultSchema...)}
	if pageSize > 0 {
		summary.PageSize = pageSize
		summary.Truncated = result.HasMore
		if result.TotalKnown {
			summary.Total = result.Total
			summary.TotalSemantics = reportmodel.ReportTotalExact
		} else {
			summary.Total = pagePosition + len(rows)
			summary.TotalSemantics = reportmodel.ReportTotalExact
		}
		if !result.TotalKnown && result.HasMore {
			summary.Total++
			summary.TotalSemantics = reportmodel.ReportTotalAtLeast
		}
	}
	summary.ExecutionCursor = result.NextCursor
	return summary, nil
}

func (s *ReportDomainService) reportObjectSQLExecutionInputs(ctx context.Context, report reportmodel.ReportSchema, principal principalmodel.Principal) (reportmodel.ReportObjectSQLPlan, map[string]definitionmodel.ObjectSchema, map[string]recordmodel.RecordListQuery, error) {
	if s == nil || s.dependencies.Access == nil || report.ObjectSQLV1 == nil {
		return reportmodel.ReportObjectSQLPlan{}, nil, nil, reportAppError(apperror.KindInternal, "backend.report.object_sql_execution_unavailable", nil)
	}
	objectsByKey := map[string]definitionmodel.ObjectSchema{}
	for _, rawObjectKey := range report.ObjectSQLV1.SourceObjects {
		objectKey := strings.TrimSpace(rawObjectKey)
		object, err := s.dependencies.Access.ReportObjectForAction(ctx, principal, objectKey, "read")
		if err != nil {
			return reportmodel.ReportObjectSQLPlan{}, nil, nil, err
		}
		objectsByKey[objectKey] = object
	}
	plan, err := reportcontract.CompileReportObjectSQL(*report.ObjectSQLV1, objectsByKey)
	if err != nil {
		return reportmodel.ReportObjectSQLPlan{}, nil, nil, reportObjectSQLAppError(err)
	}
	authorizer, ok := s.dependencies.Access.(reportcontract.ReportObjectSQLFieldAuthorizer)
	if !ok {
		return reportmodel.ReportObjectSQLPlan{}, nil, nil, reportAppError(apperror.KindInternal, "backend.report.object_sql_field_authorization_unavailable", nil)
	}
	objects := make(map[string]definitionmodel.ObjectSchema, len(plan.Sources))
	queries := make(map[string]recordmodel.RecordListQuery, len(plan.Sources))
	for _, source := range plan.Sources {
		object := objectsByKey[source.ObjectKey]
		for _, fieldKey := range source.Fields {
			if err := authorizer.AuthorizeReportObjectSQLField(ctx, principal, object, fieldKey); err != nil {
				return reportmodel.ReportObjectSQLPlan{}, nil, nil, err
			}
		}
		query := recordmodel.RecordListQuery{Page: 1, PageSize: reportDatasetPageSize, SelectFields: append([]string(nil), source.Fields...)}
		query = s.dependencies.Access.NormalizeReportListQuery(ctx, object, query, principal)
		objects[source.Alias], queries[source.Alias] = object, reportIncludeAuthorizationProjection(query)
	}
	return plan, objects, queries, nil
}

func reportObjectSQLAppError(err error) error {
	planErr, ok := err.(*reportmodel.ReportObjectSQLPlanError)
	if !ok {
		return reportAppError(apperror.KindBadRequest, "backend.report.object_sql_invalid", err)
	}
	return &apperror.AppError{Kind: apperror.KindBadRequest, Code: planErr.Code, Params: planErr.Params, Err: planErr}
}
