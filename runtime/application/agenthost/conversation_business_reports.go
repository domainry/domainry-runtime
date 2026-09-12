package agenthost

import (
	"context"
	"errors"

	agent "github.com/domainry/domainry-agent-sdk"
	report "github.com/domainry/domainry-report-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// ConversationBusinessReports is the Report owner's existing query boundary.
// Runtime does not retain another definition store or Report execution engine.
type ConversationBusinessReports interface {
	Summary(context.Context, reportmodel.ReportSummaryRequest, principalmodel.Principal) (reportmodel.ReportSummary, error)
	QueryObjectSQL(context.Context, reportmodel.ReportObjectSQLRequest, principalmodel.Principal) (reportmodel.ReportSummary, error)
}

type ConversationGovernedBusinessReports interface {
	ConversationBusinessReports
	Catalog(context.Context, reportmodel.ReportCatalogRequest, principalmodel.Principal) (reportmodel.ReportCatalog, error)
	Query(context.Context, reportmodel.ReportObjectSQLRequest, principalmodel.Principal) (reportmodel.ReportQueryResult, error)
	AuthorizeQueryResult(context.Context, reportmodel.ReportQueryResultAuthorization, principalmodel.Principal) error
}

func (h *ConversationBusinessHost) ReportCatalog(ctx context.Context, in reportmodel.ReportCatalogRequest, a agent.ConversationAuthority) (reportmodel.ReportCatalog, error) {
	p, err := h.reportPrincipal(ctx, a)
	if err != nil {
		return reportmodel.ReportCatalog{}, err
	}
	queries, ok := h.reports.(ConversationGovernedBusinessReports)
	if !ok {
		return reportmodel.ReportCatalog{}, conversationBusinessError("unavailable")
	}
	out, err := queries.Catalog(ctx, in, p)
	return out, conversationReportError(err)
}

func (h *ConversationBusinessHost) QueryReport(ctx context.Context, in reportmodel.ReportObjectSQLRequest, a agent.ConversationAuthority) (reportmodel.ReportQueryResult, error) {
	p, err := h.reportPrincipal(ctx, a)
	if err != nil {
		return reportmodel.ReportQueryResult{}, err
	}
	queries, ok := h.reports.(ConversationGovernedBusinessReports)
	if !ok {
		return reportmodel.ReportQueryResult{}, conversationBusinessError("unavailable")
	}
	out, err := queries.Query(ctx, in, p)
	return out, conversationReportError(err)
}

func (h *ConversationBusinessHost) AuthorizeReportResult(ctx context.Context, in reportmodel.ReportQueryResultAuthorization, a agent.ConversationAuthority) error {
	p, err := h.reportPrincipal(ctx, a)
	if err != nil {
		return err
	}
	queries, ok := h.reports.(ConversationGovernedBusinessReports)
	if !ok {
		return conversationBusinessError("unavailable")
	}
	return conversationReportError(queries.AuthorizeQueryResult(ctx, in, p))
}

func WithConversationBusinessReports(queries ConversationBusinessReports) ConversationBusinessHostOption {
	return func(h *ConversationBusinessHost) error {
		if queries == nil {
			return conversationBusinessError("unavailable")
		}
		h.reports = queries
		return nil
	}
}

func (h *ConversationBusinessHost) reportPrincipal(ctx context.Context, a agent.ConversationAuthority) (principalmodel.Principal, error) {
	if h.reports == nil {
		return principalmodel.Principal{}, conversationBusinessError("unavailable")
	}
	return h.principal(ctx, a)
}

func (h *ConversationBusinessHost) ReportSummary(ctx context.Context, in reportmodel.ReportSummaryRequest, a agent.ConversationAuthority) (reportmodel.ReportSummary, error) {
	authority, err := h.reportPrincipal(ctx, a)
	if err != nil {
		return reportmodel.ReportSummary{}, err
	}
	out, err := h.reports.Summary(ctx, in, authority)
	return out, conversationReportError(err)
}

func (h *ConversationBusinessHost) ReportObjectSQL(ctx context.Context, in reportmodel.ReportObjectSQLRequest, a agent.ConversationAuthority) (reportmodel.ReportSummary, error) {
	authority, err := h.reportPrincipal(ctx, a)
	if err != nil {
		return reportmodel.ReportSummary{}, err
	}
	out, err := h.reports.QueryObjectSQL(ctx, in, authority)
	return out, conversationReportError(err)
}

func conversationReportError(err error) error {
	if err == nil {
		return nil
	}
	var e *report.Error
	if errors.As(err, &e) {
		switch e.StatusCode {
		case 400, 422:
			return conversationBusinessError("bad_request")
		case 401, 403:
			return conversationBusinessError("forbidden")
		case 404:
			return conversationBusinessError("not_found")
		case 409:
			return conversationBusinessError("conflict")
		}
	}
	return conversationBusinessReadError(err)
}
