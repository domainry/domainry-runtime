package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func (s *ReportDomainService) executeReportObjectSQL(ctx context.Context, report reportmodel.ReportSchema, rawParameters map[string]any, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	return s.executeReportObjectSQLPage(ctx, report, rawParameters, 0, 0, principal)
}

func (s *ReportDomainService) executeReportObjectSQLPage(ctx context.Context, report reportmodel.ReportSchema, rawParameters map[string]any, pageOffset, pageSize int, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	if s == nil || s.dependencies.Access == nil || s.dependencies.ObjectSQL == nil || report.ObjectSQLV1 == nil {
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindInternal, "backend.report.object_sql_execution_unavailable", nil)
	}
	plan, objects, queries, err := s.reportObjectSQLExecutionInputs(ctx, report, principal)
	if err != nil {
		return reportmodel.ReportSummary{}, err
	}
	parameters, err := normalizeReportObjectSQLParameters(plan.Parameters, rawParameters)
	if err != nil {
		return reportmodel.ReportSummary{}, err
	}
	timeout := time.Duration(report.ObjectSQLV1.TimeoutMilliseconds) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	result, err := s.dependencies.ObjectSQL.ExecuteReportObjectSQL(ctx, reportcontract.ReportObjectSQLExecutionRequest{WorkspaceID: principal.WorkspaceID, Plan: plan, Objects: objects, Queries: queries, Parameters: parameters, Timeout: timeout, PageOffset: pageOffset, PageSize: pageSize})
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
			summary.Total = pageOffset + len(rows)
			summary.TotalSemantics = reportmodel.ReportTotalExact
		}
		if !result.TotalKnown && result.HasMore {
			summary.Total++
			summary.TotalSemantics = reportmodel.ReportTotalAtLeast
		}
	}
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

// NormalizeReportObjectSQLParameters applies the same declared type/default
// contract used by interactive query execution. Governed export stores this
// canonical result in its immutable scope before hashing and auditing it.
func NormalizeReportObjectSQLParameters(declarations []reportmodel.ReportObjectSQLParameter, raw map[string]any) (map[string]any, error) {
	declared := make(map[string]reportmodel.ReportObjectSQLParameter, len(declarations))
	for _, parameter := range declarations {
		parameter.Key = strings.TrimSpace(parameter.Key)
		parameter.Type = strings.TrimSpace(parameter.Type)
		declared[parameter.Key] = parameter
	}
	return normalizeReportObjectSQLParameters(declared, raw)
}

func normalizeReportObjectSQLParameters(declared map[string]reportmodel.ReportObjectSQLParameter, raw map[string]any) (map[string]any, error) {
	if raw == nil {
		raw = map[string]any{}
	}
	for key := range raw {
		if declared[key].Key == "" {
			return nil, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.object_sql_parameter_unknown", Params: map[string]string{"parameter": key}}
		}
	}
	result := make(map[string]any, len(declared))
	for key, parameter := range declared {
		value, exists := raw[key]
		if !exists && parameter.Default != nil {
			value, exists = parameter.Default, true
		}
		if !exists {
			if parameter.Required {
				return nil, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.object_sql_parameter_required", Params: map[string]string{"parameter": key}}
			}
			result[key] = nil
			continue
		}
		normalized, err := reportObjectSQLParameterValue(parameter.Type, value)
		if err != nil {
			return nil, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.object_sql_parameter_type_invalid", Params: map[string]string{"parameter": key, "type": parameter.Type}, Err: err}
		}
		result[key] = normalized
	}
	return result, nil
}

func reportObjectSQLParameters(declared map[string]reportmodel.ReportObjectSQLParameter, raw map[string]any) (map[string]any, error) {
	return normalizeReportObjectSQLParameters(declared, raw)
}

func reportObjectSQLParameterValue(parameterType string, value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	switch parameterType {
	case "text":
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("text parameter must be a string")
		}
		return text, nil
	case "integer":
		switch typed := value.(type) {
		case json.Number:
			return typed.Int64()
		case string:
			return strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		case int:
			return int64(typed), nil
		case int64:
			return typed, nil
		}
		return nil, fmt.Errorf("integer parameter must be an integer")
	case "decimal":
		text := strings.TrimSpace(fmt.Sprint(value))
		parsed, err := decimal.NewFromString(text)
		if err != nil {
			return nil, err
		}
		return parsed.String(), nil
	case "boolean":
		boolean, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("boolean parameter must be a boolean")
		}
		return boolean, nil
	case "date":
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("date parameter must be a string")
		}
		if _, err := time.Parse("2006-01-02", text); err != nil {
			return nil, err
		}
		return text, nil
	case "datetime":
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("datetime parameter must be a string")
		}
		parsed, err := time.Parse(time.RFC3339, text)
		if err != nil {
			return nil, err
		}
		return parsed.UTC().Format(time.RFC3339Nano), nil
	default:
		return nil, fmt.Errorf("unsupported parameter type %q", parameterType)
	}
}

func reportObjectSQLAppError(err error) error {
	planErr, ok := err.(*reportmodel.ReportObjectSQLPlanError)
	if !ok {
		return reportAppError(apperror.KindBadRequest, "backend.report.object_sql_invalid", err)
	}
	return &apperror.AppError{Kind: apperror.KindBadRequest, Code: planErr.Code, Params: planErr.Params, Err: planErr}
}
