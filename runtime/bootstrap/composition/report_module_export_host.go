package composition

import (
	"context"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportmodulehost "github.com/domainry/domainry-report-sdk/modulehost"
	reportexportapplication "github.com/domainry/domainry-runtime/runtime/application/report/export/application"
	reportadapter "github.com/domainry/domainry-runtime/runtime/modulehost/report"
)

type reportModuleExportHost struct {
	service *reportexportapplication.ReportExportApplicationService
}

func (h reportModuleExportHost) PrepareReportExport(ctx context.Context, request reportmodel.ReportExportPrepareRequest, report reportmodel.ReportSchema, control reportmodel.ReportExportControlSchema, subject reportmodel.ReportSubject) (reportmodel.ReportExportJob, error) {
	if h.service == nil {
		return reportmodel.ReportExportJob{}, reportadapter.StableReportHostError(nil)
	}
	result, err := h.service.PrepareResolvedExport(ctx, request, report, control, reportadapter.RuntimePrincipalFromReportSubject(subject))
	if err != nil {
		return reportmodel.ReportExportJob{}, reportadapter.StableReportHostError(err)
	}
	return result, nil
}

var _ reportmodulehost.ExportGateway = reportModuleExportHost{}
