package query

import (
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	reportdataset "github.com/domainry/domainry-runtime/runtime/domain/report/query/dataset"
)

type reportDatasetRow reportdataset.ReportDatasetRow

func reportCopyDatasetRow(row reportDatasetRow) reportDatasetRow {
	return reportDatasetRow(reportdataset.CopyRow(reportdataset.ReportDatasetRow(row)))
}
func reportDatasetFieldValue(row reportDatasetRow, field reportmodel.ReportDatasetField) (any, bool) {
	return reportdataset.FieldValue(reportdataset.ReportDatasetRow(row), field)
}
func reportRecordPointerValue(record *recordmodel.Record, field string) (any, bool) {
	return reportdataset.RecordPointerValue(record, field)
}
func reportRecordValue(record recordmodel.Record, field string) (any, bool) {
	return reportdataset.RecordValue(record, field)
}
