package query

import (
	"context"
	"fmt"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	reportobjectsql "github.com/domainry/domainry-runtime/runtime/domain/report/query/objectsql"
)

type ReportCrossWorkspaceExecutionAudit interface {
	AppendCrossWorkspaceExecution(context.Context, reportmodel.ReportSchema, reportmodel.ReportSummary, principalmodel.Principal) error
}

func (s *ReportQueryApplicationService) Summary(ctx context.Context, reportKey string, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	return s.SummaryMode(ctx, reportKey, "realtime", principal)
}

func (s *ReportQueryApplicationService) SummaryMode(ctx context.Context, reportKey, mode string, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	if _, err := principalmodel.QueryScopeForPrincipal(principal); err != nil {
		return reportmodel.ReportSummary{}, reportWorkspaceError(err)
	}
	report, err := s.domain.ReportForSummary(ctx, reportKey, principal)
	if err != nil {
		return reportmodel.ReportSummary{}, err
	}
	summary, err := s.domain.SummaryMode(ctx, reportKey, mode, principal)
	if err != nil {
		return summary, err
	}
	if err := s.auditCrossWorkspaceReport(ctx, report, summary, principal); err != nil {
		return reportmodel.ReportSummary{}, err
	}
	return summary, nil
}

func (s *ReportQueryApplicationService) QueryObjectSQL(ctx context.Context, reportKey string, parameters map[string]any, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	if _, err := principalmodel.QueryScopeForPrincipal(principal); err != nil {
		return reportmodel.ReportSummary{}, reportWorkspaceError(err)
	}
	report, err := s.domain.ReportForSummary(ctx, reportKey, principal)
	if err != nil {
		return reportmodel.ReportSummary{}, err
	}
	summary, err := s.domain.QueryObjectSQL(ctx, reportKey, parameters, principal)
	if err != nil {
		return summary, err
	}
	if err := s.auditCrossWorkspaceReport(ctx, report, summary, principal); err != nil {
		return reportmodel.ReportSummary{}, err
	}
	return summary, nil
}

func (s *ReportQueryApplicationService) QueryObjectSQLPage(ctx context.Context, reportKey string, parameters map[string]any, page reportmodel.ReportPageRequest, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	if _, err := principalmodel.QueryScopeForPrincipal(principal); err != nil {
		return reportmodel.ReportSummary{}, reportWorkspaceError(err)
	}
	report, err := s.domain.ReportForSummary(ctx, reportKey, principal)
	if err != nil {
		return reportmodel.ReportSummary{}, err
	}
	normalized, err := reportobjectsql.NormalizeParameters(report.ObjectSQLV1.Parameters, parameters)
	if err != nil {
		return reportmodel.ReportSummary{}, err
	}
	pageSize := page.PageSize
	if pageSize == 0 {
		pageSize = reportmodel.ReportPageDefaultSize
	}
	if pageSize < 1 || pageSize > reportmodel.ReportPageMaximumSize {
		return reportmodel.ReportSummary{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.page_size_invalid", Params: map[string]string{"maximum": fmt.Sprint(reportmodel.ReportPageMaximumSize)}}
	}
	fingerprint, err := reportPageFingerprint(report, map[string]any{"parameters": normalized}, principal)
	if err != nil {
		return reportmodel.ReportSummary{}, reportApplicationError(err)
	}
	summary, err := s.executeStableReportKeysetPage(ctx, report, fingerprint, page, pageSize, principal, func(cursor string, position, size int) (reportmodel.ReportSummary, error) {
		return s.domain.QueryObjectSQLPage(ctx, reportKey, normalized, cursor, position, size, principal)
	})
	if err != nil {
		return summary, err
	}
	if err := s.auditCrossWorkspaceReport(ctx, report, summary, principal); err != nil {
		return reportmodel.ReportSummary{}, err
	}
	return summary, nil
}

func (s *ReportQueryApplicationService) auditCrossWorkspaceReport(ctx context.Context, report reportmodel.ReportSchema, summary reportmodel.ReportSummary, principal principalmodel.Principal) error {
	if !reportmodel.ReportCrossWorkspaceAggregate(report) {
		return nil
	}
	if s.audit == nil {
		return reportApplicationError(nil)
	}
	if err := s.audit.AppendCrossWorkspaceExecution(ctx, report, summary, principal); err != nil {
		return reportApplicationError(err)
	}
	return nil
}
