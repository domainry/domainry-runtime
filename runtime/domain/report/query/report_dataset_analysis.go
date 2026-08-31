package query

import (
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportengine "github.com/domainry/domainry-report/query/engine"
)

func reportExecuteDatasetAnalyses(rows []reportDatasetRow, dataset reportmodel.ReportDatasetSchema) ([]reportmodel.ReportAnalysisResult, error) {
	return reportengine.ExecuteAnalyses(reportEngineRows(rows), dataset)
}
