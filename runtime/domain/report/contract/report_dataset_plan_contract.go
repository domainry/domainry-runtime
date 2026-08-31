package contract

import (
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportcontract "github.com/domainry/domainry-report/contract"
)

// BuildReportDatasetPlan is the Runtime compatibility facade for the
// Report-owned plan compiler.
func BuildReportDatasetPlan(report reportmodel.ReportSchema) (reportmodel.ReportDatasetPlan, error) {
	return reportcontract.BuildReportDatasetPlan(report)
}
