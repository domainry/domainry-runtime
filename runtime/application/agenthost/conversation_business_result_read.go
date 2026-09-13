package agenthost

import (
	"context"
	agent "github.com/domainry/domainry-agent-sdk"
	model "github.com/domainry/domainry-report-sdk/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type ConversationBusinessResultReader interface {
	AuthorizeQueryResultRead(context.Context, model.ReportQueryResultAuthorization, principalmodel.Principal) error
	AuthorizeCatalogRead(context.Context, model.ReportCatalogReadAuthorization, principalmodel.Principal) error
	AuthorizeAnalysisResultRead(context.Context, model.AnalysisResultAuthorization, principalmodel.Principal) error
	AuthorizeAnalysisCatalogRead(context.Context, model.AnalysisCatalogReadAuthorization, principalmodel.Principal) error
}

func (h *ConversationBusinessHost) AuthorizeReportResultRead(ctx context.Context, in model.ReportQueryResultAuthorization, a agent.ConversationAuthority) error {
	p, err := h.reportPrincipal(ctx, a)
	if err != nil {
		return err
	}
	owner, ok := h.reports.(ConversationBusinessResultReader)
	if !ok {
		return conversationBusinessError("unavailable")
	}
	return conversationReportError(owner.AuthorizeQueryResultRead(ctx, in, p))
}
func (h *ConversationBusinessHost) AuthorizeReportCatalogRead(ctx context.Context, in model.ReportCatalogReadAuthorization, a agent.ConversationAuthority) error {
	p, err := h.reportPrincipal(ctx, a)
	if err != nil {
		return err
	}
	owner, ok := h.reports.(ConversationBusinessResultReader)
	if !ok {
		return conversationBusinessError("unavailable")
	}
	return conversationReportError(owner.AuthorizeCatalogRead(ctx, in, p))
}
func (h *ConversationBusinessHost) AuthorizeAnalysisResultRead(ctx context.Context, in model.AnalysisResultAuthorization, a agent.ConversationAuthority) error {
	p, err := h.reportPrincipal(ctx, a)
	if err != nil {
		return err
	}
	owner, ok := h.reports.(ConversationBusinessResultReader)
	if !ok {
		return conversationBusinessError("unavailable")
	}
	return conversationAnalysisError(owner.AuthorizeAnalysisResultRead(ctx, in, p))
}
func (h *ConversationBusinessHost) AuthorizeAnalysisCatalogRead(ctx context.Context, in model.AnalysisCatalogReadAuthorization, a agent.ConversationAuthority) error {
	p, err := h.reportPrincipal(ctx, a)
	if err != nil {
		return err
	}
	owner, ok := h.reports.(ConversationBusinessResultReader)
	if !ok {
		return conversationBusinessError("unavailable")
	}
	return conversationAnalysisError(owner.AuthorizeAnalysisCatalogRead(ctx, in, p))
}
