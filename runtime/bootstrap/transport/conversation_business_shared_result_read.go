package transport

import (
	"context"
	report "github.com/domainry/domainry-report-sdk"
	model "github.com/domainry/domainry-report-sdk/model"
	principal "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	adapter "github.com/domainry/domainry-runtime/runtime/modulehost/report"
)

func (q conversationReportQueries) AuthorizeSharedQueryResultRead(ctx context.Context, in model.ReportQueryResultAuthorization, reader, producer principal.Principal) error {
	a, err := adapter.ReportAuthorityFromRuntimePrincipal(reader)
	if err != nil {
		return err
	}
	origin, err := adapter.ReportAuthorityFromRuntimePrincipal(producer)
	if err != nil {
		return err
	}
	owner, ok := q.queries.(report.SharedResultReader)
	if !ok {
		return &report.Error{StatusCode: 503, Code: "backend.report.shared_read_scope_unavailable"}
	}
	return owner.AuthorizeSharedQueryResultRead(ctx, in, a, origin)
}

func (q conversationReportQueries) AuthorizeSharedCatalogRead(ctx context.Context, in model.ReportCatalogReadAuthorization, reader, producer principal.Principal) error {
	a, err := adapter.ReportAuthorityFromRuntimePrincipal(reader)
	if err != nil {
		return err
	}
	origin, err := adapter.ReportAuthorityFromRuntimePrincipal(producer)
	if err != nil {
		return err
	}
	owner, ok := q.queries.(report.SharedResultReader)
	if !ok {
		return &report.Error{StatusCode: 503, Code: "backend.report.shared_read_scope_unavailable"}
	}
	return owner.AuthorizeSharedCatalogRead(ctx, in, a, origin)
}

func (q conversationReportQueries) AuthorizeSharedAnalysisResultRead(ctx context.Context, in model.AnalysisResultAuthorization, reader, producer principal.Principal) error {
	a, err := adapter.ReportAuthorityFromRuntimePrincipal(reader)
	if err != nil {
		return err
	}
	origin, err := adapter.ReportAuthorityFromRuntimePrincipal(producer)
	if err != nil {
		return err
	}
	owner, ok := q.queries.(report.SharedResultReader)
	if !ok {
		return &report.Error{StatusCode: 503, Code: "backend.report.shared_read_scope_unavailable"}
	}
	return owner.AuthorizeSharedAnalysisResultRead(ctx, in, a, origin)
}

func (q conversationReportQueries) AuthorizeSharedAnalysisCatalogRead(ctx context.Context, in model.AnalysisCatalogReadAuthorization, reader, producer principal.Principal) error {
	a, err := adapter.ReportAuthorityFromRuntimePrincipal(reader)
	if err != nil {
		return err
	}
	origin, err := adapter.ReportAuthorityFromRuntimePrincipal(producer)
	if err != nil {
		return err
	}
	owner, ok := q.queries.(report.SharedResultReader)
	if !ok {
		return &report.Error{StatusCode: 503, Code: "backend.report.shared_read_scope_unavailable"}
	}
	return owner.AuthorizeSharedAnalysisCatalogRead(ctx, in, a, origin)
}
