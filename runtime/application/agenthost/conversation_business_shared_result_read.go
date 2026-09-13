package agenthost

import (
	"context"
	agent "github.com/domainry/domainry-agent-sdk"
	model "github.com/domainry/domainry-report-sdk/model"
	principal "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type ConversationBusinessSharedResultReader interface {
	AuthorizeSharedQueryResultRead(context.Context, model.ReportQueryResultAuthorization, principal.Principal, principal.Principal) error
	AuthorizeSharedCatalogRead(context.Context, model.ReportCatalogReadAuthorization, principal.Principal, principal.Principal) error
	AuthorizeSharedAnalysisResultRead(context.Context, model.AnalysisResultAuthorization, principal.Principal, principal.Principal) error
	AuthorizeSharedAnalysisCatalogRead(context.Context, model.AnalysisCatalogReadAuthorization, principal.Principal, principal.Principal) error
}

func (h *ConversationBusinessHost) AuthorizeSharedReportResultRead(ctx context.Context, in model.ReportQueryResultAuthorization, a, producer agent.ConversationAuthority) error {
	if !producer.Known || producer.RuntimeID != a.RuntimeID || producer.WorkspaceID != a.WorkspaceID {
		return conversationBusinessError("forbidden")
	}
	reader, err := h.reportPrincipal(ctx, a)
	if err != nil {
		return err
	}
	original, err := h.reportPrincipal(ctx, producer)
	if err != nil {
		return err
	}
	owner, ok := h.reports.(ConversationBusinessSharedResultReader)
	if !ok {
		return conversationBusinessError("unavailable")
	}
	return conversationReportError(owner.AuthorizeSharedQueryResultRead(ctx, in, reader, original))
}

func (h *ConversationBusinessHost) AuthorizeSharedReportCatalogRead(ctx context.Context, in model.ReportCatalogReadAuthorization, a, producer agent.ConversationAuthority) error {
	if !producer.Known || producer.RuntimeID != a.RuntimeID || producer.WorkspaceID != a.WorkspaceID {
		return conversationBusinessError("forbidden")
	}
	reader, err := h.reportPrincipal(ctx, a)
	if err != nil {
		return err
	}
	original, err := h.reportPrincipal(ctx, producer)
	if err != nil {
		return err
	}
	owner, ok := h.reports.(ConversationBusinessSharedResultReader)
	if !ok {
		return conversationBusinessError("unavailable")
	}
	return conversationReportError(owner.AuthorizeSharedCatalogRead(ctx, in, reader, original))
}

func (h *ConversationBusinessHost) AuthorizeSharedAnalysisResultRead(ctx context.Context, in model.AnalysisResultAuthorization, a, producer agent.ConversationAuthority) error {
	if !producer.Known || producer.RuntimeID != a.RuntimeID || producer.WorkspaceID != a.WorkspaceID {
		return conversationBusinessError("forbidden")
	}
	reader, err := h.reportPrincipal(ctx, a)
	if err != nil {
		return err
	}
	original, err := h.reportPrincipal(ctx, producer)
	if err != nil {
		return err
	}
	owner, ok := h.reports.(ConversationBusinessSharedResultReader)
	if !ok {
		return conversationBusinessError("unavailable")
	}
	return conversationReportError(owner.AuthorizeSharedAnalysisResultRead(ctx, in, reader, original))
}

func (h *ConversationBusinessHost) AuthorizeSharedAnalysisCatalogRead(ctx context.Context, in model.AnalysisCatalogReadAuthorization, a, producer agent.ConversationAuthority) error {
	if !producer.Known || producer.RuntimeID != a.RuntimeID || producer.WorkspaceID != a.WorkspaceID {
		return conversationBusinessError("forbidden")
	}
	reader, err := h.reportPrincipal(ctx, a)
	if err != nil {
		return err
	}
	original, err := h.reportPrincipal(ctx, producer)
	if err != nil {
		return err
	}
	owner, ok := h.reports.(ConversationBusinessSharedResultReader)
	if !ok {
		return conversationBusinessError("unavailable")
	}
	return conversationReportError(owner.AuthorizeSharedAnalysisCatalogRead(ctx, in, reader, original))
}
