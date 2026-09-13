package transport

import (
	"context"
	report "github.com/domainry/domainry-report-sdk"
	model "github.com/domainry/domainry-report-sdk/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportadapter "github.com/domainry/domainry-runtime/runtime/modulehost/report"
)

func (q conversationReportQueries) AuthorizeQueryResultRead(ctx context.Context, in model.ReportQueryResultAuthorization, p principalmodel.Principal) error {
	a, err := reportadapter.ReportAuthorityFromRuntimePrincipal(p)
	if err != nil {
		return err
	}
	owner, ok := q.queries.(report.ResultReader)
	if !ok {
		return &report.Error{StatusCode: 503, Code: "backend.report.read_scope_unavailable"}
	}
	return owner.AuthorizeQueryResultRead(ctx, in, a)
}
func (q conversationReportQueries) AuthorizeCatalogRead(ctx context.Context, in model.ReportCatalogReadAuthorization, p principalmodel.Principal) error {
	a, err := reportadapter.ReportAuthorityFromRuntimePrincipal(p)
	if err != nil {
		return err
	}
	owner, ok := q.queries.(report.ResultReader)
	if !ok {
		return &report.Error{StatusCode: 503, Code: "backend.report.read_scope_unavailable"}
	}
	return owner.AuthorizeCatalogRead(ctx, in, a)
}
func (q conversationReportQueries) AuthorizeAnalysisResultRead(ctx context.Context, in model.AnalysisResultAuthorization, p principalmodel.Principal) error {
	a, err := reportadapter.ReportAuthorityFromRuntimePrincipal(p)
	if err != nil {
		return err
	}
	owner, ok := q.queries.(report.ResultReader)
	if !ok {
		return &report.Error{StatusCode: 503, Code: "backend.report.read_scope_unavailable"}
	}
	return owner.AuthorizeAnalysisResultRead(ctx, in, a)
}
func (q conversationReportQueries) AuthorizeAnalysisCatalogRead(ctx context.Context, in model.AnalysisCatalogReadAuthorization, p principalmodel.Principal) error {
	a, err := reportadapter.ReportAuthorityFromRuntimePrincipal(p)
	if err != nil {
		return err
	}
	owner, ok := q.queries.(report.ResultReader)
	if !ok {
		return &report.Error{StatusCode: 503, Code: "backend.report.read_scope_unavailable"}
	}
	return owner.AuthorizeAnalysisCatalogRead(ctx, in, a)
}
