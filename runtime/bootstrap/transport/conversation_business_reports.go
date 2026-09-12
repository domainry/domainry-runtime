package transport

import (
	"context"

	report "github.com/domainry/domainry-report-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportadapter "github.com/domainry/domainry-runtime/runtime/modulehost/report"
)

// Bootstrap alone projects the freshly resolved Runtime principal into the
// public Report SDK. No browser credential or manufactured process grant is used.
type conversationReportQueries struct{ queries report.Queries }

func (q conversationReportQueries) governed(p principalmodel.Principal) (report.GovernedQueries, reportmodel.ReportAuthority, error) {
	a, err := reportadapter.ReportAuthorityFromRuntimePrincipal(p)
	if err != nil {
		return nil, a, err
	}
	queries, ok := q.queries.(report.GovernedQueries)
	if !ok {
		return nil, a, &report.Error{StatusCode: 503, Code: "backend.report.evidence_unavailable"}
	}
	return queries, a, nil
}

func (q conversationReportQueries) Catalog(ctx context.Context, in reportmodel.ReportCatalogRequest, p principalmodel.Principal) (reportmodel.ReportCatalog, error) {
	queries, a, err := q.governed(p)
	if err != nil {
		return reportmodel.ReportCatalog{}, err
	}
	return queries.Catalog(ctx, in, a)
}

func (q conversationReportQueries) Query(ctx context.Context, in reportmodel.ReportObjectSQLRequest, p principalmodel.Principal) (reportmodel.ReportQueryResult, error) {
	queries, a, err := q.governed(p)
	if err != nil {
		return reportmodel.ReportQueryResult{}, err
	}
	return queries.Query(ctx, in, a)
}

func (q conversationReportQueries) AuthorizeQueryResult(ctx context.Context, in reportmodel.ReportQueryResultAuthorization, p principalmodel.Principal) error {
	queries, a, err := q.governed(p)
	if err != nil {
		return err
	}
	return queries.AuthorizeQueryResult(ctx, in, a)
}

func (q conversationReportQueries) Summary(ctx context.Context, in reportmodel.ReportSummaryRequest, p principalmodel.Principal) (reportmodel.ReportSummary, error) {
	a, err := reportadapter.ReportAuthorityFromRuntimePrincipal(p)
	if err != nil {
		return reportmodel.ReportSummary{}, err
	}
	return q.queries.Summary(ctx, in, a)
}

func (q conversationReportQueries) QueryObjectSQL(ctx context.Context, in reportmodel.ReportObjectSQLRequest, p principalmodel.Principal) (reportmodel.ReportSummary, error) {
	a, err := reportadapter.ReportAuthorityFromRuntimePrincipal(p)
	if err != nil {
		return reportmodel.ReportSummary{}, err
	}
	return q.queries.QueryObjectSQL(ctx, in, a)
}
