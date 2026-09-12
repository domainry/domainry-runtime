package agenthost

import (
	"context"
	"errors"

	agent "github.com/domainry/domainry-agent-sdk"
	report "github.com/domainry/domainry-report-sdk"
	model "github.com/domainry/domainry-report-sdk/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// ConversationBusinessAnalyses is a host port over public Report DTOs.
// Runtime resolves current authority; no calculation is implemented here.
type ConversationBusinessAnalyses interface{
	AnalysisCatalog(context.Context,model.AnalysisCatalogRequest,principalmodel.Principal)(model.AnalysisCatalog,error)
	RunAnalysis(context.Context,model.AnalysisRequest,principalmodel.Principal)(model.AnalysisResult,error)
	AuthorizeAnalysisResult(context.Context,model.AnalysisResultAuthorization,principalmodel.Principal)error
}

func(h *ConversationBusinessHost) AnalysisCatalog(ctx context.Context,in model.AnalysisCatalogRequest,a agent.ConversationAuthority)(model.AnalysisCatalog,error){
	p,err:=h.reportPrincipal(ctx,a);if err!=nil{return model.AnalysisCatalog{},err}
	owner,ok:=h.reports.(ConversationBusinessAnalyses);if !ok{return model.AnalysisCatalog{},conversationBusinessError("unavailable")}
	out,err:=owner.AnalysisCatalog(ctx,in,p);return out,conversationAnalysisError(err)
}
func(h *ConversationBusinessHost) RunAnalysis(ctx context.Context,in model.AnalysisRequest,a agent.ConversationAuthority)(model.AnalysisResult,error){
	p,err:=h.reportPrincipal(ctx,a);if err!=nil{return model.AnalysisResult{},err}
	owner,ok:=h.reports.(ConversationBusinessAnalyses);if !ok{return model.AnalysisResult{},conversationBusinessError("unavailable")}
	out,err:=owner.RunAnalysis(ctx,in,p);return out,conversationAnalysisError(err)
}
func(h *ConversationBusinessHost) AuthorizeAnalysisResult(ctx context.Context,in model.AnalysisResultAuthorization,a agent.ConversationAuthority)error{
	p,err:=h.reportPrincipal(ctx,a);if err!=nil{return err}
	owner,ok:=h.reports.(ConversationBusinessAnalyses);if !ok{return conversationBusinessError("unavailable")}
	return conversationAnalysisError(owner.AuthorizeAnalysisResult(ctx,in,p))
}
func conversationAnalysisError(err error)error{
	var failure *report.Error
	if errors.As(err,&failure){
		switch failure.Code{
		case "backend.report.analysis.result_limit_exceeded","backend.report.analysis.spec_invalid","backend.report.analysis.arithmetic_limit":
			return &agent.Error{Class:"bad_request",Code:failure.Code}
		case "backend.report.analysis.source_changed","backend.report.analysis.result_stale_or_invalid":
			return &agent.Error{Class:"conflict",Code:failure.Code}
		}
	}
	return conversationReportError(err)
}
