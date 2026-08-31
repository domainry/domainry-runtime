package query

import (
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportengine "github.com/domainry/domainry-report/query/engine"
)

func reportApplyRuntimeQuery(rows []reportDatasetRow, query *reportmodel.ReportDatasetRuntimeQuery) []reportDatasetRow {
	return reportRuntimeRows(reportengine.ApplyRuntimeQuery(reportEngineRows(rows), query))
}

func reportApplyRuntimeTags(rows []reportDatasetRow, tags *reportmodel.ReportDatasetRuntimeTags) []reportDatasetRow {
	return reportRuntimeRows(reportengine.ApplyRuntimeTags(reportEngineRows(rows), tags))
}
