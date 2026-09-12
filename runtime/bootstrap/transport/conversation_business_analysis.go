package transport

import (
	"context"

	report "github.com/domainry/domainry-report-sdk"
	model "github.com/domainry/domainry-report-sdk/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportadapter "github.com/domainry/domainry-runtime/runtime/modulehost/report"
)

func(q conversationReportQueries) analyses(p principalmodel.Principal)(report.Analyses,model.ReportAuthority,error){
	a,err:=reportadapter.ReportAuthorityFromRuntimePrincipal(p);if err!=nil{return nil,a,err}
	owner,ok:=q.queries.(report.Analyses);if !ok{return nil,a,&report.Error{StatusCode:503,Code:"backend.report.analysis.unavailable"}}
	return owner,a,nil
}
func(q conversationReportQueries) AnalysisCatalog(ctx context.Context,in model.AnalysisCatalogRequest,p principalmodel.Principal)(model.AnalysisCatalog,error){
	owner,a,err:=q.analyses(p);if err!=nil{return model.AnalysisCatalog{},err};return owner.AnalysisCatalog(ctx,in,a)
}
func(q conversationReportQueries) RunAnalysis(ctx context.Context,in model.AnalysisRequest,p principalmodel.Principal)(model.AnalysisResult,error){
	owner,a,err:=q.analyses(p);if err!=nil{return model.AnalysisResult{},err};return owner.RunAnalysis(ctx,in,a)
}
func(q conversationReportQueries) AuthorizeAnalysisResult(ctx context.Context,in model.AnalysisResultAuthorization,p principalmodel.Principal)error{
	owner,a,err:=q.analyses(p);if err!=nil{return err};return owner.AuthorizeAnalysisResult(ctx,in,a)
}
